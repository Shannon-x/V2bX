package conf

import "fmt"

// XrayQUICConfig controls receive flow credit, not preallocated memory.
// Windows are bytes, MaxIdleTimeout is seconds, and zero retains the
// corresponding default in the pinned Xray core.
type XrayQUICConfig struct {
	InitStreamReceiveWindow     uint64 `json:"InitStreamReceiveWindow"`
	MaxStreamReceiveWindow      uint64 `json:"MaxStreamReceiveWindow"`
	InitConnectionReceiveWindow uint64 `json:"InitConnectionReceiveWindow"`
	MaxConnectionReceiveWindow  uint64 `json:"MaxConnectionReceiveWindow"`
	MaxIncomingStreams          int64  `json:"MaxIncomingStreams"`
	MaxIdleTimeout              int64  `json:"MaxIdleTimeout"`
}

func (c *XrayQUICConfig) Validate() error {
	if c == nil {
		return nil
	}
	for name, value := range map[string]uint64{
		"InitStreamReceiveWindow":     c.InitStreamReceiveWindow,
		"MaxStreamReceiveWindow":      c.MaxStreamReceiveWindow,
		"InitConnectionReceiveWindow": c.InitConnectionReceiveWindow,
		"MaxConnectionReceiveWindow":  c.MaxConnectionReceiveWindow,
	} {
		if value != 0 && (value < 16384 || value > (1<<60)) {
			return fmt.Errorf("Hysteria2QUIC.%s must be zero or between 16384 and 2^60 bytes", name)
		}
	}
	// Validate effective values too: an omitted initial window is 8/20 MiB,
	// so lowering only a maximum must not silently leave initial > maximum.
	effective := func(v, fallback uint64) uint64 {
		if v == 0 {
			return fallback
		}
		return v
	}
	streamInitial := effective(c.InitStreamReceiveWindow, 8<<20)
	streamMax := effective(c.MaxStreamReceiveWindow, 8<<20)
	connInitial := effective(c.InitConnectionReceiveWindow, 20<<20)
	connMax := effective(c.MaxConnectionReceiveWindow, 20<<20)
	if streamInitial > streamMax || connInitial > connMax {
		return fmt.Errorf("Hysteria2QUIC initial windows must not exceed maximum windows (zero uses core defaults)")
	}
	if c.MaxIncomingStreams != 0 && (c.MaxIncomingStreams < 8 || c.MaxIncomingStreams > 1<<60) {
		return fmt.Errorf("Hysteria2QUIC.MaxIncomingStreams must be zero or between 8 and 2^60")
	}
	if c.MaxIdleTimeout != 0 && (c.MaxIdleTimeout < 4 || c.MaxIdleTimeout > 120) {
		return fmt.Errorf("Hysteria2QUIC.MaxIdleTimeout must be zero or between 4 and 120 seconds")
	}
	return nil
}
