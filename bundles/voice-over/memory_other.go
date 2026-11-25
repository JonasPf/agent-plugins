//go:build !linux

package main

// totalMemory is not asked for off Linux: the local backend runs on a Mac
// only on Apple silicon, none of which has less than 8 GB.
func totalMemory() (int64, bool) { return 0, false }
