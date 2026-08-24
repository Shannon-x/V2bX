package throttle

import (
	"sync"
	"testing"
	"time"
)

func TestFirstCallAlwaysAllowed(t *testing.T) {
	// 回归用例：早先的实现把「从未放行」的零值当成了「在 Unix 纪元放行过」，
	// interval 一旦大于当前 Unix 纳秒时间戳，第一条日志就会被吞掉。
	for _, interval := range []time.Duration{time.Nanosecond, 30 * time.Second, 1 << 62} {
		g := New(interval)
		if !g.Allow("k") {
			t.Errorf("interval=%v 时首次调用被拒绝", interval)
		}
	}
}

func TestSuppressesWithinWindow(t *testing.T) {
	g := New(time.Hour)
	if !g.Allow("a") {
		t.Fatal("首次调用应放行")
	}
	for i := 0; i < 100; i++ {
		if g.Allow("a") {
			t.Fatal("窗口内不应再次放行")
		}
	}
}

func TestKeysAreIndependent(t *testing.T) {
	g := New(time.Hour)
	if !g.Allow("a") || !g.Allow("b") {
		t.Fatal("不同 key 应各自独立计时")
	}
}

func TestAllowsAgainAfterWindow(t *testing.T) {
	g := New(time.Millisecond)
	if !g.Allow("a") {
		t.Fatal("首次调用应放行")
	}
	time.Sleep(3 * time.Millisecond)
	if !g.Allow("a") {
		t.Fatal("超过窗口后应重新放行")
	}
}

// 并发下同一窗口内只能有一个调用者拿到放行。
func TestConcurrentAllowExactlyOnce(t *testing.T) {
	g := New(time.Hour)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Allow("race") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("同一窗口内应恰好放行 1 次，实际 %d", allowed)
	}
}
