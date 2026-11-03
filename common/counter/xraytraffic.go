package counter

import (
	"sync/atomic"

	fstats "github.com/xtls/xray-core/features/stats"
)

var _ fstats.Counter = (*XrayTrafficCounter)(nil)

type XrayTrafficCounter struct {
	V       *atomic.Int64
	Storage *TrafficStorage
}

func (c *XrayTrafficCounter) Value() int64 {
	return c.V.Load()
}

func (c *XrayTrafficCounter) Set(newValue int64) int64 {
	previous := c.V.Swap(newValue)
	if newValue != 0 && c.Storage != nil {
		c.Storage.MarkDirty()
	}
	return previous
}

func (c *XrayTrafficCounter) Add(delta int64) int64 {
	value := c.V.Add(delta)
	if delta != 0 && c.Storage != nil {
		c.Storage.MarkDirty()
	}
	return value
}
