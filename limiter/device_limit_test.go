package limiter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
)

func newDeviceTestLimiter(limit int, alive int) (*Limiter, string) {
	const (
		tag  = "device-test"
		uuid = "test-user"
		uid  = 42
	)
	aliveList := map[int]int{}
	if alive > 0 {
		aliveList[uid] = alive
	}
	l := AddLimiter("vless", tag, &conf.LimitConfig{}, []panel.UserInfo{{
		Id: uid, Uuid: uuid, DeviceLimit: limit,
	}}, aliveList)
	return l, format.UserTag(tag, uuid)
}

// Device limiting keys off the panel's global alive-IP count only (matching
// the reference v2node). While that count is below the limit, this node must
// NOT reject new local IPs. Counting local not-yet-reported IPs on top of the
// panel count over-rejects under Proxy Protocol / mobile IP churn, which is
// what made nodes intermittently time out once Proxy Protocol was enabled.
func TestLocalIPsAdmittedWhilePanelCountBelowLimit(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(3, 0)
	// Many distinct local IPs (as Proxy Protocol would expose) — all admitted
	// because the panel's alive count is still 0.
	for i := 1; i <= 10; i++ {
		if _, rejected := l.CheckLimit(taguuid, fmt.Sprintf("10.0.0.%d", i), true, true); rejected {
			t.Fatalf("local IP %d rejected while the panel alive count is below the limit (over-rejection)", i)
		}
	}
}

// Once the panel's global alive count has reached the limit, a new (unreported)
// IP is rejected.
func TestNewIPRejectedWhenPanelCountAtLimit(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(2, 2)
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.9", true, true); !rejected {
		t.Fatal("new IP should be rejected when the panel alive count is at the limit")
	}
}

func TestDeviceLimitRecoversWhenAliveDropsBelowLimit(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(2, 2)
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.3", true, true); !rejected {
		t.Fatal("new IP should be rejected while the global count is at the limit")
	}
	l.MergeAliveCounts(map[int]int{42: 1})
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.3", true, true); rejected {
		t.Fatal("IP remained rejected after the panel count dropped below the limit")
	}
}

// An IP already in the previous report window (already counted in AliveList)
// stays usable even at the limit; a different unreported IP is rejected.
func TestPreviouslyReportedIPStaysUsableAtLimit(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(2, 2)
	l.OldUserOnline.Store("10.0.0.1", 42)

	if _, rejected := l.CheckLimit(taguuid, "10.0.0.1", true, true); rejected {
		t.Fatal("previously reported IP must remain usable while the count is at the limit")
	}
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.2", true, true); !rejected {
		t.Fatal("a different unreported IP must be rejected while the global count is at the limit")
	}
}

// Regression for the Proxy Protocol instability: a burst of distinct local IPs
// (the real client IPs that PROXY protocol exposes) must NOT be capped locally
// to the device limit. The panel's periodic count is the single source of
// truth, so a local burst below that count is fully admitted — no intermittent
// timeouts from local over-counting.
func TestConcurrentLocalBurstNotLocallyCapped(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(5, 0)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, rejected := l.CheckLimit(taguuid, fmt.Sprintf("10.1.0.%d", i), true, true); !rejected {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if got := allowed.Load(); got != 100 {
		t.Fatalf("local burst admitted %d/100 IPs; local counting must not cap below the panel count", got)
	}
}
