package counter

import (
	"sync/atomic"

	fstats "github.com/xtls/xray-core/features/stats"
)

var _ fstats.Counter = (*XrayTrafficCounter)(nil)

// XrayTrafficCounter adapts one TrafficStorage field to xray's stats.Counter.
//
// The xray dispatcher hands it to xray as the Counter of xray's own
// dispatcher.SizeStatWriter. When XTLS Vision switches a connection to
// splice, xray moves the downlink in the kernel and credits the user only
// through that Counter, once, when the connection ends.
//
// Parent and UUID are optional. When both are set, every non-zero Add also
// marks the user dirty so the next report (TrafficCounter.IterateDirty)
// picks the bytes up. That matters for the splice credit, which can land
// long after the user's last ordinary write.
type XrayTrafficCounter struct {
	V      *atomic.Int64
	Parent *TrafficCounter
	UUID   string
}

func (c *XrayTrafficCounter) Value() int64 {
	return c.V.Load()
}

func (c *XrayTrafficCounter) Set(newValue int64) int64 {
	return c.V.Swap(newValue)
}

func (c *XrayTrafficCounter) Add(delta int64) int64 {
	n := c.V.Add(delta)
	if delta != 0 && c.Parent != nil && c.UUID != "" {
		c.Parent.markDirty(c.UUID)
	}
	return n
}
