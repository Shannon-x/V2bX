package dispatcher

import (
	"sync/atomic"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

var _ buf.TimeoutReader = (*CounterReader)(nil)

type CounterReader struct {
	Reader  buf.TimeoutReader
	Counter *atomic.Int64
	// W6 / B3: optional dirty-marker so GetUserTrafficSlice can iterate
	// only active users (via TrafficCounter.IterateDirty) instead of the
	// full Counters sync.Map every report period. When unset the wrapper
	// behaves exactly as before.
	Parent *counter.TrafficCounter
	UUID   string
}

func (c *CounterReader) markDirty() {
	if c.Parent != nil && c.UUID != "" {
		c.Parent.MarkDirty(c.UUID)
	}
}

// ReadMultiBufferTimeout forwards the caller-supplied timeout to the underlying
// reader. W1.9 / audit #38: previously the parameter was unnamed and the call
// site hard-coded time.Second, so xray's own timeout configuration was ignored.
func (c *CounterReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb, err := c.Reader.ReadMultiBufferTimeout(timeout)
	if err != nil {
		return nil, err
	}
	if mb.Len() > 0 {
		c.Counter.Add(int64(mb.Len()))
		c.markDirty()
	}
	return mb, nil
}

func (c *CounterReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := c.Reader.ReadMultiBuffer()
	if err != nil {
		return nil, err
	}
	if mb.Len() > 0 {
		c.Counter.Add(int64(mb.Len()))
		c.markDirty()
	}
	return mb, nil
}

func (c *CounterReader) Close() error {
	return common.Close(c.Reader)
}

func (c *CounterReader) Interrupt() {
	common.Interrupt(c.Reader)
}

type SizeStatWriter struct {
	Counter *counter.XrayTrafficCounter
	Writer  buf.Writer
	// W6 / B3: optional dirty-marker — see CounterReader for rationale.
	Parent *counter.TrafficCounter
	UUID   string
}

func (w *SizeStatWriter) markDirty() {
	if w.Parent != nil && w.UUID != "" {
		w.Parent.MarkDirty(w.UUID)
	}
}

func (w *SizeStatWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if mb != nil && mb.Len() > 0 {
		w.Counter.V.Add(int64(mb.Len()))
		w.markDirty()
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *SizeStatWriter) Close() error {
	return common.Close(w.Writer)
}

func (w *SizeStatWriter) Interrupt() {
	common.Interrupt(w.Writer)
}
