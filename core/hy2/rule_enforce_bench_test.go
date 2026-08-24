package hy2

import (
	"net"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/limiter"
	"github.com/apernet/hysteria/core/v2/server"
)

// 基准专用的空桩：stubOutbound 会 append 到切片，那部分开销会污染测量结果。
type nopOutbound struct{}

func (nopOutbound) TCP(string) (net.Conn, error)       { return nil, nil }
func (nopOutbound) UDP(string) (server.UDPConn, error) { return nil, nil }
func (nopOutbound) CheckUDP(string) error              { return nil }

func benchLimiter(b *testing.B, tag string, rules *panel.Rules) {
	b.Helper()
	limiter.Init()
	l := limiter.AddLimiter("hysteria2", tag, &conf.LimitConfig{}, nil, map[int]int{})
	b.Cleanup(func() { limiter.DeleteLimiter(tag) })
	if rules != nil {
		if err := l.UpdateRule(rules); err != nil {
			b.Fatal(err)
		}
	}
}

// CheckUDP 是 hysteria 逐包调用的那个点（结果按会话缓存），
// 这里量的就是它在热路径上的净开销。
func BenchmarkRuleOutboundCheckUDP(b *testing.B) {
	const addr = "93.184.216.34:443"

	b.Run("无任何阻断规则", func(b *testing.B) {
		benchLimiter(b, "bench-norules", nil)
		ob := newRuleOutbound("bench-norules", nopOutbound{}, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ob.CheckUDP(addr)
		}
	})

	b.Run("有域名IP端口规则", func(b *testing.B) {
		benchLimiter(b, "bench-rules", &panel.Rules{
			Regexp:      []string{`(^|\.)tracker\.example\.com$`, `(torrent|magnet)`},
			InboundIP:   []string{"203.0.113.0/24", "198.51.100.0/24"},
			InboundPort: []string{"6881-6999", "25"},
		})
		ob := newRuleOutbound("bench-rules", nopOutbound{}, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = ob.CheckUDP(addr)
		}
	})
}

// UDP 首包的 BT 判定开销。只在每个 UDP 会话的第一个包上跑一次。
func BenchmarkBTRequestHookUDP(b *testing.B) {
	dns := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	b.Run("未开启", func(b *testing.B) {
		benchLimiter(b, "bench-bt-off", nil)
		h := newBTRequestHook("bench-bt-off", nil, nil)
		addr := "93.184.216.34:443"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = h.UDP(dns, &addr)
		}
	})

	b.Run("已开启", func(b *testing.B) {
		benchLimiter(b, "bench-bt-on", &panel.Rules{Protocol: []string{"bittorrent"}})
		h := newBTRequestHook("bench-bt-on", nil, nil)
		addr := "93.184.216.34:443"
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = h.UDP(dns, &addr)
		}
	})
}
