package dispatcher

import (
	"sync/atomic"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	xraydispatcher "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

var _ buf.TimeoutReader = (*CounterReader)(nil)

type CounterReader struct {
	Reader  buf.TimeoutReader
	Counter *atomic.Int64
	Storage *counter.TrafficStorage
}

func (c *CounterReader) markDirty() {
	if c.Storage != nil {
		c.Storage.MarkDirty()
	}
}

// ReadMultiBufferTimeout forwards the caller-supplied timeout to the underlying
// reader. W1.9 / audit #38: previously the parameter was unnamed and the call
// site hard-coded time.Second, so xray's own timeout configuration was ignored.
func (c *CounterReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb, err := c.Reader.ReadMultiBufferTimeout(timeout)
	if mb.Len() > 0 {
		c.Counter.Add(int64(mb.Len()))
		c.markDirty()
	}
	return mb, err
}

func (c *CounterReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := c.Reader.ReadMultiBuffer()
	if mb.Len() > 0 {
		c.Counter.Add(int64(mb.Len()))
		c.markDirty()
	}
	return mb, err
}

func (c *CounterReader) Close() error {
	return common.Close(c.Reader)
}

func (c *CounterReader) Interrupt() {
	common.Interrupt(c.Reader)
}

// Preserve the exact upstream type: Linux splice recognizes this writer with
// a concrete type assertion and adds the copied bytes through Counter.Add.
// XrayTrafficCounter marks its storage on both this path and ordinary writes.
type SizeStatWriter = xraydispatcher.SizeStatWriter
