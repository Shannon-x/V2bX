package rate

import (
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type Writer struct {
	writer  buf.Writer
	limiter *DynamicBucket
}

func NewRateLimitWriter(writer buf.Writer, limiter *DynamicBucket) buf.Writer {
	return &Writer{
		writer:  writer,
		limiter: limiter,
	}
}

func (w *Writer) Close() error {
	return common.Close(w.writer)
}

func (w *Writer) Interrupt() {
	common.Interrupt(w.writer)
}

func (w *Writer) WriteMultiBuffer(mb buf.MultiBuffer) error {
	// W3.5 / audit #19 #20 #21: charge tokens BEFORE the write, with a plain
	// Wait that actually blocks (the previous WaitMaxDuration silently let
	// traffic through whenever the wait exceeded its 5s cap).
	if limiter := w.limiter.Get(); limiter != nil {
		if n := int64(mb.Len()); n > 0 {
			limiter.Wait(n)
		}
	}
	return w.writer.WriteMultiBuffer(mb)
}

