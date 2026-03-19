package rate

import (
	"github.com/juju/ratelimit"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type Writer struct {
	writer  buf.Writer
	limiter *ratelimit.Bucket
}

func NewRateLimitWriter(writer buf.Writer, limiter *ratelimit.Bucket) buf.Writer {
	return &Writer{
		writer:  writer,
		limiter: limiter,
	}
}

func (w *Writer) Close() error {
	return common.Close(w.writer)
}

func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	n := int64(mb.Len())
	// Burst optimization: allow small packets through immediately
	// to reduce latency for interactive traffic
	if n > 0 {
		if avail := w.limiter.Available(); avail >= n {
			w.limiter.TakeAvailable(n)
		} else {
			w.limiter.Wait(n)
		}
	}
	return w.writer.WriteMultiBuffer(mb)
}
