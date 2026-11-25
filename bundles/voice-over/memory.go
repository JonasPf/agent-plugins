package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// What the local backend's work takes at most, in one run of sherpa-onnx's
// program, measured on an M-series Mac: Parakeet hearing two 30 s chunks
// peaked at 1.9 GB (23 Nov 2026), Kokoro speaking a line of 423 characters at
// 0.6 GB (25 Nov 2026). Where less is allowed, the kernel ends the program
// partway, after a download of hundreds of MB, so the work is refused first.
var (
	forHearing  = memoryNeed{"Transcribing", 2 << 30}
	forSpeaking = memoryNeed{"Speaking", 1 << 30}
)

type memoryNeed struct {
	work  string
	bytes int64
}

// sysRoot is where /proc and /sys are read from, and machineMemory says how
// much memory the machine has; tests give their own.
var (
	sysRoot       = "/"
	machineMemory = totalMemory
)

// memoryAllowed is the most memory this process may use, and who sets it, as
// a refusal says it.
type memoryAllowed struct {
	bytes int64
	of    string
}

// memoryLimit is the tightest of the machine's memory and the limit of each
// cgroup this process is in, up to the root: a container's limit is its
// cgroup's, or a parent's. ok is false where none is known, as on a Mac,
// which no Mac this runs on is short of.
func memoryLimit() (memoryAllowed, bool) {
	var got memoryAllowed
	if b, ok := machineMemory(); ok {
		got = memoryAllowed{b, "this machine has"}
	}
	for _, b := range cgroupLimits() {
		if got.bytes == 0 || b < got.bytes {
			got = memoryAllowed{b, "the container it runs in allows"}
		}
	}
	return got, got.bytes != 0
}

// cgroupLimits are the memory limits set on this process's cgroups, from
// /proc/self/cgroup: cgroup v2's memory.max, or v1's memory.limit_in_bytes,
// at its own cgroup and each above it. A cgroup with no limit says max (v2)
// or a number near 2^63 (v1), which is never the tightest.
//
// The agent lets a bundle read /sys/fs/cgroup and not /proc, whose
// /proc/<pid>/environ would hand it every process's secrets. Without
// /proc/self/cgroup, this process's cgroup is taken to be the root of what it
// sees, as in a container, which has a cgroup namespace of its own.
func cgroupLimits() []int64 {
	b, err := os.ReadFile(filepath.Join(sysRoot, "proc", "self", "cgroup"))
	if err != nil {
		b = []byte("0::/\n1:memory:/\n")
	}
	var limits []int64
	for line := range strings.Lines(string(b)) {
		f := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(f) != 3 {
			continue
		}
		var base, file string
		switch {
		case f[0] == "0" && f[1] == "":
			base, file = filepath.Join(sysRoot, "sys", "fs", "cgroup"), "memory.max"
		case slices.Contains(strings.Split(f[1], ","), "memory"):
			base, file = filepath.Join(sysRoot, "sys", "fs", "cgroup", "memory"), "memory.limit_in_bytes"
		default:
			continue
		}
		for p := f[2]; ; p = filepath.Dir(p) {
			if s, err := os.ReadFile(filepath.Join(base, p, file)); err == nil {
				if n, err := strconv.ParseInt(strings.TrimSpace(string(s)), 10, 64); err == nil && n > 0 {
					limits = append(limits, n)
				}
			}
			if p == "/" || p == "." {
				break
			}
		}
	}
	return limits
}

// roomFor refuses work this process has not the memory for, saying how much
// it has and how to have OpenRouter do it instead.
func roomFor(n memoryNeed) error {
	got, ok := memoryLimit()
	if !ok || got.bytes >= n.bytes {
		return nil
	}
	return fmt.Errorf("%s on this machine needs about %s of memory, and %s %s, so it would be ended partway. "+
		"The operator sets an OpenRouter API key (%s) in this bundle's settings, with the backend auto or openrouter; "+
		"until then, voiceover_frames still works", n.work, size(n.bytes), got.of, size(got.bytes), keyName)
}

// size is a number of bytes as an operator sets a container's memory: in MB
// below a GB, else in GB to the tenth.
func size(b int64) string {
	if b < 1<<30 {
		return fmt.Sprintf("%d MB", b>>20)
	}
	return strings.Replace(fmt.Sprintf("%.1f GB", float64(b)/(1<<30)), ".0 GB", " GB", 1)
}
