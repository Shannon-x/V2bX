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
func (c *TrafficCounter) markDirty(uuid string) {
	if d := c.dirty.Load(); d != nil {
		d.Store(uuid, struct{}{})
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
