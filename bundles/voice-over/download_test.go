package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The stand-ins in testdata were made from testdata/src with
//
//	COPYFILE_DISABLE=1 tar --no-xattrs --format ustar -cjf ../fake-sherpa.tar.bz2 fake-sherpa
//
// and the same for fake-parakeet: a program that answers as sherpa-onnx's
// does, and model files that are only their names.

// testdata is the folder of stand-ins, found before a test moves into a
// folder of its own.
var testdata, _ = filepath.Abs("testdata")

// downloads is a stand-in for the place the local backend downloads from:
// it serves testdata, and notes what was asked for and with what range.
type downloads struct {
	mu     sync.Mutex
	got    []string
	ranges []string
}

func (d *downloads) asked() ([]string, []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.got...), append([]string(nil), d.ranges...)
}

// withLocalModels points the local backend at stand-ins for its helper and
// models, served by a test server, with a cache of the test's own.
func withLocalModels(t *testing.T) (*downloads, string) {
	t.Helper()
	d := &downloads{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.got = append(d.got, r.URL.Path)
		d.ranges = append(d.ranges, r.Header.Get("Range"))
		d.mu.Unlock()
		http.ServeFile(w, r, filepath.Join(testdata, filepath.Base(r.URL.Path)))
	}))
	t.Cleanup(srv.Close)
	cache := t.TempDir()
	old := userCache
	userCache = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCache = old })

	pinned := func(title, file string) asset {
		b, err := os.ReadFile(filepath.Join(testdata, file))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return asset{name: strings.TrimSuffix(strings.TrimSuffix(file, ".tar.bz2"), ".onnx"), title: title,
			url: srv.URL + "/" + file, sha256: hex.EncodeToString(sum[:]), size: int64(len(b))}
	}
	oldHelper, hadHelper := helpers[platform]
	oldParakeet, oldSilero := parakeet, silero
	helpers[platform] = pinned("sherpa-onnx 1.13.8", "fake-sherpa.tar.bz2")
	parakeet = pinned("nvidia/parakeet-tdt-0.6b-v3", "fake-parakeet.tar.bz2")
	silero = pinned("Silero VAD v5", "silero_vad_v5.onnx")
	t.Cleanup(func() {
		parakeet, silero = oldParakeet, oldSilero
		if hadHelper {
			helpers[platform] = oldHelper
		} else {
			delete(helpers, platform)
		}
	})
	return d, filepath.Join(cache, "voice-over")
}

// "What runs on this machine is downloaded the first time it is needed, by a
// pinned address and digest, into the user's cache, unpacked, and kept: a
// second call downloads nothing."
func TestDownloadUnpacksOnceAndKeepsIt(t *testing.T) {
	d, cache := withLocalModels(t)

	dir, fetched, err := download(context.Background(), parakeet)
	if err != nil || !fetched {
		t.Fatalf("download = %q, %v, %v; want the archive downloaded", dir, fetched, err)
	}
	if dir != filepath.Join(cache, parakeet.name) {
		t.Errorf("unpacked into %s, want %s", dir, filepath.Join(cache, parakeet.name))
	}
	if got := mustRead(t, filepath.Join(dir, "encoder.int8.onnx")); got != "a stand-in for encoder.int8.onnx\n" {
		t.Errorf("the archive's own folder was not taken off its paths: encoder.int8.onnx holds %q", got)
	}
	vad, _, err := download(context.Background(), silero)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(vad, "silero_vad_v5.onnx")); err != nil {
		t.Errorf("a file that is not an archive is not kept under its own name: %v", err)
	}

	if _, fetched, err := download(context.Background(), parakeet); err != nil || fetched {
		t.Errorf("a second download = %v, %v; want what is there, downloaded nothing", fetched, err)
	}
	if got, _ := d.asked(); len(got) != 2 {
		t.Errorf("asked the server %d times for two files, each once: %v", len(got), got)
	}
	left, _ := filepath.Glob(filepath.Join(cache, "*.part"))
	if len(left) != 0 {
		t.Errorf("a finished download left %v behind", left)
	}
}

// "A download that does not match its digest is thrown away, not used."
func TestDownloadRefusesWhatIsNotPinned(t *testing.T) {
	_, cache := withLocalModels(t)
	wrong := parakeet
	wrong.sha256 = strings.Repeat("0", 64)

	_, _, err := download(context.Background(), wrong)
	if err == nil || !strings.Contains(err.Error(), "digest") || !strings.Contains(err.Error(), "call again") {
		t.Fatalf("download of a file that does not match = %v, want a refusal that names the digest and what to do", err)
	}
	left, _ := os.ReadDir(cache)
	if len(left) != 0 {
		t.Errorf("a download that did not match was kept: %v", left)
	}
}

// "A download cut off, by a host's time limit or a lost connection, carries
// on where it stopped at the next call rather than starting again."
func TestDownloadCarriesOnWhereADownloadStopped(t *testing.T) {
	d, cache := withLocalModels(t)
	whole, err := os.ReadFile(filepath.Join(testdata, "fake-parakeet.tar.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, parakeet.name+".part"), whole[:100], 0o644); err != nil {
		t.Fatal(err)
	}

	dir, _, err := download(context.Background(), parakeet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tokens.txt")); err != nil {
		t.Errorf("the carried-on download did not unpack: %v", err)
	}
	if _, ranges := d.asked(); len(ranges) != 1 || ranges[0] != "bytes=100-" {
		t.Errorf("asked with ranges %q, want the rest from byte 100", ranges)
	}
}

// TestMain keeps every test away from the real downloads, half a gigabyte, and
// from the user's own cache: a test that reaches them without
// withLocalModels fails at once instead. HOME stays as it is: Chrome, which
// the review page's tests start, does not start under a HOME of its own.
func TestMain(m *testing.M) {
	cache, err := os.MkdirTemp("", "voice-over-cache-")
	if err != nil {
		panic(err)
	}
	userCache = func() (string, error) { return cache, nil }
	nowhere := func(a asset) asset { a.url = "http://127.0.0.1:1/" + a.name; return a }
	for p, a := range helpers {
		helpers[p] = nowhere(a)
	}
	parakeet, silero = nowhere(parakeet), nowhere(silero)
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}
