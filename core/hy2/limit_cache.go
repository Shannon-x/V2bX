package hy2

import (
	"time"

	"github.com/InazumaV/V2bX/limiter"
)

// checkLimitCached is the W6.1 / W3.6 后半 / audit #3 short-window cache
// for limiter.CheckLimit. hy2's three EventLogger callbacks
// (Connect/TCPRequest/UDPRequest) are invoked once each per stream/session
// in the hy2 worker goroutines — under sustained load that's three
// sync.Map walks + three device-limit calculations per flow, almost all
// returning the same answer. We elide the back-to-back ones via a 2s
// cache on UserLimitInfo (see limiter.UserLimitInfo.CachedCheck).
//
// Side effects (in addition to returning rejected):
//   - On cache miss, calls Limiter.CheckLimit, stores the result back into
//     UserLimitInfo, and updates UserLimitInfo.OverLimit (so the hy2
//     LogTraffic CompareAndSwap-reader path sees the change).
//   - On cache hit, only refreshes UserLimitInfo.OverLimit to match the
//     cached decision — this keeps the OverLimit signal current without
//     re-running the heavy CheckLimit.
//
// The 2s window is short enough that legitimate device-limit changes are
// detected within one keepalive cycle, long enough to elide repeated work
// across hy2's per-flow callbacks.
func checkLimitCached(l *limiter.Limiter, taguuid, ip string, isTcp, noSSUDP bool) bool {
	if l == nil {
		return false
	}
	v, ok := l.UserLimitInfo.Load(taguuid)
	if !ok {
		// No user record — fall back to a direct call so we don't silently
		// admit unknown users.
		_, rejected := l.CheckLimit(taguuid, ip, isTcp, noSSUDP)
		return rejected
	}
	ui := v.(*limiter.UserLimitInfo)
	now := time.Now()
	if rejected, hit := ui.CachedCheck(now); hit {
		ui.OverLimit.Store(rejected)
		return rejected
	}
	_, rejected := l.CheckLimit(taguuid, ip, isTcp, noSSUDP)
	ui.StoreCheckResult(now, rejected)
	ui.OverLimit.Store(rejected)
	return rejected
}
