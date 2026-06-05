package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Task struct {
	Name     string
	Interval time.Duration
	// Execute is the legacy callback (no ctx). Use ExecuteCtx for new code so
	// the watchdog timeout actually cancels the in-flight work (in particular
	// the resty HTTP call) instead of merely leaking a goroutine holding the
	// stalled response body. W3.2 / audit #25 #44.
	Execute    func() error
	ExecuteCtx func(ctx context.Context) error
	Reload     func()
	access     sync.Mutex

	running bool
	stop    chan struct{}
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

	stopCh := t.stop // Capture local channel to prevent struct field overwrite issues
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
			case <-stopCh:
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

// executeWithTimeout wraps Execute / ExecuteCtx with a timeout to prevent
// goroutine leaks when API calls hang. When ExecuteCtx is set, the timeout
// ctx is propagated to the callback so resty (or any other I/O) can abort
// the in-flight request — matching v2node's pattern and what audit #25 / #44
// recommended.
func (t *Task) executeWithTimeout() error {
	timeout := 3 * t.Interval
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// W6 review #15: the outer scheduler goroutine's recover does NOT
		// cover this inner goroutine. Without this defer, a panic in
		// Execute/ExecuteCtx (e.g. an unexpected type assertion deep in the
		// report path) would crash the whole process instead of failing just
		// this cycle. Convert the panic into an error on the done channel.
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("Task %s panicked: %v", t.Name, r)
				select {
				case done <- fmt.Errorf("task %s panic: %v", t.Name, r):
				default:
				}
			}
		}()
		if t.ExecuteCtx != nil {
			done <- t.ExecuteCtx(ctx)
			return
		}
		done <- t.Execute()
	}()

	select {
	case <-ctx.Done():
		log.Errorf("Task %s execution timed out, skipping this cycle and triggering reload", t.Name)
		if t.Reload != nil {
			go t.Reload()
		}
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
