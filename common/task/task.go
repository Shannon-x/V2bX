package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Task struct {
	Name string
	// Interval is the initial interval. Use SetInterval once the task starts.
	Interval time.Duration
	// Execute is retained for synchronous work that cannot accept a context.
	// Close waits for it to return; cancellation cannot forcibly stop Go code.
	Execute    func() error
	ExecuteCtx func(context.Context) error
	Reload     func()

	access sync.Mutex
	run    *execution
}

type execution struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	wake   chan struct{}
}

func (t *Task) Start(first bool) error {
	t.access.Lock()
	defer t.access.Unlock()
	if t.run != nil {
		return nil
	}
	if t.Interval <= 0 {
		return fmt.Errorf("task %s interval must be positive", t.Name)
	}
	if t.ExecuteCtx == nil && t.Execute == nil {
		return fmt.Errorf("task %s has no callback", t.Name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &execution{ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1)}
	t.run = r
	go t.loop(r, first)
	return nil
}

// SetInterval is safe to call from a running callback. It changes the next
// scheduled run without stopping, replacing, or overlapping the current run.
func (t *Task) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	t.access.Lock()
	defer t.access.Unlock()
	if t.Interval == interval {
		return
	}
	t.Interval = interval
	if t.run != nil {
		select {
		case t.run.wake <- struct{}{}:
		default:
		}
	}
}

func (t *Task) interval() time.Duration {
	t.access.Lock()
	defer t.access.Unlock()
	return t.Interval
}

func (t *Task) loop(r *execution, first bool) {
	defer func() {
		r.cancel()
		t.access.Lock()
		if t.run == r {
			t.run = nil
		}
		close(r.done)
		t.access.Unlock()
	}()
	if first && t.execute(r.ctx) != nil {
		return
	}
	timer := time.NewTimer(t.interval())
	defer timer.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(t.interval())
		case <-timer.C:
			if r.ctx.Err() != nil || t.execute(r.ctx) != nil {
				return
			}
			timer.Reset(t.interval())
		}
	}
}

// execute runs inline: a timed-out callback must finish before another callback
// or reload can start. Context-aware I/O still observes the watchdog deadline.
func (t *Task) execute(parent context.Context) (err error) {
	if parent.Err() != nil {
		return parent.Err()
	}
	timeout := t.interval()
	if timeout >= 5*time.Minute/3 {
		timeout = 5 * time.Minute
	} else {
		timeout *= 3
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("task %s panic: %v", t.Name, r)
			log.Error(err)
		}
	}()
	if t.ExecuteCtx != nil {
		err = t.ExecuteCtx(ctx)
	} else {
		err = t.Execute()
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if ctx.Err() == context.DeadlineExceeded {
		log.Errorf("Task %s execution timed out; callback stopped before reload", t.Name)
		if t.Reload != nil {
			t.Reload()
		}
		return nil
	}
	return err
}

// Stop requests cancellation without waiting, allowing owners to cancel every
// task before waiting for any one task. A callback must not call Close on itself.
func (t *Task) Stop() {
	t.access.Lock()
	defer t.access.Unlock()
	if t.run != nil {
		t.run.cancel()
	}
}

func (t *Task) Wait() {
	t.access.Lock()
	r := t.run
	t.access.Unlock()
	if r != nil {
		<-r.done
	}
}

func (t *Task) Close() {
	t.access.Lock()
	r := t.run
	if r != nil {
		r.cancel()
	}
	t.access.Unlock()
	if r != nil {
		<-r.done
	}
}
