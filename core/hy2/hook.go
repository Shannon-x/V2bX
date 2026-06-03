package hy2

import (
	"sync"
	"sync/atomic"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/limiter"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

var _ server.TrafficLogger = (*HookServer)(nil)

type HookServer struct {
	Tag    string
	logger *zap.Logger
	// Counter is accessed concurrently from LogTraffic (hy2 worker goroutine)
	// and GetUserTrafficSlice (report task). sync.Map is the existing choice.
	Counter sync.Map
	// W2.1 / audit #22 #41: ReportMinTrafficBytes is read on every LogTraffic
	// callback in GetUserTrafficSlice (report goroutine) and written by
	// Hysteria2.UpdateNodeReportMinTraffic (panel update goroutine). atomic
	// keeps the int64 store/load coherent without a mutex.
	ReportMinTrafficBytes atomic.Int64
}

func (h *HookServer) TraceStream(stream server.HyStream, stats *server.StreamStats) {
}

func (h *HookServer) UntraceStream(stream server.HyStream) {
}

func (h *HookServer) LogTraffic(id string, tx, rx uint64) (ok bool) {
	limiterinfo, err := limiter.GetLimiter(h.Tag)
	if err != nil {
		h.logger.Error("Get limiter error", zap.String("tag", h.Tag), zap.Error(err))
		return false
	}

	if userLimit, found := limiterinfo.UserLimitInfo.Load(format.UserTag(h.Tag, id)); found {
		userlimitInfo := userLimit.(*limiter.UserLimitInfo)
		// W2.7 / audit #27 #48: CompareAndSwap so the flip-and-skip is atomic.
		// Without it, two concurrent LogTraffic calls could both observe true,
		// both flip to false, and both incorrectly drop one packet's worth of
		// accounting (the OverLimit signal is supposed to fire exactly once).
		if userlimitInfo.OverLimit.CompareAndSwap(true, false) {
			return false
		}
	}

	// W2.5 / W6 / audit #22 #42 #56 / B1: Load-first fast path; LoadOrStore
	// (with a pre-allocated NewTrafficCounter) only on cold miss. Steady
	// state allocates nothing — previously every LogTraffic call alloc'd
	// a TrafficCounter that was immediately GC'd when LoadOrStore returned
	// the already-stored value.
	var tc *counter.TrafficCounter
	if v, ok := h.Counter.Load(h.Tag); ok {
		tc, _ = v.(*counter.TrafficCounter)
	} else {
		actual, _ := h.Counter.LoadOrStore(h.Tag, counter.NewTrafficCounter())
		tc, _ = actual.(*counter.TrafficCounter)
	}
	if tc == nil {
		return false
	}
	tc.Rx(id, int(rx))
	tc.Tx(id, int(tx))
	return true
}

func (s *HookServer) LogOnlineState(id string, online bool) {
}
