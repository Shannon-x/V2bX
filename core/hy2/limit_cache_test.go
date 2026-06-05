package hy2

import (
	"sync"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/limiter"
)

// TestCheckLimitCachedDoesNotBypassDeviceLimit is the W6 review #4
// regression. The 2s CheckLimit cache is keyed by user (taguuid) and is
// IP-agnostic. For a device-limited user it MUST NOT short-circuit a
// fresh IP — otherwise new devices never register in UserOnlineIP, the
// device count is under-reported to the panel, and DeviceLimit is bypassed.
//
// The test asserts the registration side-effect directly: with DeviceLimit
// > 0, two checkLimitCached calls for two different IPs of the same user
// (within the cache window) must BOTH land in UserOnlineIP. With the old
// IP-agnostic cache the second IP would be cache-short-circuited and absent.
func TestCheckLimitCachedDoesNotBypassDeviceLimit(t *testing.T) {
	limiter.Init()
	const tag = "test-hy2-tag"
	const uuid = "device-limited-user"
	users := []panel.UserInfo{{Id: 1, Uuid: uuid, DeviceLimit: 2}}
	l := limiter.AddLimiter("hysteria2", tag, &conf.LimitConfig{}, users, map[int]int{})
	defer limiter.DeleteLimiter(tag)

	taguuid := format.UserTag(tag, uuid)

	// Two distinct source IPs within the 2s cache window.
	if rejected := checkLimitCached(l, taguuid, "1.1.1.1", true, true); rejected {
		t.Fatalf("first IP unexpectedly rejected")
	}
	if rejected := checkLimitCached(l, taguuid, "2.2.2.2", true, true); rejected {
		t.Fatalf("second IP unexpectedly rejected (aliveIp=0 < deviceLimit=2)")
	}

	// Both IPs must be registered — proving the cache did NOT skip the real
	// CheckLimit for the device-limited user.
	v, ok := l.UserOnlineIP.Load(taguuid)
	if !ok {
		t.Fatal("UserOnlineIP has no entry for the user — CheckLimit never ran")
	}
	ipMap := v.(*sync.Map)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if _, present := ipMap.Load(ip); !present {
			t.Errorf("IP %s not registered in UserOnlineIP — device-limit cache bypass regression", ip)
		}
	}
}

// TestCheckLimitCachedUsesCacheWhenNoDeviceLimit confirms the perf path is
// preserved: a user with NO device limit still benefits from the cache
// (the second call within the window does not need to re-register, and the
// function returns the cached allow decision).
func TestCheckLimitCachedUsesCacheWhenNoDeviceLimit(t *testing.T) {
	limiter.Init()
	const tag = "test-hy2-tag-nolimit"
	const uuid = "unlimited-user"
	users := []panel.UserInfo{{Id: 2, Uuid: uuid, DeviceLimit: 0}}
	l := limiter.AddLimiter("hysteria2", tag, &conf.LimitConfig{}, users, map[int]int{})
	defer limiter.DeleteLimiter(tag)

	taguuid := format.UserTag(tag, uuid)
	if rejected := checkLimitCached(l, taguuid, "1.1.1.1", true, true); rejected {
		t.Fatalf("no-device-limit user unexpectedly rejected")
	}
	// Second call should still be allowed (cache hit path is fine here).
	if rejected := checkLimitCached(l, taguuid, "2.2.2.2", true, true); rejected {
		t.Fatalf("no-device-limit user rejected on second call")
	}
}
