package main

import "syscall"

// totalMemory is the machine's memory, as /proc/meminfo's MemTotal says it,
// asked of the kernel directly: a bundle in the agent may not read /proc.
func totalMemory() (int64, bool) {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return 0, false
	}
	b := int64(si.Totalram) * int64(si.Unit)
	return b, b > 0
}
