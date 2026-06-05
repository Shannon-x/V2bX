package hy2

import (
	"time"

	"github.com/InazumaV/V2bX/limiter"
)

// checkLimitCached is the W6.1 / W3.6 后半 / audit #3 short-window cache
// for limiter.CheckLimit. hy2's three EventLogger callbacks
// (Connect/TCPRequest/UDPRequest) are invoked once each per stream/session
// in the hy2 worker goroutines — under sustained load that's three
// sync.Map walks + three speed-limit calculations per flow, almost all
// returning the same answer. We elide the back-to-back ones via a 2s
// cache on UserLimitInfo (see limiter.UserLimitInfo.CachedCheck).
//
// W6 review #4 / DEVICE-LIMIT SAFETY: the cache is keyed by user (taguuid)
// and is IP-AGNOSTIC, so it MUST NOT be used when the user has a device
// (IP-count) limit. CheckLimit's device-limit branch is the only place a
// new source IP gets registered into UserOnlineIP and counted toward the
// limit; short-circuiting it for a fresh IP within the 2s window would let
// a single user bring up unlimited devices and would under-report the true
// device count to the panel. Therefore:
//
//   - DeviceLimit > 0  → ALWAYS run the real CheckLimit (no cache). This is
//     correct and the per-flow cost is unavoidable for these users.
//   - DeviceLimit == 0 → the decision depends only on the speed limit and
//     user existence (IP-independent), so the 2s cache is safe and retains
//     the perf win for the common (no-device-limit) case.
//
// The cache also never CLEARS OverLimit on a hit (it only Stores the fresh
// result on a miss), so it can't race-clobber a concurrent real CheckLimit
// that just set OverLimit=true.
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

	// Device-limited users can never use the IP-agnostic cache — every new
	// IP must reach CheckLimit's UserOnlineIP registration.
	if ui.DeviceLimit.Load() > 0 {
		_, rejected := l.CheckLimit(taguuid, ip, isTcp, noSSUDP)
		ui.OverLimit.Store(rejected)
		return rejected
	}

	now := time.Now()
	if rejected, hit := ui.CachedCheck(now); hit {
		// Cache hit: return the cached speed-limit decision WITHOUT touching
		// OverLimit (avoid clobbering a concurrent real CheckLimit result).
		return rejected
	}
	_, rejected := l.CheckLimit(taguuid, ip, isTcp, noSSUDP)
	ui.StoreCheckResult(now, rejected)
	ui.OverLimit.Store(rejected)
	return rejected
}
