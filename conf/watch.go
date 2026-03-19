package conf

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

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

	go func() {
		var pre time.Time
		defer watcher.Close()
		for {
			select {
			case <-done:
				return
			case e := <-watcher.Events:
				if e.Has(fsnotify.Chmod) {
					continue
				}
				if pre.Add(10 * time.Second).After(time.Now()) {
					continue
				}
				pre = time.Now()
				go func() {
					select {
					case <-done:
						return
					case <-time.After(5 * time.Second):
					}
					switch filepath.Base(strings.TrimSuffix(e.Name, "~")) {
					case filepath.Base(xDnsPath), filepath.Base(sDnsPath):
						log.Println("DNS file changed, reloading...")
					default:
						log.Println("config file changed, reloading...")
					}
					newConf := New()
					err := newConf.LoadFromPath(filePath)
					if err != nil {
						log.Printf("reload config error: %s", err)
						return
					}
					p.mu.Lock()
					p.LogConfig = newConf.LogConfig
					p.CoresConfig = newConf.CoresConfig
					p.NodeConfig = newConf.NodeConfig
					p.mu.Unlock()
					reload()
					log.Println("reload config success")
				}()
			case err := <-watcher.Errors:
				if err != nil {
					log.Printf("File watcher error: %s", err)
				}
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
