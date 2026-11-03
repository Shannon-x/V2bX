package counter

import (
	"strconv"
	"sync"
	"testing"
)

func TestAccountingHotPathAllocations(t *testing.T) {
	c := NewTrafficCounter()
	ts := c.GetCounter("user")
	xray := &XrayTrafficCounter{V: &ts.DownCounter, Storage: ts}
	for name, update := range map[string]func(){
		"uuid mark":    func() { c.MarkDirty("user") },
		"storage mark": ts.MarkDirty,
		"rx":           func() { c.Rx("user", 1024) },
		"xray":         func() { xray.Add(1024) },
	} {
		t.Run(name, func(t *testing.T) {
			if got := testing.AllocsPerRun(1000, update); got != 0 {
				t.Fatalf("steady-state allocations = %g, want 0", got)
			}
		})
	}
}

func TestDirtyDrainConcurrentUpdateAndBackfill(t *testing.T) {
	for _, updateBeforeDrain := range []bool{false, true} {
		t.Run(strconv.FormatBool(updateBeforeDrain), func(t *testing.T) {
			c := NewTrafficCounter()
			c.Rx("user", 10)
			var total int64
			c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
				if updateBeforeDrain {
					c.Rx("user", 5)
				}
				total += ts.DownCounter.Swap(0)
				if !updateBeforeDrain {
					c.Rx("user", 5)
				}
				return true
			})
			c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
				total += ts.DownCounter.Swap(0)
				return true
			})
			if total != 15 {
				t.Fatalf("drained %d bytes, want 15", total)
			}
			// A failed push or below-threshold drain restores bytes without any
			// new network activity. Its mark must survive to the next report.
			ts := c.GetCounter("user")
			ts.DownCounter.Add(total)
			c.MarkDirty("user")
			var restored int64
			c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
				restored += ts.DownCounter.Swap(0)
				return true
			})
			if restored != total {
				t.Fatalf("backfill = %d, want %d", restored, total)
			}
		})
	}
}

func TestDeletedStorageCannotMarkReplacement(t *testing.T) {
	c := NewTrafficCounter()
	old := c.GetCounter("user")
	c.Delete("user")
	c.MarkDirty("user")
	if c.Len() != 0 {
		t.Fatal("mark recreated a deleted counter")
	}
	replacement := c.GetCounter("user")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			old.DownCounter.Add(1)
			old.MarkDirty()
		}
	}()
	for range 1000 {
		c.Rx("user", 1)
	}
	wg.Wait()
	var got int64
	c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
		if ts != replacement {
			t.Error("report returned deleted storage")
		}
		got += ts.DownCounter.Swap(0)
		return true
	})
	if got != 1000 {
		t.Fatalf("replacement traffic = %d, want 1000", got)
	}
	old.MarkDirty()
	c.IterateDirty(false, func(string, *TrafficStorage) bool {
		t.Error("stale connection dirtied the replacement")
		return true
	})
}

func BenchmarkTrafficReport10000Users(b *testing.B) {
	for _, active := range []int{100, 10000} {
		b.Run(strconv.Itoa(active)+"_active", func(b *testing.B) {
			c := NewTrafficCounter()
			entries := make([]*TrafficStorage, 10000)
			for i := range entries {
				entries[i] = c.GetCounter(strconv.Itoa(i))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for _, ts := range entries[:active] {
					ts.DownCounter.Add(1024)
					ts.MarkDirty()
				}
				c.IterateDirty(true, func(_ string, ts *TrafficStorage) bool {
					ts.DownCounter.Swap(0)
					return true
				})
			}
		})
	}
}
