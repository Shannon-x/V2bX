package rate

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
)

const rateLimitSleepInterval = 10 * time.Millisecond

// DynamicBucket supports atomic hot-swap of rate limit bucket.
// All connections sharing the same DynamicBucket will see updated rates
// immediately after Update() is called.
type DynamicBucket struct {
	bucket atomic.Pointer[ratelimit.Bucket]
}

func NewDynamicBucket(limit int64) *DynamicBucket {
	db := &DynamicBucket{}
	b := ratelimit.NewBucketWithQuantum(time.Second, limit, limit)
	db.bucket.Store(b)
	return db
}

func (db *DynamicBucket) Get() *ratelimit.Bucket {
	return db.bucket.Load()
}

func (db *DynamicBucket) Update(limit int64) {
	b := ratelimit.NewBucketWithQuantum(time.Second, limit, limit)
	db.bucket.Store(b)
}

func NewConnRateLimiter(c net.Conn, l *DynamicBucket) *Conn {
	return &Conn{
		Conn:    c,
		limiter: l,
	}
}

type Conn struct {
	net.Conn
	limiter *DynamicBucket
}

func (c *Conn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		waitForTokens(c.limiter, int64(n))
	}
	return n, err
}

func (c *Conn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		waitForTokens(c.limiter, int64(n))
	}
	return n, err
}

// waitForTokens consumes tokens from the DynamicBucket in a non-blocking loop.
// It fetches the current bucket on each iteration, so rate changes via
// DynamicBucket.Update() take effect immediately for existing connections.
func waitForTokens(db *DynamicBucket, n int64) {
	for n > 0 {
		b := db.Get()
		if b == nil {
			return
		}
		taken := b.TakeAvailable(n)
		n -= taken
		if n > 0 {
			time.Sleep(rateLimitSleepInterval)
		}
	}
}
