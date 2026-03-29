package rate

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
)

// DynamicBucket supports atomic hot-swap of rate limit bucket.
// All connections sharing the same DynamicBucket will see updated rates
// after Update() is called — new I/O operations pick up the latest bucket
// via Get(), matching v2node's approach.
type DynamicBucket struct {
	v atomic.Value // *ratelimit.Bucket
}

func NewDynamicBucket(rate int64) *DynamicBucket {
	b := ratelimit.NewBucketWithQuantum(time.Second, rate, rate)
	d := &DynamicBucket{}
	d.v.Store(b)
	return d
}

func (d *DynamicBucket) Get() *ratelimit.Bucket {
	return d.v.Load().(*ratelimit.Bucket)
}

func (d *DynamicBucket) Update(rate int64) {
	newB := ratelimit.NewBucketWithQuantum(time.Second, rate, rate)
	d.v.Store(newB)
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
	c.limiter.Get().Wait(int64(len(b)))
	return c.Conn.Read(b)
}

func (c *Conn) Write(b []byte) (n int, err error) {
	c.limiter.Get().Wait(int64(len(b)))
	return c.Conn.Write(b)
}
