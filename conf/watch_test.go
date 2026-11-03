package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeWatchConfig(t *testing.T, path, level string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"Log":{"Level":%q}}`, level)), 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitWatch(t *testing.T, updates <-chan string, want string) {
	t.Helper()
	select {
	case got := <-updates:
		if got != want {
			t.Fatalf("reload = %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reload did not arrive")
	}
}

func TestWatchAtomicReplacementAndExactPaths(t *testing.T) {
	dir := t.TempDir()
	path, dns := filepath.Join(dir, "config.json"), filepath.Join(dir, "dns.json")
	writeWatchConfig(t, path, "initial")
	c := New()
	updates := make(chan string, 16)
	if err := c.watch(path, dns, "", func() { updates <- c.LogConfig.Level }, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.StopWatch()
	writeWatchConfig(t, path+".backup", "ignore")
	select {
	case <-updates:
		t.Fatal("unrelated file triggered reload")
	case <-time.After(100 * time.Millisecond):
	}
	for _, level := range []string{"first", "second"} {
		tmp := filepath.Join(dir, ".next")
		writeWatchConfig(t, tmp, level)
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
		awaitWatch(t, updates, level)
	}
	// A DNS file that did not exist at startup is still watched.
	if err := os.WriteFile(dns, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitWatch(t, updates, "second")
}

func TestWatchDebouncesToLatestValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchConfig(t, path, "initial")
	c := New()
	updates := make(chan string, 16)
	if err := c.watch(path, "", "", func() { updates <- c.LogConfig.Level }, 80*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.StopWatch()
	for _, level := range []string{"first", "second", "last"} {
		writeWatchConfig(t, path, level)
		time.Sleep(15 * time.Millisecond)
	}
	awaitWatch(t, updates, "last")
	select {
	case got := <-updates:
		t.Fatalf("duplicate reload: %s", got)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-updates:
		t.Fatal("invalid config was published")
	case <-time.After(150 * time.Millisecond):
	}
	writeWatchConfig(t, path, "recovered")
	awaitWatch(t, updates, "recovered")
}

func TestStopWatchWaitsForReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchConfig(t, path, "initial")
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	c := New()
	if err := c.watch(path, "", "", func() { close(entered); <-release }, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	writeWatchConfig(t, path, "changed")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		once.Do(func() { close(release) })
		c.StopWatch()
		t.Fatal("callback not entered")
	}
	go func() { c.StopWatch(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("StopWatch returned while reload was running")
	case <-time.After(40 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("StopWatch did not finish")
	}
	c.StopWatch()
}

func TestWatchSetupFailureDoesNotPublishWorker(t *testing.T) {
	c := New()
	if err := c.watch(filepath.Join(t.TempDir(), "missing", "config.json"), "", "", func() {}, time.Millisecond); err == nil {
		t.Fatal("missing parent accepted")
	}
	if c.watcherDone != nil || c.watcherExited != nil {
		t.Fatal("failed setup published watcher")
	}
	c.StopWatch()
}

func TestWatchSymlinkTargetAndRetarget(t *testing.T) {
	link := filepath.Join(t.TempDir(), "config.json")
	first := filepath.Join(t.TempDir(), "first.json")
	second := filepath.Join(t.TempDir(), "second.json")
	writeWatchConfig(t, first, "initial")
	writeWatchConfig(t, second, "retargeted")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	c := New()
	updates := make(chan string, 16)
	if err := c.watch(link, "", "", func() { updates <- c.LogConfig.Level }, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.StopWatch()
	writeWatchConfig(t, first, "target-change")
	awaitWatch(t, updates, "target-change")
	if err := os.Symlink(second, link+".next"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(link+".next", link); err != nil {
		t.Fatal(err)
	}
	awaitWatch(t, updates, "retargeted")
	writeWatchConfig(t, second, "new-target-change")
	awaitWatch(t, updates, "new-target-change")
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	writeWatchConfig(t, second, "recreated-target")
	awaitWatch(t, updates, "recreated-target")
}

func TestWatchSymlinkParentRetarget(t *testing.T) {
	root, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	link := filepath.Join(root, "current")
	writeWatchConfig(t, filepath.Join(first, "config.json"), "initial")
	writeWatchConfig(t, filepath.Join(second, "config.json"), "new-directory")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	c := New()
	updates := make(chan string, 16)
	if err := c.watch(filepath.Join(link, "config.json"), "", "", func() { updates <- c.LogConfig.Level }, 30*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer c.StopWatch()
	if err := os.Symlink(second, link+".next"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(link+".next", link); err != nil {
		t.Fatal(err)
	}
	awaitWatch(t, updates, "new-directory")
	writeWatchConfig(t, filepath.Join(second, "config.json"), "new-directory-write")
	awaitWatch(t, updates, "new-directory-write")
}
