package runtimetune

import (
	"math"
	"runtime"
	"runtime/metrics"
	"time"
)

// Stats is a point-in-time summary of the Go runtime, meant to answer one
// operational question: is this node running out of memory or CPU?
type Stats struct {
	Time time.Time
	// Total is all memory mapped by the Go runtime (heap, stacks, GC
	// metadata). It tracks the process RSS closely and is the number to
	// compare against MemoryLimit.
	Total int64
	// HeapLive is the live (reachable) heap after the last GC — the memory
	// the connections actually need, as opposed to garbage headroom.
	HeapLive int64
	// MemoryLimit is the active soft limit, or 0 when none is set.
	MemoryLimit int64
	Goroutines  int64
	GCCycles    uint64
	// GCCPUShare is the fraction of available CPU (wall time × GOMAXPROCS)
	// the collector used since the previous sample, excluding idle-priority
	// mark workers (0 on the first sample). Sustained values above ~0.15
	// mean the heap is being squeezed against the memory limit.
	GCCPUShare float64
	// MemLimited is true when the runtime's GC CPU limiter engaged since the
	// previous sample, i.e. the soft memory limit is actively binding and
	// the node is memory-bound.
	MemLimited bool
	// Load1 is the 1-minute load average (0 when unavailable).
	Load1  float64
	NumCPU int
}

// Sampler produces successive Stats; GC CPU share and MemLimited are
// computed as deltas between consecutive Sample calls.
type Sampler struct {
	prevGCCPU    float64
	prevTotalCPU float64
	prevCycles   uint64
	primed       bool
}

var sampleNames = []string{
	"/memory/classes/total:bytes",
	"/gc/heap/live:bytes",
	"/gc/gomemlimit:bytes",
	"/sched/goroutines:goroutines",
	"/gc/cycles/total:gc-cycles",
	"/cpu/classes/gc/total:cpu-seconds",
	"/cpu/classes/total:cpu-seconds",
	"/gc/limiter/last-enabled:gc-cycle",
	// Idle mark workers soak up otherwise-idle CPU and would make the GC
	// share look alarming on a quiet machine; they are excluded.
	"/cpu/classes/gc/mark/idle:cpu-seconds",
}

// Sample reads the runtime metrics and returns the current Stats.
func (s *Sampler) Sample() Stats {
	samples := make([]metrics.Sample, len(sampleNames))
	for i, n := range sampleNames {
		samples[i].Name = n
	}
	metrics.Read(samples)
	get := func(i int) (uint64, float64) {
		switch samples[i].Value.Kind() {
		case metrics.KindUint64:
			v := samples[i].Value.Uint64()
			return v, float64(v)
		case metrics.KindFloat64:
			v := samples[i].Value.Float64()
			return uint64(v), v
		}
		return 0, 0
	}
	total, _ := get(0)
	heapLive, _ := get(1)
	limit, _ := get(2)
	goroutines, _ := get(3)
	cycles, _ := get(4)
	_, gcCPU := get(5)
	_, totalCPU := get(6)
	limiterCycle, _ := get(7)
	_, gcIdle := get(8)
	gcCPU -= gcIdle

	st := Stats{
		Time:       time.Now(),
		Total:      int64(total),
		HeapLive:   int64(heapLive),
		Goroutines: int64(goroutines),
		GCCycles:   cycles,
		Load1:      loadAverage1(),
		NumCPU:     runtime.GOMAXPROCS(0),
	}
	if limit > 0 && limit < math.MaxInt64 {
		st.MemoryLimit = int64(limit)
	}
	if s.primed {
		if dt := totalCPU - s.prevTotalCPU; dt > 0 {
			st.GCCPUShare = (gcCPU - s.prevGCCPU) / dt
			if st.GCCPUShare < 0 {
				st.GCCPUShare = 0
			}
		}
		st.MemLimited = limiterCycle > s.prevCycles && limiterCycle <= cycles
	}
	s.prevGCCPU, s.prevTotalCPU, s.prevCycles, s.primed = gcCPU, totalCPU, cycles, true
	return st
}
