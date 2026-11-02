package runtimetune

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// physicalMemory reads MemTotal from /proc/meminfo.
func physicalMemory() int64 {
	return readMemInfoTotal("/proc/meminfo")
}

func readMemInfoTotal(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return kb << 10
	}
	return 0
}

// cgroupMemoryLimit returns the cgroup memory limit that applies to this
// process (0 when unlimited or undetectable). Docker / systemd slices commonly
// cap a container well below the host's MemTotal, and that cap — not the
// host RAM — is what the OOM killer enforces.
func cgroupMemoryLimit() int64 {
	// cgroup v2 (unified hierarchy).
	if v := readCgroupFile("/sys/fs/cgroup/memory.max"); v > 0 {
		return v
	}
	// cgroup v1.
	if v := readCgroupFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); v > 0 {
		return v
	}
	return 0
}

// readCgroupFile parses a single-value cgroup memory file. "max" and the
// v1 "effectively unlimited" sentinel (a huge page-aligned number) both map
// to 0 so callers fall back to physical memory.
func readCgroupFile(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(b))
	if s == "" || s == "max" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0
	}
	// cgroup v1 reports ~2^63 rounded to the page size when no limit is set.
	if v >= 1<<62 {
		return 0
	}
	return v
}
