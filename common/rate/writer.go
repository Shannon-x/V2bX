package rate

import (
	"sync"
	"time"

	"github.com/juju/ratelimit"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type Writer struct {
	writer   buf.Writer
	limiter  *DynamicBucket
	done     chan struct{}
	doneOnce sync.Once
}

func NewRateLimitWriter(writer buf.Writer, limiter *DynamicBucket) buf.Writer {
	return &Writer{
		writer:  writer,
		limiter: limiter,
		done:    make(chan struct{}),
	}
}

func (w *Writer) signalDone() {
	w.doneOnce.Do(func() { close(w.done) })
}

func (w *Writer) Close() error {
	w.signalDone()
	return common.Close(w.writer)
}

func (w *Writer) Interrupt() {
	w.signalDone()
	common.Interrupt(w.writer)
}

// waitTokens is the interruptible token wait — see Conn.waitTokens.
// W6 review #14: on Close/Interrupt the in-flight rate wait aborts instead
// of pinning the upstream link for the full token duration.
func (w *Writer) waitTokens(l *ratelimit.Bucket, n int64) {
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
	case <-w.done:
	}
}

func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	// W3.5 / audit #19 #20 #21: charge tokens BEFORE the write, with a plain
	// Wait that actually blocks (the previous WaitMaxDuration silently let
	// traffic through whenever the wait exceeded its 5s cap).
	//
	// W6 follow-up: split mb into chunks so a 256 KB MultiBuffer on a low
	// rate limit doesn't induce a single multi-second Wait. Each batch is
	// at most chunkSizeFor() bytes; we Wait per batch and write that batch
	// alone. xray MultiBuffers are typically 32–256 KB, so this only kicks
	// in for the largest writes.
	limiter := w.limiter.Get()
	if limiter == nil {
		return w.writer.WriteMultiBuffer(mb)
	}
	chunk := chunkSizeFor(limiter)
	for len(mb) > 0 {
		// Accumulate buffers from mb's head until the next one would push
		// the batch over `chunk` (and we already have something to send).
		// A single oversized buffer (> chunk) still gets sent on its own
		// — we can't split it without copying.
		var batchLen int32
		split := 0
		for split < len(mb) {
			bbLen := mb[split].Len()
			if batchLen > 0 && batchLen+bbLen > int32(chunk) {
				break
			}
			batchLen += bbLen
			split++
			if batchLen >= int32(chunk) {
				break
			}
		}
		batch := mb[:split]
		mb = mb[split:]
		if batchLen > 0 {
			w.waitTokens(limiter, int64(batchLen))
		}
		if err := w.writer.WriteMultiBuffer(batch); err != nil {
			// On error, release any buffers we haven't handed off yet —
			// matching buf.Writer's "callee owns the MultiBuffer" contract.
			if len(mb) > 0 {
				buf.ReleaseMulti(mb)
			}
			return err
		}
	}
	return nil
}

