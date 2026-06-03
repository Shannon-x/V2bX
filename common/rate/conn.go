package rate

import (
	"net"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
)

// W3.5 / audit #19 #20 #21 #52: the previous limiter wired the token bucket
// with quantum = capacity = 1s, called WaitMaxDuration AFTER the I/O completed,
// and ignored the WaitMaxDuration bool (which is set false when the wait
// would have exceeded the cap). Net effect: per-user speed limiting was
// effectively defeated for short transfers and bursts. Three independent
// changes here restore proper shaping:
//
//  1. bucketQuantum = 10ms refills tokens at ~100 Hz instead of 1 Hz, so
//     speeds match the configured Mbps over sub-second windows (no more
//     1-second stair-step that stalled TLS handshakes and gRPC headers).
//  2. The Wait happens BEFORE the underlying Read/Write call, so tokens
//     gate the I/O rather than being debited after the fact (a "free first
//     burst" was the most-visible symptom).
//  3. Plain Wait (no max) replaces WaitMaxDuration so the limiter blocks
//     even when the wait would be long, instead of silently letting the
//     traffic through and never charging tokens for it.
const bucketQuantum = 10 * time.Millisecond

// DynamicBucket supports atomic hot-swap of rate limit bucket.
// All connections sharing the same DynamicBucket will see updated rates
// after Update() is called — new I/O operations pick up the latest bucket
// via Get(), matching v2node's approach.
type DynamicBucket struct {
	v atomic.Value // *ratelimit.Bucket
}

// newBucket builds a token bucket with a quantum aligned to bucketQuantum,
// capacity = rate (1s burst), and refill of rate * quantum / 1s rounded up.
func newBucket(rate int64) *ratelimit.Bucket {
	if rate < 1 {
		rate = 1
	}
	fill := rate / int64(time.Second/bucketQuantum) // tokens per quantum
	if fill < 1 {
		fill = 1
	}
	return ratelimit.NewBucketWithQuantum(bucketQuantum, rate, fill)
}

func NewDynamicBucket(rate int64) *DynamicBucket {
	d := &DynamicBucket{}
	d.v.Store(newBucket(rate))
	return d
}

func (d *DynamicBucket) Get() *ratelimit.Bucket {
	return d.v.Load().(*ratelimit.Bucket)
}

func (d *DynamicBucket) Update(rate int64) {
	d.v.Store(newBucket(rate))
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
	// W3.5: gate I/O on tokens — match v2node's pre-read Wait semantics. The
	// pre-charge is the maximum we could read; if the underlying Read returns
	// fewer bytes we over-debit by the difference, which is a small bias
	// in favour of the limit (acceptable for a rate cap).
	if l := c.limiter.Get(); l != nil && len(b) > 0 {
		l.Wait(int64(len(b)))
	}
	return c.Conn.Read(b)
}

func (c *Conn) Write(b []byte) (n int, err error) {
	// W3.5: charge tokens before sending so a slow consumer cannot drain the
	// peer pipe in one burst.
	if l := c.limiter.Get(); l != nil && len(b) > 0 {
		l.Wait(int64(len(b)))
	}
	return c.Conn.Write(b)
}
