package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// The kernel says the machine's memory to the syscall as /proc/meminfo says
// it, which a bundle in the agent may not read.
func TestTotalMemoryIsMemTotal(t *testing.T) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatal(err)
	}
	var kb int64
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ = strconv.ParseInt(f[1], 10, 64)
		}
	}
	got, ok := totalMemory()
	if !ok || got>>10 != kb {
		t.Errorf("totalMemory() = %d, %v; /proc/meminfo has MemTotal %d kB", got, ok, kb)
	}
}
