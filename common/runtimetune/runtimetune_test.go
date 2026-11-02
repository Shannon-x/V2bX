package runtimetune

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"1024":    1024,
		"1024B":   1024,
		"512KiB":  512 << 10,
		"800K":    800 << 10,
		"1536MiB": 1536 << 20,
		"1536M":   1536 << 20,
		"1536MB":  1536 << 20,
		"2GiB":    2 << 30,
		"2G":      2 << 30,
		"1.5G":    3 << 29,
		" 2 GiB ": 2 << 30,
		"1TiB":    1 << 40,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "1XB", "-5M", "0"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) expected error", bad)
		}
	}
}

func TestFormatSize(t *testing.T) {
	if got := FormatSize(2 << 30); got != "2.00GiB" {
		t.Errorf("FormatSize(2GiB) = %q", got)
	}
	if got := FormatSize(300 << 20); got != "300MiB" {
		t.Errorf("FormatSize(300MiB) = %q", got)
	}
}

func TestReadMemInfoTotal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(p, []byte("MemTotal:        4015532 kB\nMemFree:          123 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := readMemInfoTotal(p), int64(4015532)<<10; got != want {
		t.Fatalf("readMemInfoTotal = %d, want %d", got, want)
	}
	if got := readMemInfoTotal(filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("missing file should yield 0, got %d", got)
	}
}

func TestReadCgroupFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := readCgroupFile(write("max", "max\n")); got != 0 {
		t.Errorf("'max' should be unlimited, got %d", got)
	}
	if got := readCgroupFile(write("v1unlimited", "9223372036854771712\n")); got != 0 {
		t.Errorf("v1 sentinel should be unlimited, got %d", got)
	}
	if got, want := readCgroupFile(write("limit", "2147483648\n")), int64(2<<30); got != want {
		t.Errorf("limit = %d, want %d", got, want)
	}
}

// restoreRuntime snapshots GOGC / memory limit so Apply tests cannot leak
// into the rest of the test binary.
func restoreRuntime(t *testing.T) {
	t.Helper()
	gc := debug.SetGCPercent(100)
	debug.SetGCPercent(gc)
	ml := debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		debug.SetGCPercent(gc)
		debug.SetMemoryLimit(ml)
	})
}

func TestApplyExplicitValues(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOGC", "")
	t.Setenv("GOMEMLIMIT", "")

	r := Apply(Options{GCPercent: 50, MemoryLimit: "1536MiB"})
	if r.GCPercent != 50 || r.GCSource != "config" {
		t.Fatalf("GC: %+v", r)
	}
	if got := debug.SetGCPercent(50); got != 50 {
		t.Fatalf("runtime GOGC = %d, want 50", got)
	}
	if r.MemoryLimit != 1536<<20 {
		t.Fatalf("MemoryLimit = %d, want %d", r.MemoryLimit, 1536<<20)
	}
	if got := debug.SetMemoryLimit(-1); got != 1536<<20 {
		t.Fatalf("runtime memory limit = %d, want %d", got, 1536<<20)
	}
	if len(r.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", r.Warnings)
	}
}

func TestApplyEnvWins(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOGC", "80")
	t.Setenv("GOMEMLIMIT", "3GiB")
	before := debug.SetGCPercent(100)
	debug.SetGCPercent(before)

	r := Apply(Options{GCPercent: 50, MemoryLimit: "1GiB", MemoryLimitPercent: 70})
	if r.GCPercent != -1 || r.GCSource != "env GOGC=80" {
		t.Fatalf("env GOGC must win: %+v", r)
	}
	if r.MemoryLimit != -1 || r.MemorySource != "env GOMEMLIMIT=3GiB" {
		t.Fatalf("env GOMEMLIMIT must win: %+v", r)
	}
	if got := debug.SetGCPercent(before); got != before {
		t.Fatalf("GOGC was modified despite env override: %d", got)
	}
}

func TestApplyPercentClampAndMinimum(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOGC", "")
	t.Setenv("GOMEMLIMIT", "")

	r := Apply(Options{MemoryLimit: "1MiB"})
	if r.MemoryLimit != minMemoryLimit {
		t.Fatalf("tiny limit should clamp to %d, got %d", minMemoryLimit, r.MemoryLimit)
	}

	r = Apply(Options{MemoryLimitPercent: 700})
	if len(r.Warnings) == 0 {
		t.Fatal("expected clamp warning for 700%")
	}
	if r.TotalMemory > 0 && r.MemoryLimit > r.TotalMemory {
		t.Fatalf("limit %d exceeds detected memory %d", r.MemoryLimit, r.TotalMemory)
	}
}

func TestApplyInvalidSizeFallsBackToPercent(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOGC", "")
	t.Setenv("GOMEMLIMIT", "")

	r := Apply(Options{MemoryLimit: "lots", MemoryLimitPercent: 50})
	if len(r.Warnings) == 0 {
		t.Fatal("expected warning for unparseable MemoryLimit")
	}
	if r.TotalMemory > 0 {
		want := r.TotalMemory / 100 * 50
		if r.MemoryLimit != want {
			t.Fatalf("MemoryLimit = %d, want 50%% of %d = %d", r.MemoryLimit, r.TotalMemory, want)
		}
	} else if r.MemorySource != "disabled" {
		t.Fatalf("without detectable memory the limit must stay disabled: %+v", r)
	}
}

func TestApplyNothing(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOGC", "")
	t.Setenv("GOMEMLIMIT", "")
	r := Apply(Options{})
	if r.GCPercent != -1 || r.MemoryLimit != -1 || r.MemorySource != "disabled" {
		t.Fatalf("zero options must not touch the runtime: %+v", r)
	}
}

func TestSamplerProducesSaneStats(t *testing.T) {
	var s Sampler
	runtime.GC() // /gc/heap/live is only populated after a cycle
	first := s.Sample()
	if first.Total <= 0 || first.HeapLive <= 0 || first.Goroutines <= 0 || first.NumCPU <= 0 {
		t.Fatalf("first sample has zero fields: %+v", first)
	}
	if first.GCCPUShare != 0 || first.MemLimited {
		t.Fatalf("first sample must not report deltas: %+v", first)
	}
	// Generate some garbage so a GC cycle and CPU time accrue.
	var sink [][]byte
	for i := 0; i < 2000; i++ {
		sink = append(sink, make([]byte, 64<<10))
	}
	runtime.GC()
	_ = sink
	second := s.Sample()
	if second.GCCPUShare < 0 || second.GCCPUShare > 1 {
		t.Fatalf("GCCPUShare out of range: %v", second.GCCPUShare)
	}
	if second.GCCycles < first.GCCycles {
		t.Fatalf("GC cycles went backwards: %d -> %d", first.GCCycles, second.GCCycles)
	}
	if !second.Time.After(first.Time) && !second.Time.Equal(first.Time) {
		t.Fatal("timestamps must be monotonic")
	}
}
