package conf

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// reloadDebounce coalesces fsnotify event bursts (atomic save → 2-3 events
// fired in <100ms by editors). W5.2: kept as a single constant so the
// debounce window and the apply delay agree.
const reloadDebounce = 5 * time.Second

// Watch arms an fsnotify watcher on the config + optional DNS files and
// returns when the watcher goroutine is running. Reload events are coalesced
// and delivered to `reload` SERIALLY from a single consumer goroutine —
// concurrent fsnotify events can never spawn parallel reloads racing on the
// caller's shared state. W2 Mode B / audit #15 #30 #60.
//
// `reload` is called with the new Conf already swapped into `p`'s fields
// under p.mu, so callers can read updated values without further locking.
func (p *Conf) Watch(filePath, xDnsPath string, sDnsPath string, reload func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("new watcher error: %s", err)
	}
	p.watcherMu.Lock()
	if p.watcherDone != nil {
		close(p.watcherDone)
	}
	p.watcherDone = make(chan struct{})
	done := p.watcherDone
	p.watcherMu.Unlock()

	// Buffered size 1: collapses N events into "at-most-one pending reload".
	// While the consumer is mid-reload, additional events are dropped (the
	// already-pending signal will trigger a single follow-up reload that
	// picks up any further changes).
	reloadCh := make(chan string, 1)

	// Watcher goroutine: only owns the watcher + posts signals to reloadCh.
	// No heavy work happens here, so we never block fsnotify's internal queue.
	go func() {
		defer watcher.Close()
		var pre time.Time
		for {
			select {
			case <-done:
				return
			case e := <-watcher.Events:
				if e.Has(fsnotify.Chmod) {
					continue
				}
				if pre.Add(reloadDebounce).After(time.Now()) {
					continue
				}
				pre = time.Now()
				// Non-blocking send: skip if a reload is already queued.
				select {
				case reloadCh <- e.Name:
				default:
				}
			case wErr := <-watcher.Errors:
				if wErr != nil {
					log.Printf("File watcher error: %s", wErr)
				}
			}
		}
	}()

	// Consumer goroutine: single-threaded reload loop. Serializes reload
	// calls so the caller's per-reload state mutation (`vc`, nodes, etc.)
	// can never race against itself.
	go func() {
		for {
			select {
			case <-done:
				return
			case name := <-reloadCh:
				// Brief delay to let the editor's atomic-save sequence settle
				// before reading the file.
				select {
				case <-done:
					return
				case <-time.After(reloadDebounce):
				}
				switch filepath.Base(strings.TrimSuffix(name, "~")) {
				case filepath.Base(xDnsPath), filepath.Base(sDnsPath):
					log.Println("DNS file changed, reloading...")
				default:
					log.Println("config file changed, reloading...")
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
				log.Println("reload config success")
			}
		}
	}()

	err = watcher.Add(filePath)
	if err != nil {
		return fmt.Errorf("watch file error: %s", err)
	}
	if xDnsPath != "" {
		err = watcher.Add(xDnsPath)
		if err != nil {
			return fmt.Errorf("watch dns file error: %s", err)
		}
	}
	if sDnsPath != "" {
		err = watcher.Add(sDnsPath)
		if err != nil {
			return fmt.Errorf("watch dns file error: %s", err)
		}
	}
	return nil
}

func (p *Conf) StopWatch() {
	p.watcherMu.Lock()
	defer p.watcherMu.Unlock()
	if p.watcherDone != nil {
		close(p.watcherDone)
		p.watcherDone = nil
	}
}
