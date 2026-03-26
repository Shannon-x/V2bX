package task

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
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
				log.Errorf("Task panic recovered: %v", r)
				t.access.Lock()
				t.running = false
				t.access.Unlock()
			}
		}()

		if first {
			if err := t.executeWithTimeout(); err != nil {
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

			if err := t.executeWithTimeout(); err != nil {
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

// executeWithTimeout wraps Execute with a timeout to prevent goroutine leaks
// when API calls hang. Matches v2node's ExecuteWithTimeout pattern.
func (t *Task) executeWithTimeout() error {
	timeout := 3 * t.Interval
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- t.Execute()
	}()

	select {
	case <-ctx.Done():
		log.Error("Task execution timed out, skipping this cycle")
		return nil // don't return error — just skip this cycle
	case err := <-done:
		return err
	}
}

func (t *Task) Close() {
	t.access.Lock()
	if t.running {
		t.running = false
		close(t.stop)
	}
	t.access.Unlock()
}
