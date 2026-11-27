package dispatcher

import (
	"sync/atomic"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	xdispatcher "github.com/xtls/xray-core/app/dispatcher"
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

// newUserStatWriter wraps w so every byte written through it is credited
// to v (one direction of one user's TrafficStorage).
//
// It must return xray's own *dispatcher.SizeStatWriter, not a V2bX type.
// When XTLS Vision switches a connection to splice, xray's
// proxy.CopyRawConnIfExist copies the downlink in the kernel and credits the
// user only via `writer.(*dispatcher.SizeStatWriter).Counter.Add(n)`. Any
// other wrapper type fails that assertion, and every spliced byte goes
// unbilled (measured: 9,248 B billed for a 20 MiB Vision download).
func newUserStatWriter(w buf.Writer, v *atomic.Int64, parent *counter.TrafficCounter, uuid string) *xdispatcher.SizeStatWriter {
	return &xdispatcher.SizeStatWriter{
		Counter: &counter.XrayTrafficCounter{V: v, Parent: parent, UUID: uuid},
		Writer:  w,
	}
}
