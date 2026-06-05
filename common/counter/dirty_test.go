package counter

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// TestPruneIdleReclaimsOrphans is the W6 review #5 regression: an entry
// that left uidMap (keep returns false) and holds zero bytes must be
// reclaimed by PruneIdle, while an entry that is kept OR still holds bytes
// must survive.
func TestPruneIdleReclaimsOrphans(t *testing.T) {
	c := NewTrafficCounter()
	// orphan-idle: not kept, zero bytes → should be deleted
	c.GetCounter("orphan-idle")
	// orphan-with-bytes: not kept but has pending bytes → must survive
	bts := c.GetCounter("orphan-bytes")
	bts.UpCounter.Store(123)
	// active: kept → must survive regardless
	c.GetCounter("active")

	keep := map[string]bool{"active": true}
	c.PruneIdle(func(uuid string) bool { return keep[uuid] })

	if _, ok := c.Counters.Load("orphan-idle"); ok {
		t.Error("orphan-idle should have been pruned")
	}
	if _, ok := c.Counters.Load("orphan-bytes"); !ok {
		t.Error("orphan-bytes has pending traffic — must NOT be pruned")
	}
	if _, ok := c.Counters.Load("active"); !ok {
		t.Error("active user must NOT be pruned")
	}
}

// TestMaybePruneIdleCadence verifies MaybePruneIdle only sweeps every
// PruneEveryN calls.
func TestMaybePruneIdleCadence(t *testing.T) {
	c := NewTrafficCounter()
	c.GetCounter("orphan")
	keepNone := func(string) bool { return false }
	// First PruneEveryN-1 calls must NOT prune.
	for i := 0; i < PruneEveryN-1; i++ {
		c.MaybePruneIdle(keepNone)
	}
	if _, ok := c.Counters.Load("orphan"); !ok {
		t.Fatal("orphan pruned too early — cadence broken")
	}
	// The PruneEveryN-th call triggers the sweep.
	c.MaybePruneIdle(keepNone)
	if _, ok := c.Counters.Load("orphan"); ok {
		t.Fatal("orphan should have been pruned on the Nth call")
	}
}

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

// TestConnCounterMarksDirty pins the W6 post-revert regression: the sing
// HookServer wraps the conn via NewConnCounter, and Read/Write MUST call
// MarkDirty so the per-tag IterateDirty(reset) report path actually sees
// these users. The earlier Wave 6 commit only bumped the atomic counters
// and silently dropped sing traffic from every panel push.
func TestConnCounterMarksDirty(t *testing.T) {
	c := NewTrafficCounter()

	// Use net.Pipe() to drive Read/Write through the real wrapper.
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()

	wrapped := NewConnCounter(srv, c, "sing-user")

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		_, _ = io.ReadFull(wrapped, buf[:5])
	}()
	_, _ = cli.Write([]byte("hello"))
	<-done

	// After exactly one Read, the user MUST appear in dirty.
	var seenUser bool
	c.IterateDirty(true, func(uuid string, ts *TrafficStorage) bool {
		if uuid == "sing-user" && ts.UpCounter.Load() == 5 {
			seenUser = true
		}
		return true
	})
	if !seenUser {
		t.Fatalf("sing-user with 5 bytes UpCounter did NOT appear in IterateDirty — sing traffic would silently drop")
	}

	// And after the dirty clear, a second Read must re-add to dirty.
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		buf := make([]byte, 64)
		_, _ = io.ReadFull(wrapped, buf[:3])
	}()
	_, _ = cli.Write([]byte("hi!"))
	<-done2

	seenUser = false
	c.IterateDirty(true, func(uuid string, ts *TrafficStorage) bool {
		if uuid == "sing-user" {
			seenUser = true
		}
		return true
	})
	if !seenUser {
		t.Fatal("second Read did not re-mark dirty — long-lived sing connections would only report once")
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
//
// Historical context: this test reliably flaked under -count=20 with
// `traffic loss: written=8000 read=799x` because (a) the drainer
// goroutine could overlap with the main goroutine's final IterateDirty,
// putting two IterateDirty(true) in flight at once, and more
// fundamentally (b) the Load-then-Store sequence inside markDirty could
// land in a sync.Map snapshot that the iterator was already Range-ing,
// where sync.Map.Range explicitly permits the Store to be missed.
//
// Fix (a): drainer signals exit via drainerDone; main waits before its
// own final drain so only one IterateDirty(true) is ever in flight.
//
// Fix (b): markDirty now verifies the dirty pointer hasn't changed and
// re-Stores in the new live dirty if it has (see traffic.go).
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
	drainerDone := make(chan struct{})
	go func() {
		defer close(drainerDone)
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
	<-drainerDone // single-IterateDirty invariant for the final drain
	// Final drain — anything still dirty from the last write batch.
	c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
		sumRead.Add(ts.DownCounter.Swap(0))
		return true
	})
	if sumRead.Load() != sumWritten.Load() {
		t.Fatalf("traffic loss: written=%d read=%d", sumWritten.Load(), sumRead.Load())
	}
}
