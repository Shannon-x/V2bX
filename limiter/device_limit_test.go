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

func TestDeviceLimitCountsLocalPendingIPsDuringBurst(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(3, 0)
	for i := 1; i <= 3; i++ {
		if _, rejected := l.CheckLimit(taguuid, fmt.Sprintf("10.0.0.%d", i), true, true); rejected {
			t.Fatalf("IP %d rejected before local pending count reached the limit", i)
		}
	}
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.4", true, true); !rejected {
		t.Fatal("fourth IP was admitted even though three local IPs already consumed the limit")
	}
}

func TestDeviceLimitRecoversImmediatelyWhenAliveDeltaDrops(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(2, 2)
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.3", true, true); !rejected {
		t.Fatal("new IP should be rejected while the global count is at the limit")
	}

	l.MergeAliveCounts(map[int]int{42: 1})
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.3", true, true); rejected {
		t.Fatal("IP remained rejected after the panel delta dropped below the limit")
	}
}

func TestPreviouslyReportedIPDoesNotConsumeAnotherPendingSlot(t *testing.T) {
	l, taguuid := newDeviceTestLimiter(2, 2)
	l.OldUserOnline.Store("10.0.0.1", 42)

	if _, rejected := l.CheckLimit(taguuid, "10.0.0.1", true, true); rejected {
		t.Fatal("previously reported IP must remain usable while the count is at the limit")
	}
	if _, rejected := l.CheckLimit(taguuid, "10.0.0.2", true, true); !rejected {
		t.Fatal("a different unreported IP must be rejected while the global count is at the limit")
	}
}

func TestConcurrentLocalBurstAdmitsExactlyTheLimit(t *testing.T) {
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
	if got := allowed.Load(); got != 5 {
		t.Fatalf("concurrent burst admitted %d IPs, want exactly 5", got)
	}
}
