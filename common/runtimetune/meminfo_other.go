//go:build !linux

package runtimetune

// physicalMemory is only implemented for Linux; other platforms must set
// Runtime.MemoryLimit explicitly to get a soft limit.
func physicalMemory() int64 { return 0 }

func cgroupMemoryLimit() int64 { return 0 }
