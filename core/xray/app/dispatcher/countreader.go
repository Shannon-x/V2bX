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
}

func (c *CounterReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	mb, err := c.Reader.ReadMultiBufferTimeout(time.Second)
	if err != nil {
		return nil, err
	}
	if mb.Len() > 0 {
		c.Counter.Add(int64(mb.Len()))
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
}

func (w *SizeStatWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if mb != nil && mb.Len() > 0 {
		w.Counter.V.Add(int64(mb.Len()))
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *SizeStatWriter) Close() error {
	return common.Close(w.Writer)
}

func (w *SizeStatWriter) Interrupt() {
	common.Interrupt(w.Writer)
}
