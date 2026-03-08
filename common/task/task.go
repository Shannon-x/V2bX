package task

import (
	"sync"
	"time"
)

type Task struct {
	Interval time.Duration
	Execute  func() error
	access   sync.Mutex
	running  bool
	stop     chan struct{}
}

func (t *Task) Start(first bool) error {
	t.access.Lock()
	if t.running {
		t.access.Unlock()
		return nil
	}
	t.running = true
	t.stop = make(chan struct{})
	t.access.Unlock()

	go func() {
		if first {
			if err := t.Execute(); err != nil {
				t.access.Lock()
				t.running = false
				close(t.stop)
				t.access.Unlock()
				return
			}
		}

		for {
			select {
			case <-time.After(t.Interval):
			case <-t.stop:
				return
			}

			if err := t.Execute(); err != nil {
				t.access.Lock()
				t.running = false
				close(t.stop)
				t.access.Unlock()
				return
			}
		}
	}()

	return nil
}

func (t *Task) Close() {
	t.access.Lock()
	if t.running {
		t.running = false
		close(t.stop)
	}
	t.access.Unlock()
}