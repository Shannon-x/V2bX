package limiter

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
)

// TestUserLimitInfoAtomicRace exercises the W2.6/W2.7 atomic conversion
// of UserLimitInfo fields. Without atomic types, this triggers the race
// detector via concurrent SpeedLimit / DeviceLimit / DynamicSpeedLimit /
// ExpireTime / OverLimit writes from UpdateUser, UpdateDynamicSpeedLimit,
// and OverLimit flips against the CheckLimit reader.
func TestUserLimitInfoAtomicRace(t *testing.T) {
	Init()
	const (
		tag      = "race-test-tag"
		userCnt  = 32
		iters    = 200
		workers  = 16
	)

	users := make([]panel.UserInfo, userCnt)
	for i := range users {
		users[i] = panel.UserInfo{
			Id:          i + 1,
			Uuid:        "uuid-" + strconv.Itoa(i),
			SpeedLimit:  100,
			DeviceLimit: 5,
		}
	}
	l := AddLimiter("xray", tag, &conf.LimitConfig{SpeedLimit: 200}, users, map[int]int{})
	defer DeleteLimiter(tag)

	var wg sync.WaitGroup

	// CheckLimit reader: hammers the hot path.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				u := users[i%userCnt]
				_, _ = l.CheckLimit(format.UserTag(tag, u.Uuid), "10.0.0.1", true, true)
			}
		}()
	}

	// UpdateDynamicSpeedLimit writer: flips DynamicSpeedLimit + ExpireTime.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				u := users[i%userCnt]
				_ = l.UpdateDynamicSpeedLimit(tag, u.Uuid, 50, time.Now().Add(time.Hour))
			}
		}()
	}

	// OverLimit flipper: simulates hy2 logger Connect/TCPRequest/UDPRequest.
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				u := users[(i+w)%userCnt]
				if v, ok := l.UserLimitInfo.Load(format.UserTag(tag, u.Uuid)); ok {
					info := v.(*UserLimitInfo)
					// Pattern matches hy2 logger.go: Store(r).
					info.OverLimit.Store(i%2 == 0)
					// Pattern matches hy2 hook.go: flip-and-skip.
					info.OverLimit.CompareAndSwap(true, false)
				}
			}
		}(w)
	}

	// UpdateUser writer: modifies SpeedLimit / DeviceLimit on the existing set.
	wg.Add(1)
	go func() {
		defer wg.Done()
		modified := make([]panel.UserInfo, len(users))
		for i, u := range users {
			modified[i] = u
			modified[i].SpeedLimit = u.SpeedLimit + 1
		}
		for i := 0; i < iters; i++ {
			l.UpdateUser(tag, nil, nil, modified)
		}
	}()

	wg.Wait()
}
