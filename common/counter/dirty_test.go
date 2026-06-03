package counter

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestIterateDirtyClearsAndCollects validates the W6 / B3 dirty-set
// semantics. Without it the counter would Range the full Counters map
// every period; with it only users that actually had traffic this period
// are visited.
func TestIterateDirtyClearsAndCollects(t *testing.T) {
	c := NewTrafficCounter()
	// Touch three users via Rx/Tx.
	c.Rx("alice", 100)
	c.Tx("bob", 50)
	c.Rx("charlie", 25)

	// First IterateDirty(clear=true) should visit exactly three.
	seen := map[string]int64{}
	c.IterateDirty(true, func(uuid string, ts *TrafficStorage) bool {
		seen[uuid] = ts.DownCounter.Load() + ts.UpCounter.Load()
		return true
	})
	if got, want := len(seen), 3; got != want {
		t.Fatalf("first IterateDirty visit count = %d, want %d (seen=%v)", got, want, seen)
	}
	if seen["alice"] != 100 || seen["bob"] != 50 || seen["charlie"] != 25 {
		t.Fatalf("traffic mismatch: %v", seen)
	}

	// Second IterateDirty(clear=false) should see nothing — dirty was cleared.
	count := 0
	c.IterateDirty(false, func(uuid string, ts *TrafficStorage) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("post-clear IterateDirty visit count = %d, want 0", count)
	}

	// New traffic only for bob.
	c.Tx("bob", 7)
	count = 0
	var seenBob bool
	c.IterateDirty(true, func(uuid string, ts *TrafficStorage) bool {
		count++
		if uuid == "bob" {
			seenBob = true
		}
		return true
	})
	if count != 1 || !seenBob {
		t.Fatalf("expected exactly bob on second period, got count=%d seenBob=%v", count, seenBob)
	}
}

// TestMarkDirtyExternal validates the W6 / B3 path used by xray dispatcher
// (SizeStatWriter / CounterReader) which bumps the atomic counters
// directly and only calls MarkDirty.
func TestMarkDirtyExternal(t *testing.T) {
	c := NewTrafficCounter()
	ts := c.GetCounter("ext-user")
	ts.UpCounter.Add(1234)
	c.MarkDirty("ext-user")

	var hit int
	c.IterateDirty(true, func(uuid string, ts *TrafficStorage) bool {
		if uuid == "ext-user" && ts.UpCounter.Load() == 1234 {
			hit++
		}
		return true
	})
	if hit != 1 {
		t.Fatalf("expected to see ext-user once, got %d", hit)
	}
}

// TestIterateDirtyConcurrent stresses the dirty-set semantics under
// concurrent Tx/Rx + IterateDirty(clear=true) — must not lose traffic
// or panic.
func TestIterateDirtyConcurrent(t *testing.T) {
	c := NewTrafficCounter()
	const (
		workers = 8
		iters   = 1000
	)
	var wg sync.WaitGroup
	var sumWritten atomic.Int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				uuid := "u" + string(rune('A'+(w+i)%8))
				c.Rx(uuid, 1)
				sumWritten.Add(1)
			}
		}(w)
	}
	var sumRead atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
				sumRead.Add(ts.DownCounter.Swap(0))
				return true
			})
		}
	}()
	wg.Wait()
	close(stop)
	// Final drain — anything still dirty from the last write batch.
	c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
		sumRead.Add(ts.DownCounter.Swap(0))
		return true
	})
	if sumRead.Load() != sumWritten.Load() {
		t.Fatalf("traffic loss: written=%d read=%d", sumWritten.Load(), sumRead.Load())
	}
}
