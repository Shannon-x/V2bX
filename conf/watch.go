package conf

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const reloadDebounce = 5 * time.Second

// Watch follows exact paths through atomic replacements by watching their parent
// directories. A single loop debounces and applies changes serially. The callback
// must not call Watch or StopWatch itself: both wait for the active callback.
func (p *Conf) Watch(filePath, xDnsPath, sDnsPath string, reload func()) error {
	return p.watch(filePath, xDnsPath, sDnsPath, reload, reloadDebounce)
}

func (p *Conf) watch(filePath, xDnsPath, sDnsPath string, reload func(), delay time.Duration) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new watcher: %w", err)
	}
	paths := make(map[string]bool)
	dirs := make(map[string]bool)
	targets := make(map[string]string)
	symlinkDirs := make(map[string]bool)
	// Reconcile after replacements too: a symlink can point to a new target.
	refreshPaths := func() error {
		newPaths, newDirs := make(map[string]bool), make(map[string]bool)
		newTargets, newSymlinkDirs := make(map[string]string), make(map[string]bool)
		for _, path := range []string{filePath, xDnsPath, sDnsPath} {
			if path == "" {
				continue
			}
			path, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			newPaths[path], newDirs[filepath.Dir(path)] = true, true
			newTargets[path] = path
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
				newSymlinkDirs[filepath.Dir(path)] = true
			}
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			newPaths[target], newDirs[filepath.Dir(target)] = true, true
			newTargets[path] = target
			if target != path {
				newSymlinkDirs[filepath.Dir(path)] = true
			}
		}
		for dir := range newDirs {
			if !dirs[dir] {
				if err := watcher.Add(dir); err != nil {
					return fmt.Errorf("watch directory %s: %w", dir, err)
				}
				dirs[dir] = true
			}
		}
		for dir := range dirs {
			if !newDirs[dir] {
				_ = watcher.Remove(dir)
				delete(dirs, dir)
			}
		}
		paths = newPaths
		targets, symlinkDirs = newTargets, newSymlinkDirs
		return nil
	}
	if err := refreshPaths(); err != nil {
		watcher.Close()
		return err
	}
	p.watcherMu.Lock()
	defer p.watcherMu.Unlock()
	p.stopWatchLocked()
	done, exited := make(chan struct{}), make(chan struct{})
	p.watcherDone, p.watcherExited = done, exited
	go func() {
		defer close(exited)
		defer watcher.Close()
		var timer *time.Timer
		var timerCh <-chan time.Time
		pendingRelevant := false
		var linkTicker *time.Ticker
		var linkTick <-chan time.Time
		syncLinkTicker := func() {
			if len(symlinkDirs) > 0 && linkTicker == nil {
				linkTicker = time.NewTicker(time.Second)
				linkTick = linkTicker.C
			} else if len(symlinkDirs) == 0 && linkTicker != nil {
				linkTicker.Stop()
				linkTicker, linkTick = nil, nil
			}
		}
		syncLinkTicker()
		schedule := func() {
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				timer.Stop()
				timer.Reset(delay)
			}
			timerCh = timer.C
		}
		defer func() {
			if timer != nil {
				timer.Stop()
			}
			if linkTicker != nil {
				linkTicker.Stop()
			}
		}()
		errorsCh := watcher.Errors
		for {
			select {
			case <-done:
				return
			case <-linkTick:
				// kqueue can omit every event for a symlink replaced before its
				// directory rescan. Only symlink configurations need this fallback.
				for path, previous := range targets {
					if !symlinkDirs[filepath.Dir(path)] {
						continue
					}
					target, err := filepath.EvalSymlinks(path)
					if err != nil {
						target = path
					}
					if target != previous && !pendingRelevant {
						pendingRelevant = true
						schedule()
					}
				}
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
					continue
				}
				if paths[filepath.Clean(event.Name)] {
					pendingRelevant = true
				} else if !symlinkDirs[filepath.Dir(event.Name)] {
					continue
				}
				// kqueue may only report a temporary symlink's creation during
				// rename. Recheck targets after the burst without reloading for
				// unrelated files when the target is unchanged.
				schedule()
			case watchErr, ok := <-errorsCh:
				if !ok {
					errorsCh = nil
					continue
				}
				log.Printf("File watcher error: %v", watchErr)
				if errors.Is(watchErr, fsnotify.ErrEventOverflow) {
					pendingRelevant = true
					schedule()
				}
			case <-timerCh:
				timerCh = nil
				select {
				case <-done:
					return
				default:
				}
				relevant := pendingRelevant
				pendingRelevant = false
				oldTargets := targets
				if err := refreshPaths(); err != nil {
					log.Printf("refresh watched paths: %v", err)
				}
				syncLinkTicker()
				for path, target := range targets {
					if oldTargets[path] != target {
						relevant = true
					}
				}
				if !relevant {
					continue
				}
				newConf := New()
				if err := newConf.LoadFromPath(filePath); err != nil {
					log.Printf("reload config error: %s", err)
					continue
				}
				p.mu.Lock()
				p.LogConfig = newConf.LogConfig
				p.CoresConfig = newConf.CoresConfig
				p.NodeConfig = newConf.NodeConfig
				p.mu.Unlock()
				reload()
			}
		}
	}()
	return nil
}

func (p *Conf) stopWatchLocked() {
	if p.watcherDone != nil {
		close(p.watcherDone)
		<-p.watcherExited
		p.watcherDone, p.watcherExited = nil, nil
	}
}

// StopWatch returns only after an in-flight reload and the watcher have exited.
func (p *Conf) StopWatch() {
	p.watcherMu.Lock()
	defer p.watcherMu.Unlock()
	p.stopWatchLocked()
}
