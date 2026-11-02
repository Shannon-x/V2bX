// Package runtimetune applies process-wide Go runtime tuning (GC percent and
// soft memory limit) from V2bX configuration.
//
// Why this exists: a proxy node keeps a large, long-lived working set (one
// TLS/REALITY state, several goroutine stacks and a pair of pipe buffers per
// connection — tens of KB each, times tens of thousands of connections). With
// Go's default GOGC=100 the garbage collector lets the heap grow to roughly
// twice that live set before collecting, so the process RSS is about 2× what
// the connections actually need. Lowering GOGC trades a little CPU for a much
// smaller heap ceiling, and a soft memory limit (GOMEMLIMIT) makes the runtime
// collect more aggressively as it approaches a hard budget instead of letting
// the kernel OOM-kill the node.
//
// Environment variables always win: if GOGC or GOMEMLIMIT is set in the
// process environment we leave the corresponding knob untouched, so operators
// can still override everything through systemd `Environment=`.
package runtimetune

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Options are the resolved (non-pointer) runtime knobs. Zero values mean "do
// not touch"; see Apply for the exact semantics of each field.
type Options struct {
	// GCPercent is the GOGC value to apply. <= 0 leaves the Go default (100).
	GCPercent int
	// MemoryLimitPercent sets the soft memory limit to this percentage of the
	// memory available to the process (min of physical RAM and the cgroup
	// limit). <= 0 disables the percentage-based limit.
	MemoryLimitPercent int
	// MemoryLimit, when non-empty, is an absolute soft limit such as "1536MiB"
	// or "2GiB" and takes precedence over MemoryLimitPercent.
	MemoryLimit string
}

// Result describes what Apply actually did, for logging.
type Result struct {
	// GCPercent is the effective GOGC after Apply, or -1 when untouched.
	GCPercent int
	// GCSource explains where GCPercent came from ("env", "config", "default").
	GCSource string
	// MemoryLimit is the effective soft limit in bytes, or -1 when untouched.
	MemoryLimit int64
	// MemorySource explains where MemoryLimit came from.
	MemorySource string
	// TotalMemory is the detected memory available to the process (0 when
	// unknown). Only populated when a percentage-based limit was requested.
	TotalMemory int64
	// Warnings lists non-fatal problems (unparseable sizes, undetectable RAM).
	Warnings []string
}

// maxMemoryLimitPercent caps percentage-based limits so a typo like 700 cannot
// request more memory than the machine has. The runtime also needs headroom
// for the kernel's own socket buffers, which are not part of the Go heap.
const maxMemoryLimitPercent = 95

// Apply installs the requested GC percent and soft memory limit. It never
// panics and never lowers the memory limit below 64 MiB.
func Apply(o Options) Result {
	r := Result{GCPercent: -1, MemoryLimit: -1}

	if v := os.Getenv("GOGC"); v != "" {
		r.GCSource = "env GOGC=" + v
	} else if o.GCPercent > 0 {
		debug.SetGCPercent(o.GCPercent)
		r.GCPercent = o.GCPercent
		r.GCSource = "config"
	} else {
		r.GCSource = "go default"
	}

	if v := os.Getenv("GOMEMLIMIT"); v != "" {
		r.MemorySource = "env GOMEMLIMIT=" + v
		return r
	}

	if o.MemoryLimit != "" {
		limit, err := ParseSize(o.MemoryLimit)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("invalid Runtime.MemoryLimit %q: %v", o.MemoryLimit, err))
		} else {
			r.MemoryLimit = clampLimit(limit)
			r.MemorySource = "config MemoryLimit"
			debug.SetMemoryLimit(r.MemoryLimit)
			return r
		}
	}

	if o.MemoryLimitPercent <= 0 {
		if r.MemorySource == "" {
			r.MemorySource = "disabled"
		}
		return r
	}
	pct := o.MemoryLimitPercent
	if pct > maxMemoryLimitPercent {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Runtime.MemoryLimitPercent %d clamped to %d", pct, maxMemoryLimitPercent))
		pct = maxMemoryLimitPercent
	}
	total := TotalMemory()
	r.TotalMemory = total
	if total <= 0 {
		r.Warnings = append(r.Warnings, "cannot detect total memory; percentage-based memory limit not applied (set Runtime.MemoryLimit explicitly)")
		r.MemorySource = "disabled"
		return r
	}
	r.MemoryLimit = clampLimit(total / 100 * int64(pct))
	r.MemorySource = fmt.Sprintf("%d%% of %s", pct, FormatSize(total))
	debug.SetMemoryLimit(r.MemoryLimit)
	return r
}

// minMemoryLimit guards against absurdly small limits that would make the
// collector spin continuously.
const minMemoryLimit = 64 << 20

func clampLimit(limit int64) int64 {
	if limit < minMemoryLimit {
		return minMemoryLimit
	}
	return limit
}

// ParseSize parses a human-readable byte size. Accepted forms match
// GOMEMLIMIT ("1024MiB", "2GiB", "512KiB", "1TiB", "1000000B") plus the
// common shorthands "2G", "1536M", "800K", "2GB", "1536MB" (binary multiples)
// and a bare number of bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	numStr, unit := s[:i], strings.ToUpper(strings.TrimSpace(s[i:]))
	if numStr == "" {
		return 0, fmt.Errorf("missing number in %q", s)
	}
	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("bad number %q: %w", numStr, err)
	}
	var mult float64
	switch unit {
	case "", "B":
		mult = 1
	case "K", "KB", "KIB":
		mult = 1 << 10
	case "M", "MB", "MIB":
		mult = 1 << 20
	case "G", "GB", "GIB":
		mult = 1 << 30
	case "T", "TB", "TIB":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("unknown unit %q", unit)
	}
	v := num * mult
	if v <= 0 || v > math.MaxInt64 {
		return 0, fmt.Errorf("size %q out of range", s)
	}
	return int64(v), nil
}

// FormatSize renders bytes with a binary unit, e.g. "2.68GiB".
func FormatSize(n int64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.2fGiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.0fMiB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.0fKiB", float64(n)/kib)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// TotalMemory returns the memory available to this process in bytes: the
// smaller of physical RAM and any cgroup (v1 or v2) limit. It returns 0 when
// nothing could be detected (non-Linux platforms, unusual mounts).
func TotalMemory() int64 {
	total := physicalMemory()
	if cg := cgroupMemoryLimit(); cg > 0 && (total == 0 || cg < total) {
		total = cg
	}
	return total
}
