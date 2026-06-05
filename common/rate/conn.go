package rate

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
)

// W3.5 / audit #19 #20 #21 #52: the previous limiter wired the token bucket
// with quantum = capacity = 1s, called WaitMaxDuration AFTER the I/O completed,
// and ignored the WaitMaxDuration bool (which is set false when the wait
// would have exceeded the cap). Net effect: per-user speed limiting was
// effectively defeated for short transfers and bursts. Three independent
// changes there restored proper shaping:
//
//  1. bucketQuantum = 10ms refills tokens at ~100 Hz instead of 1 Hz, so
//     speeds match the configured Mbps over sub-second windows (no more
//     1-second stair-step that stalled TLS handshakes and gRPC headers).
//  2. The Wait happens BEFORE the underlying Write call, so tokens
//     gate the I/O rather than being debited after the fact.
//  3. Plain Wait (no max) replaces WaitMaxDuration so the limiter blocks
//     even when the wait would be long, instead of silently letting the
//     traffic through and never charging tokens for it.
//
// W6 follow-up: refinement to the Read path specifically. Pre-Read
// Wait(len(b)) was over-debiting idle and short-read connections (a 32 KB
// buffer on a 100 KB/s link forced a 320 ms wait even when the peer had
// nothing to send right now, wasting ~28 KB of tokens every Read). Two
// changes:
//
//  4. Read now charges tokens POST-I/O for the actual n bytes returned.
//     Idle (n=0) costs nothing; short reads pay only what they got.
//  5. Both Read and Write cap a single call to chunkSizeFor() bytes
//     (~100 ms of rate budget, floored at 1 KB, ceiling 64 KB). An
//     oversized buffer no longer translates to a single multi-second
//     Wait — the work splits naturally across multiple I/O calls.
const bucketQuantum = 10 * time.Millisecond

// chunkSizeFor bounds a single Read/Write to roughly 100 ms of token
// budget on the given bucket. Floor 1 KB so even very low-rate users
// make some forward progress per call; ceiling 64 KB so high-rate users
// don't blow up syscall overhead.
//
// W6 follow-up: avoids "a 256 KB buffer on a 1 KB/s rate limit blocks
// Read() for 256 seconds" pathology.
func chunkSizeFor(b *ratelimit.Bucket) int {
	const (
		minChunk = 1 << 10  // 1 KB
		maxChunk = 64 << 10 // 64 KB
	)
	chunk := int(b.Capacity() / 10)
	if chunk < minChunk {
		return minChunk
	}
	if chunk > maxChunk {
		return maxChunk
	}
	return chunk
}

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
		done:    make(chan struct{}),
	}
}

type Conn struct {
	net.Conn
	limiter  *DynamicBucket
	done     chan struct{}
	doneOnce sync.Once
}

// Close signals the done channel so any in-flight rate Wait is interrupted,
// then closes the underlying conn.
//
// W6 review #14: the previous bare l.Wait(n) used juju's uninterruptible
// time.Sleep. When the peer vanished mid-transfer, the goroutine + FD + the
// upstream transport.Link stayed pinned for the FULL token wait (seconds to
// minutes on a low SpeedLimit), accumulating half-closed sockets in
// FIN-WAIT-2 / CLOSE_WAIT. waitTokens below now blocks in a select on a
// timer AND this done channel, so Close() releases the wait immediately.
func (c *Conn) Close() error {
	c.doneOnce.Do(func() { close(c.done) })
	return c.Conn.Close()
}

// waitTokens removes n tokens and blocks for the resulting duration, but
// aborts early if the conn is closed. Take() has already debited the tokens
// (so the rate accounting stays correct even on early abort).
func (c *Conn) waitTokens(l *ratelimit.Bucket, n int64) {
	if n <= 0 {
		return
	}
	d := l.Take(n)
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-c.done:
	}
}

func (c *Conn) Read(b []byte) (n int, err error) {
	// W6 follow-up: post-Read accounting (charge for bytes actually read,
	// not buffer length) + chunk cap to bound per-call latency. See the
	// const-block doc above for the full rationale. Idle peers (n == 0)
	// cost nothing; slow peers pay only for what they delivered.
	target := b
	l := c.limiter.Get()
	if l != nil && len(target) > 0 {
		if chunk := chunkSizeFor(l); chunk < len(target) {
			target = target[:chunk]
		}
	}
	n, err = c.Conn.Read(target)
	if n > 0 && l != nil {
		c.waitTokens(l, int64(n))
	}
	return n, err
}

func (c *Conn) Write(b []byte) (n int, err error) {
	// W3.5 + W6 follow-up: still token-gate the write (pre-I/O Wait), but
	// chunk through the buffer so an oversized b can't induce a single
	// multi-second Wait on a low-rate connection. io.Writer's "wrote all
	// of b or returned an error" contract is preserved by the for-loop.
	l := c.limiter.Get()
	if l == nil {
		return c.Conn.Write(b)
	}
	chunk := chunkSizeFor(l)
	for n < len(b) {
		end := n + chunk
		if end > len(b) {
			end = len(b)
		}
		piece := b[n:end]
		c.waitTokens(l, int64(len(piece)))
		written, werr := c.Conn.Write(piece)
		n += written
		if werr != nil {
			return n, werr
		}
	}
	return n, nil
}
