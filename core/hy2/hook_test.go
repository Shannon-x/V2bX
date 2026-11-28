package hy2

import (
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/limiter"
	"go.uber.org/zap"
)

const hookTag = "hook-test"

func newHookTest(t *testing.T, users ...panel.UserInfo) (*HookServer, *limiter.Limiter) {
	t.Helper()
	limiter.Init()
	l := limiter.AddLimiter("hysteria2", hookTag, &conf.LimitConfig{}, users, map[int]int{})
	t.Cleanup(func() { limiter.DeleteLimiter(hookTag) })
	return &HookServer{Tag: hookTag, logger: zap.NewNop()}, l
}

func counted(h *HookServer, uuid string) (up, down int64, ok bool) {
	v, found := h.Counter.Load(hookTag)
	if !found {
		return 0, 0, false
	}
	c := v.(*counter.TrafficCounter)
	if _, found := c.Counters.Load(uuid); !found {
		return 0, 0, false
	}
	ts := c.GetCounter(uuid)
	return ts.UpCounter.Load(), ts.DownCounter.Load(), true
}

// Hysteria closes a client's QUIC connection when LogTraffic returns false.
// A user the panel removed (deleted, banned, expired, out of traffic) must be
// disconnected on the next traffic callback instead of being let through
// and counted.
func TestLogTrafficDisconnectsRemovedUser(t *testing.T) {
	user := panel.UserInfo{Id: 1, Uuid: "live-user"}
	h, l := newHookTest(t, user)

	if !h.LogTraffic(user.Uuid, 100, 200) {
		t.Fatal("listed user was disconnected")
	}
	if up, down, ok := counted(h, user.Uuid); !ok || up != 100 || down != 200 {
		t.Fatalf("listed user counted up=%d down=%d ok=%v, want 100/200", up, down, ok)
	}

	l.UpdateUser(hookTag, nil, []panel.UserInfo{user}, nil)
	if h.LogTraffic(user.Uuid, 100, 200) {
		t.Fatal("removed user's session was kept open")
	}
	if up, down, _ := counted(h, user.Uuid); up != 100 || down != 200 {
		t.Fatalf("removed user's traffic was counted: up=%d down=%d", up, down)
	}
}

func TestLogTrafficDisconnectsUnknownUser(t *testing.T) {
	h, _ := newHookTest(t)
	if h.LogTraffic("never-listed", 1, 1) {
		t.Fatal("unknown user was let through")
	}
	if _, _, ok := counted(h, "never-listed"); ok {
		t.Fatal("unknown user got a traffic counter")
	}
}

// OverLimit (set by a device-limit rejection) still disconnects exactly once.
func TestLogTrafficOverLimitFiresOnce(t *testing.T) {
	user := panel.UserInfo{Id: 2, Uuid: "over-limit"}
	h, l := newHookTest(t, user)
	v, _ := l.UserLimitInfo.Load(format.UserTag(hookTag, user.Uuid))
	v.(*limiter.UserLimitInfo).OverLimit.Store(true)
	if h.LogTraffic(user.Uuid, 1, 1) {
		t.Fatal("over-limit user was not disconnected")
	}
	if !h.LogTraffic(user.Uuid, 1, 1) {
		t.Fatal("OverLimit fired more than once")
	}
}

// A late callback after the node's limiter was removed must not panic.
func TestLogTrafficAfterLimiterRemoved(t *testing.T) {
	user := panel.UserInfo{Id: 3, Uuid: "teardown"}
	h, _ := newHookTest(t, user)
	limiter.DeleteLimiter(hookTag)
	if h.LogTraffic(user.Uuid, 1, 1) {
		t.Fatal("session kept open after the node's limiter was removed")
	}
}
