package limiter

import (
	"testing"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/common/rate"
	"github.com/InazumaV/V2bX/conf"
)

const (
	expiryTag  = "dynamic-expiry-test"
	expiryUUID = "dynamic-user"
)

func newExpiryTestLimiter(t *testing.T, nodeLimit, userLimit int) (*Limiter, string) {
	t.Helper()
	Init()
	l := AddLimiter("hysteria2", expiryTag, &conf.LimitConfig{SpeedLimit: nodeLimit},
		[]panel.UserInfo{{Id: 7, Uuid: expiryUUID, SpeedLimit: userLimit}}, map[int]int{})
	t.Cleanup(func() { DeleteLimiter(expiryTag) })
	return l, format.UserTag(expiryTag, expiryUUID)
}

// expire makes the user's dynamic limit look expired a minute ago.
func expire(t *testing.T, l *Limiter, taguuid string) {
	t.Helper()
	v, ok := l.UserLimitInfo.Load(taguuid)
	if !ok {
		t.Fatal("user record missing before expiry")
	}
	v.(*UserLimitInfo).ExpireTime.Store(time.Now().Add(-time.Minute).Unix())
}

func bucketCapacity(t *testing.T, b *rate.DynamicBucket) int64 {
	t.Helper()
	if b == nil {
		t.Fatal("expected a rate bucket, got none")
	}
	return b.Get().Capacity()
}

// A user without a base speed limit used to have its record deleted when the
// dynamic limit expired. The next CheckLimit then treated the user as removed
// by the panel and rejected every connection on this node until a restart.
func TestExpiredDynamicLimitKeepsUnlimitedUser(t *testing.T) {
	l, taguuid := newExpiryTestLimiter(t, 0, 0)
	if err := l.UpdateDynamicSpeedLimit(expiryTag, expiryUUID, 5, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bucket, rejected := l.CheckLimit(taguuid, "1.1.1.1", true, true)
	if rejected {
		t.Fatal("rejected while the dynamic limit was active")
	}
	if got, want := bucketCapacity(t, bucket), int64(5*1000000/8); got != want {
		t.Fatalf("dynamic bucket capacity %d, want %d", got, want)
	}

	expire(t, l, taguuid)
	for i := 0; i < 3; i++ {
		bucket, rejected = l.CheckLimit(taguuid, "1.1.1.1", true, true)
		if rejected {
			t.Fatalf("call %d after expiry rejected the user", i+1)
		}
		if bucket != nil {
			t.Fatalf("call %d after expiry still rate-limited an unlimited user", i+1)
		}
	}
	v, ok := l.UserLimitInfo.Load(taguuid)
	if !ok {
		t.Fatal("expiry deleted the user's record")
	}
	info := v.(*UserLimitInfo)
	if info.ExpireTime.Load() != 0 || info.DynamicSpeedLimit.Load() != 0 {
		t.Fatalf("expiry left dynamic state: expire=%d limit=%d",
			info.ExpireTime.Load(), info.DynamicSpeedLimit.Load())
	}
	if _, ok := l.SpeedLimiter.Load(taguuid); ok {
		t.Fatal("expiry left the dynamic-rate bucket behind")
	}
}

// With a base speed limit, the shared bucket used to keep the dynamic rate
// after expiry, because CheckLimit returns an existing bucket as is.
func TestExpiredDynamicLimitRestoresBaseRate(t *testing.T) {
	l, taguuid := newExpiryTestLimiter(t, 0, 50)
	if err := l.UpdateDynamicSpeedLimit(expiryTag, expiryUUID, 5, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bucket, _ := l.CheckLimit(taguuid, "1.1.1.1", true, true)
	if got, want := bucketCapacity(t, bucket), int64(5*1000000/8); got != want {
		t.Fatalf("dynamic bucket capacity %d, want %d", got, want)
	}

	expire(t, l, taguuid)
	after, rejected := l.CheckLimit(taguuid, "1.1.1.1", true, true)
	if rejected {
		t.Fatal("rejected after expiry")
	}
	if after != bucket {
		t.Fatal("expiry replaced the shared bucket instead of updating it")
	}
	if got, want := bucketCapacity(t, after), int64(50*1000000/8); got != want {
		t.Fatalf("bucket capacity after expiry %d, want base rate %d", got, want)
	}
}

// The node-wide limit still applies once a user's dynamic limit expires.
func TestExpiredDynamicLimitFallsBackToNodeRate(t *testing.T) {
	l, taguuid := newExpiryTestLimiter(t, 100, 0)
	if err := l.UpdateDynamicSpeedLimit(expiryTag, expiryUUID, 5, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bucket, _ := l.CheckLimit(taguuid, "1.1.1.1", true, true)
	expire(t, l, taguuid)
	after, rejected := l.CheckLimit(taguuid, "1.1.1.1", true, true)
	if rejected {
		t.Fatal("rejected after expiry")
	}
	if after != bucket {
		t.Fatal("expiry replaced the shared bucket instead of updating it")
	}
	if got, want := bucketCapacity(t, after), int64(100*1000000/8); got != want {
		t.Fatalf("bucket capacity after expiry %d, want node rate %d", got, want)
	}
}

// A user the panel removed is still rejected.
func TestRemovedUserStillRejected(t *testing.T) {
	l, taguuid := newExpiryTestLimiter(t, 0, 0)
	l.UpdateUser(expiryTag, nil, []panel.UserInfo{{Id: 7, Uuid: expiryUUID}}, nil)
	if _, rejected := l.CheckLimit(taguuid, "1.1.1.1", true, true); !rejected {
		t.Fatal("removed user was admitted")
	}
}
