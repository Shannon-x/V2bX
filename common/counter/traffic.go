package counter

import (
	"sync"
	"sync/atomic"
)

type TrafficCounter struct {
	Counters sync.Map

	// W6 review #5: counts report periods so MaybePruneIdle can run a full
	// orphan sweep only every Nth period instead of every period.
	pruneTick atomic.Int64
}

type TrafficStorage struct {
	UpCounter   atomic.Int64
	DownCounter atomic.Int64
	dirty       atomic.Bool
}

func NewTrafficCounter() *TrafficCounter {
	return &TrafficCounter{}
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

// MarkDirty publishes traffic after its byte counters have been updated.
// Connections retain their storage pointer, so marking needs no map lookup,
// allocation, or shared map write. A removed connection cannot dirty a new
// user's storage if the same UUID is subsequently added again.
func (s *TrafficStorage) MarkDirty() {
	s.dirty.Store(true)
}

// MarkDirty supports callers that only have a UUID. It never recreates a
// deleted counter; callers retaining a TrafficStorage should mark it directly.
func (c *TrafficCounter) MarkDirty(uuid string) {
	if v, ok := c.Counters.Load(uuid); ok {
		v.(*TrafficStorage).MarkDirty()
	}
}

// IterateDirty scans the user table at reporting frequency and calls fn only
// for dirty entries. This trades a low-frequency O(users) scan for removing
// per-data-block sync.Map writes and allocations from the transfer path.
//
// Clear BEFORE fn drains the counters: a concurrent writer either contributes
// to this drain or leaves the entry dirty for the next one. Never clear after
// fn, which could erase a fresh mark. As before, callers serialize reporting
// drains. Returning false preserves the marks of entries not yet visited.
func (c *TrafficCounter) IterateDirty(clear bool, fn func(uuid string, cts *TrafficStorage) bool) {
	c.Counters.Range(func(k, v interface{}) bool {
		uuid, ok := k.(string)
		if !ok {
			return true
		}
		ts := v.(*TrafficStorage)
		dirty := ts.dirty.Load()
		if clear && dirty {
			dirty = ts.dirty.Swap(false)
		}
		if !dirty {
			return true
		}
		return fn(uuid, ts)
	})
}

// PruneIdle does a full Counters.Range and deletes every entry for which
// keep(uuid) returns false AND the entry currently holds zero bytes. The
// zero-byte guard means an entry that is mid-accounting (has pending traffic
// not yet reported) is never dropped — only genuinely idle orphans go.
//
// IterateDirty only invokes callbacks for dirty users. Periodic pruning also
// checks removed users that have become idle. CompareAndDelete ensures that
// a concurrent delete/re-add cannot make this sweep remove the replacement.
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
			c.Counters.CompareAndDelete(uuid, ts)
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
	cts.MarkDirty()
}

func (c *TrafficCounter) Tx(uuid string, n int) {
	cts := c.GetCounter(uuid)
	cts.UpCounter.Add(int64(n))
	cts.MarkDirty()
}
