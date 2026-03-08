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
		defer func() {
			if r := recover(); r != nil {
				t.access.Lock()
				t.running = false
				t.access.Unlock()
			}
		}()

		if first {
			if err := t.Execute(); err != nil {
				t.access.Lock()
				t.running = false
				close(t.stop)
				t.access.Unlock()
				return
			}
		}

		timer := time.NewTimer(t.Interval)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
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

			timer.Reset(t.Interval)
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
