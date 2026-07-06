package xray

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/conf"
	clog "github.com/xtls/xray-core/common/log"
)

func restoreDefaultRotateOptions() {
	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: 100, MaxBackups: 0, MaxDays: 90, Compress: true})
}

func TestResolveAccessLogPath(t *testing.T) {
	dir := t.TempDir()
	orig := defaultAccessLogPath
	defaultAccessLogPath = filepath.Join(dir, "sub", "access.log")
	defer func() { defaultAccessLogPath = orig }()

	if got := resolveAccessLogPath(""); got != defaultAccessLogPath {
		t.Fatalf("empty path: got %q, want default %q", got, defaultAccessLogPath)
	}
	if _, err := os.Stat(defaultAccessLogPath); err != nil {
		t.Fatalf("default access log not pre-created: %v", err)
	}
	if got := resolveAccessLogPath("console"); got != "" {
		t.Fatalf("console: got %q, want empty (xray console)", got)
	}
	if got := resolveAccessLogPath("stdout"); got != "" {
		t.Fatalf("stdout: got %q, want empty (xray console)", got)
	}
	if got := resolveAccessLogPath("none"); got != "none" {
		t.Fatalf("none: got %q, want none", got)
	}
	custom := filepath.Join(dir, "custom.log")
	if got := resolveAccessLogPath(custom); got != custom {
		t.Fatalf("custom: got %q, want %q", got, custom)
	}
}

func TestResolveAccessLogPathFallsBackToConsole(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	orig := defaultAccessLogPath
	// Parent is a regular file, so MkdirAll must fail.
	defaultAccessLogPath = filepath.Join(blocker, "access.log")
	defer func() { defaultAccessLogPath = orig }()

	if got := resolveAccessLogPath(""); got != "" {
		t.Fatalf("unwritable default: got %q, want empty (console fallback)", got)
	}
}

func TestRotateWriterKeepsXrayTimestampFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	w := newRotateWriter(path)
	// xray's generalLogger hands over lines with the separator appended.
	if err := w.Write("from 1.2.3.4:1 accepted tcp:example.com:443\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatalf("expected exactly one line, got %q", data)
	}
	line := strings.TrimRight(string(data), "\n")
	want := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6} from 1\.2\.3\.4:1 accepted tcp:example\.com:443$`)
	if !want.MatchString(line) {
		t.Fatalf("log line lost xray timestamp format: %q", line)
	}
}

// xray's generalLogger recreates and closes its Writer on every activity
// burst. A naive per-writer lumberjack.Logger leaks one millRun goroutine per
// cycle, so writer churn on one path must not grow the goroutine count.
func TestWriterChurnDoesNotLeakGoroutines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	w := newRotateWriter(path)
	if err := w.Write("warmup\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 40; i++ {
		w := newRotateWriter(path)
		if err := w.Write("line\n"); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after-before > 5 {
		t.Fatalf("goroutines grew from %d to %d across writer churn — rotator not shared", before, after)
	}
}

// The 90-day retention guarantee lives or dies on these fields reaching
// lumberjack; assert the wiring, not just the intermediate options struct.
func TestRotatorWiringMatchesConfig(t *testing.T) {
	defer restoreDefaultRotateOptions()
	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: 7, MaxBackups: 2, MaxDays: 45, Compress: true})

	r := getRotator(filepath.Join(t.TempDir(), "access.log"))
	r.mu.Lock()
	lj := r.lj
	r.mu.Unlock()
	if lj.MaxSize != 7 {
		t.Fatalf("MaxSize: got %d, want 7", lj.MaxSize)
	}
	if lj.MaxBackups != 2 {
		t.Fatalf("MaxBackups: got %d, want 2", lj.MaxBackups)
	}
	if lj.MaxAge != 45 {
		t.Fatalf("MaxAge (retention days): got %d, want 45", lj.MaxAge)
	}
	if !lj.Compress {
		t.Fatal("Compress: got false, want true")
	}
	if !lj.LocalTime {
		t.Fatal("LocalTime: got false, want true")
	}
}

// Hot reload rebuilds the core and re-applies the log config; existing
// rotators must pick up changed settings instead of keeping the old ones.
func TestApplyLogRotateOptionsReconfiguresExistingRotators(t *testing.T) {
	defer restoreDefaultRotateOptions()
	restoreDefaultRotateOptions()

	r := getRotator(filepath.Join(t.TempDir(), "access.log"))
	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: 100, MaxBackups: 0, MaxDays: 30, Compress: true})

	r.mu.Lock()
	got := r.lj.MaxAge
	r.mu.Unlock()
	if got != 30 {
		t.Fatalf("existing rotator MaxAge: got %d, want 30 after reconfigure", got)
	}
}

func TestDailyRotationSplitsNonEmptyFileOnly(t *testing.T) {
	defer restoreDefaultRotateOptions()
	// Compression renames backups asynchronously; disable it so the
	// directory listing below is deterministic.
	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: 100, MaxBackups: 0, MaxDays: 90, Compress: false})

	dir := t.TempDir()
	r := getRotator(filepath.Join(dir, "access.log"))
	if _, err := r.Write([]byte("a line\n")); err != nil {
		t.Fatal(err)
	}

	if err := r.rotateIfNonEmpty(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected live file + one backup after rotation, got %d entries", len(entries))
	}

	// The fresh live file is empty; a second midnight tick must not create
	// an empty backup.
	if err := r.rotateIfNonEmpty(); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("empty live file was rotated into a backup: %d entries", len(entries))
	}
}

// If the live file is deleted externally (rm, non-copytruncate logrotate),
// lumberjack would keep writing the unlinked inode; the write path must
// notice and recreate the file.
func TestWriteRecreatesExternallyDeletedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	r := getRotator(path)
	if _, err := r.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// Force the throttled path check to run on the next write.
	r.mu.Lock()
	r.lastPathCheck = time.Time{}
	r.mu.Unlock()

	if _, err := r.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not recreated after external deletion: %v", err)
	}
	if !strings.Contains(string(data), "second") {
		t.Fatalf("recreated file missing the new line: %q", data)
	}
}

// The midnight tick must also heal a deleted file (and thereby free the
// unlinked inode) on idle nodes where no write triggers the check.
func TestRotateIfNonEmptyHealsDeletedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	r := getRotator(path)
	if _, err := r.Write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := r.rotateIfNonEmpty(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("after heal\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not recreated after midnight heal: %v", err)
	}
}

// Timezones whose spring-forward gap swallows midnight (e.g. Chile) must not
// make the schedule stall: the next rotation instant has to keep advancing.
func TestNextDailyRotationAdvancesAcrossDSTGap(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("tzdata unavailable:", err)
	}
	// 2025-09-07 00:00 local did not exist (clocks jumped 00:00 -> 01:00).
	now := time.Date(2025, 9, 6, 12, 0, 0, 0, loc)
	start := now
	for i := 0; i < 5; i++ {
		next := nextDailyRotation(now)
		if !next.After(now) {
			t.Fatalf("iteration %d: schedule stalled at %v (next %v)", i, now, next)
		}
		now = next
	}
	if now.Sub(start) > 6*24*time.Hour {
		t.Fatalf("schedule drifted too far: advanced %v in 5 steps", now.Sub(start))
	}
}

func TestNextDailyRotation(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 7, 6, 13, 42, 0, 0, loc)
	want := time.Date(2026, 7, 7, 0, 0, 5, 0, loc)
	if got := nextDailyRotation(now); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Just past midnight must schedule the NEXT midnight, not fire again.
	now = time.Date(2026, 7, 6, 0, 0, 6, 0, loc)
	want = time.Date(2026, 7, 7, 0, 0, 5, 0, loc)
	if got := nextDailyRotation(now); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// End-to-end: build an xray core from a default V2bX config and check that an
// access-log record lands in the rotated default file instead of stdout.
func TestAccessLogLandsInDefaultRotatedFile(t *testing.T) {
	dir := t.TempDir()
	orig := defaultAccessLogPath
	defaultAccessLogPath = filepath.Join(dir, "access.log")
	defer func() { defaultAccessLogPath = orig }()

	xc := conf.NewXrayConfig()
	xc.AssetPath = dir
	server := getCore(xc)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	clog.Record(&clog.AccessMessage{
		From:   "203.0.113.7:1234",
		To:     "tcp:example.com:443",
		Status: clog.AccessAccepted,
		Detour: "test-detour",
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(defaultAccessLogPath)
		if err == nil && strings.Contains(string(data), "203.0.113.7:1234 accepted tcp:example.com:443") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("access record never reached %s (err=%v, content=%q)", defaultAccessLogPath, err, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestApplyLogRotateOptionsSanitizesValues(t *testing.T) {
	defer restoreDefaultRotateOptions()

	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: -1, MaxBackups: -2, MaxDays: -3, Compress: false})
	got := currentRotateOptions()
	if got.maxSizeMB != 100 {
		t.Fatalf("maxSizeMB: got %d, want fallback 100", got.maxSizeMB)
	}
	if got.maxBackups != 0 {
		t.Fatalf("maxBackups: got %d, want 0", got.maxBackups)
	}
	if got.maxDays != 0 {
		t.Fatalf("maxDays: got %d, want 0", got.maxDays)
	}
	if got.compress {
		t.Fatal("compress: got true, want false")
	}

	applyLogRotateOptions(&conf.XrayLogConfig{MaxSize: 50, MaxBackups: 3, MaxDays: 30, Compress: true})
	got = currentRotateOptions()
	if got.maxSizeMB != 50 || got.maxBackups != 3 || got.maxDays != 30 || !got.compress {
		t.Fatalf("valid values not applied verbatim: %+v", got)
	}
}
