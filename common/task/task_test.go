package task

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for task")
	}
}

func TestCloseCancelsAndWaits(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ts := &Task{Interval: time.Hour, ExecuteCtx: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}}
	if err := ts.Start(true); err != nil {
		t.Fatal(err)
	}
	await(t, started)
	closed := make(chan struct{})
	go func() { ts.Close(); close(closed) }()
	await(t, canceled)
	select {
	case <-closed:
		t.Fatal("Close returned with callback still running")
	default:
	}
	close(release)
	await(t, closed)
	ts.Close()
}

func TestTimeoutDoesNotOverlapCallbackOrReload(t *testing.T) {
	started, expired, release, reloaded := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ts := &Task{Interval: 10 * time.Millisecond, ExecuteCtx: func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(expired)
			<-release
		}
		return nil
	}, Reload: func() { close(reloaded) }}
	if err := ts.Start(true); err != nil {
		t.Fatal(err)
	}
	await(t, started)
	await(t, expired)
	time.Sleep(60 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("overlapping callbacks: %d", calls.Load())
	}
	select {
	case <-reloaded:
		t.Fatal("reload overlapped callback")
	default:
	}
	close(release)
	await(t, reloaded)
	ts.Close()
}

func TestSetIntervalFromCallback(t *testing.T) {
	second := make(chan struct{})
	var calls atomic.Int32
	ts := &Task{Interval: time.Hour}
	ts.ExecuteCtx = func(context.Context) error {
		if calls.Add(1) == 1 {
			ts.SetInterval(time.Millisecond)
		} else if calls.Load() == 2 {
			close(second)
		}
		return nil
	}
	if err := ts.Start(true); err != nil {
		t.Fatal(err)
	}
	defer ts.Close()
	await(t, second)
}

func TestConcurrentLifecycleAndInterval(t *testing.T) {
	ts := &Task{Interval: time.Millisecond, ExecuteCtx: func(context.Context) error { return nil }}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				if err := ts.Start(false); err != nil {
					t.Error(err)
				}
				ts.SetInterval(time.Duration(j+1) * time.Millisecond)
				ts.Close()
			}
		}()
	}
	wg.Wait()
	ts.Close()
}
