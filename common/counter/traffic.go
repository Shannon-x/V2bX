package counter

import (
	"sync"
	"sync/atomic"
)

type TrafficCounter struct {
	Counters sync.Map
	// W6 / B3: tracks which uuids received traffic since the last
	// IterateDirty(true) call. Replaces the O(N) Counters.Range every
	// upload period with O(active-users-this-period). At 10k users where
	// only ~100 are active per period this is a 100× speedup on the
	// report path (relevant for sing/xray GetUserTrafficSlice).
	//
	// Pointer is swapped atomically by IterateDirty so we don't hold a
	// lock during traffic accounting on the hot path. The pointer-swap
	// ensures we never miss a fresh Rx/Tx that races with the swap —
	// worst case the dirty mark lands in the new map and is collected
	// next period.
	dirty atomic.Pointer[sync.Map]

	// W6 review #5: counts report periods so MaybePruneIdle can run a full
	// orphan sweep only every Nth period instead of every period.
	pruneTick atomic.Int64
}

type TrafficStorage struct {
	UpCounter   atomic.Int64
	DownCounter atomic.Int64
}

func NewTrafficCounter() *TrafficCounter {
	tc := &TrafficCounter{}
	tc.dirty.Store(&sync.Map{})
	return tc
}

func (c *TrafficCounter) GetCounter(uuid string) *TrafficStorage {
	if cts, ok := c.Counters.Load(uuid); ok {
		return cts.(*TrafficStorage)
	}
	newStorage := &TrafficStorage{}
	if cts, loaded := c.Counters.LoadOrStore(uuid, newStorage); loaded {
		return cts.(*TrafficStorage)
	}
	return newStorage
}

// markDirty records that this uuid received some traffic. Called from Rx/Tx
// on the data hot path — must be cheap. sync.Map.Store on an already-present
// key is a single atomic.Pointer load+compare, so amortised this is O(1).
//
// Concurrency contract — what the retry loop defends against:
//
// The naive "d := dirty.Load(); d.Store(uuid, _)" sequence is NOT a single
// atomic op. If an IterateDirty(true) call slips between the Load and the
// Store, our Store lands in a map that the iterator has already swapped
// OUT and is now Range-ing as a snapshot. sync.Map.Range explicitly
// documents that concurrent Stores during a Range may NOT be reflected —
// so a worst-case interleaving leaves our mark in a discarded snapshot
// while the live dirty (the one the iterator just installed) has no
// record of our uuid. The traffic byte we just Added to ts.Down then has
// no path to drain — silently lost, ~0.05% miss rate under contention.
//
// Fix: after Store, verify the dirty pointer hasn't changed. If it has,
// the iterator may have already missed our store; re-Store in the new
// live dirty. The loop runs at most ~"number of concurrent iterators"
// times, which is 1 in practice (the report task).
func (c *TrafficCounter) markDirty(uuid string) {
	for {
		d := c.dirty.Load()
		if d == nil {
			return
		}
		d.Store(uuid, struct{}{})
		if c.dirty.Load() == d {
			return
		}
		// dirty pointer changed between our Load and our Store — an
		// IterateDirty(true) swapped while we were mid-mark. Loop to mark
		// in the new live dirty so the next iteration sees us.
	}
}

// MarkDirty is the exported equivalent of markDirty for callers that
// account traffic via direct atomic.Int64.Add on a TrafficStorage rather
// than going through Tx/Rx (notably the xray dispatcher's SizeStatWriter
// and CounterReader wrappers).
func (c *TrafficCounter) MarkDirty(uuid string) {
	c.markDirty(uuid)
}

// IterateDirty calls fn for each (uuid, cts) that received Rx/Tx since the
// previous IterateDirty call. If clear is true, the dirty set is swapped
// for a fresh empty one BEFORE iteration — so traffic that arrives during
// iteration is tracked for the NEXT period rather than missed. Returning
// false from fn aborts the iteration (mirrors sync.Map.Range semantics).
//
// W6 / B3: replaces TrafficCounter.Counters.Range on the upload path.
// Idle users no longer cost us a Range visit per period.
func (c *TrafficCounter) IterateDirty(clear bool, fn func(uuid string, cts *TrafficStorage) bool) {
	var d *sync.Map
	if clear {
		d = c.dirty.Swap(&sync.Map{})
	} else {
		d = c.dirty.Load()
	}
	if d == nil {
		return
	}
	d.Range(func(k, _ interface{}) bool {
		uuid, ok := k.(string)
		if !ok {
			return true
		}
		v, exists := c.Counters.Load(uuid)
		if !exists {
			return true
		}
		return fn(uuid, v.(*TrafficStorage))
	})
}

// PruneIdle does a full Counters.Range and deletes every entry for which
// keep(uuid) returns false AND the entry currently holds zero bytes. The
// zero-byte guard means an entry that is mid-accounting (has pending traffic
// not yet reported) is never dropped — only genuinely idle orphans go.
//
// W6 review #5: the dirty-set fast path (IterateDirty) only ever visits
// users who sent traffic THIS period, so a "was active → removed from
// uidMap → went idle" user's TrafficStorage would otherwise live forever
// (the per-period cleanup branches only run for dirty users). Callers run
// this occasionally (every N report periods) to restore the full-Range GC
// guarantee that the pre-dirty-set code had, without paying it every period.
func (c *TrafficCounter) PruneIdle(keep func(uuid string) bool) {
	c.Counters.Range(func(k, v interface{}) bool {
		uuid, ok := k.(string)
		if !ok {
			return true
		}
		if keep(uuid) {
			return true
		}
		ts := v.(*TrafficStorage)
		if ts.UpCounter.Load() == 0 && ts.DownCounter.Load() == 0 {
			c.Counters.Delete(uuid)
		}
		return true
	})
}

// PruneEveryN is how many report periods elapse between full orphan sweeps.
// At a typical 60s PushInterval this is one sweep per hour — cheap enough to
// not matter, frequent enough to bound orphan accumulation.
const PruneEveryN = 60

// MaybePruneIdle runs PruneIdle once every PruneEveryN calls. Intended to be
// called once per report period (reset=true) from GetUserTrafficSlice.
// W6 review #5.
func (c *TrafficCounter) MaybePruneIdle(keep func(uuid string) bool) {
	if c.pruneTick.Add(1)%PruneEveryN != 0 {
		return
	}
	c.PruneIdle(keep)
}

func (c *TrafficCounter) GetUpCount(uuid string) int64 {
	if cts, ok := c.Counters.Load(uuid); ok {
		return cts.(*TrafficStorage).UpCounter.Load()
	}
	return 0
}

func (c *TrafficCounter) GetDownCount(uuid string) int64 {
	if cts, ok := c.Counters.Load(uuid); ok {
		return cts.(*TrafficStorage).DownCounter.Load()
	}
	return 0
}

func (c *TrafficCounter) Len() int {
	length := 0
	c.Counters.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	return length
}

func (c *TrafficCounter) Reset(uuid string) {
	if cts, ok := c.Counters.Load(uuid); ok {
		cts.(*TrafficStorage).UpCounter.Store(0)
		cts.(*TrafficStorage).DownCounter.Store(0)
	}
}

func (c *TrafficCounter) Delete(uuid string) {
	c.Counters.Delete(uuid)
}

func (c *TrafficCounter) Rx(uuid string, n int) {
	cts := c.GetCounter(uuid)
	cts.DownCounter.Add(int64(n))
	c.markDirty(uuid) // W6 / B3
}

func (c *TrafficCounter) Tx(uuid string, n int) {
	cts := c.GetCounter(uuid)
	cts.UpCounter.Add(int64(n))
	c.markDirty(uuid) // W6 / B3
}
