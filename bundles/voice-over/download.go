package main

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// What the local backend runs on is not in the bundle: the models are
// hundreds of megabytes. Each is downloaded the first time it is needed, from
// an address pinned with its digest, into the user's cache, and kept there.

// asset is one thing to download: a .tar.bz2 archive, unpacked without its
// own top folder, or a single file, kept under its own name.
type asset struct {
	name   string // its folder in the cache
	title  string // as a result names it
	url    string
	sha256 string
	size   int64
}

// userCache is the user's cache folder; tests give one of their own.
var userCache = os.UserCacheDir

func cacheRoot() (string, error) {
	d, err := userCache()
	if err != nil {
		return "", fmt.Errorf("there is no cache folder to download into: %v", err)
	}
	return filepath.Join(d, "voice-over"), nil
}

// download returns the folder an asset unpacks to, downloading it first if it
// is not there yet, and says whether it did. A download that stops part way,
// at a host's time limit or a lost connection, is kept as <name>.part and
// carried on from where it stopped by the next call.
func download(ctx context.Context, a asset) (string, bool, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(root, a.name)
	if _, err := os.Stat(dir); err == nil {
		return dir, false, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, fmt.Errorf("could not make %s: %v", root, err)
	}
	part := dir + ".part"
	if err := fetchInto(ctx, a, part); err != nil {
		return "", false, fmt.Errorf("could not download %s from %s: %v. What arrived is kept, and the next call carries on from there", a.title, a.url, err)
	}
	if err := checkDigest(part, a.sha256); err != nil {
		os.Remove(part)
		return "", false, fmt.Errorf("the download of %s is not what was pinned (%v), so it was thrown away: call again to download it anew", a.title, err)
	}
	tmp, err := os.MkdirTemp(root, a.name+".unpack-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(tmp)
	if strings.HasSuffix(a.url, ".tar.bz2") {
		err = untar(part, tmp)
	} else {
		err = os.Rename(part, filepath.Join(tmp, path.Base(a.url)))
	}
	if err != nil {
		return "", false, fmt.Errorf("could not unpack %s: %v", a.title, err)
	}
	// Another call may have finished the same download first; either copy
	// is the pinned one.
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr != nil {
			return "", false, err
		}
	}
	os.Remove(part)
	return dir, true, nil
}

// fetchInto appends to part what it does not hold yet.
func fetchInto(ctx context.Context, a asset, part string) error {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	have, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if have == a.size {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.url, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK && have > 0:
		// The server sends the whole file again rather than the rest.
		if err := f.Truncate(0); err != nil {
			return err
		}
	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusPartialContent && have > 0:
	default:
		return errors.New(resp.Status)
	}
	_, err = io.Copy(f, resp.Body)
	return err
}

func checkDigest(file, want string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("sha256 digest %s, pinned %s", got, want)
	}
	return nil
}

// untar unpacks a .tar.bz2 into dir, each path without the archive's own top
// folder. The archives are pinned by digest, and hold only folders and files.
func untar(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(bzip2.NewReader(f))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, rest, _ := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if rest == "" {
			continue
		}
		to := filepath.Join(dir, filepath.FromSlash(rest))
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(to, 0o755)
		case tar.TypeReg:
			err = writeFile(to, tr, h.FileInfo().Mode().Perm())
		default:
			err = fmt.Errorf("%s is neither a folder nor a file", h.Name)
		}
		if err != nil {
			return err
		}
	}
}

func writeFile(to string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
