package hy2

import (
	"errors"
	"net"
	"testing"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/limiter"
	"github.com/apernet/hysteria/core/v2/server"
)

type stubOutbound struct {
	tcpCalls   []string
	udpCalls   []string
	checkCalls []string
}

func (s *stubOutbound) TCP(reqAddr string) (net.Conn, error) {
	s.tcpCalls = append(s.tcpCalls, reqAddr)
	return nil, nil
}

func (s *stubOutbound) UDP(reqAddr string) (server.UDPConn, error) {
	s.udpCalls = append(s.udpCalls, reqAddr)
	return nil, nil
}

func (s *stubOutbound) CheckUDP(reqAddr string) error {
	s.checkCalls = append(s.checkCalls, reqAddr)
	return nil
}

func newTestLimiter(t *testing.T, tag string, rules *panel.Rules, cfg *conf.LimitConfig) {
	t.Helper()
	limiter.Init()
	if cfg == nil {
		cfg = &conf.LimitConfig{}
	}
	l := limiter.AddLimiter("hysteria2", tag, cfg, nil, map[int]int{})
	t.Cleanup(func() { limiter.DeleteLimiter(tag) })
	if rules != nil {
		if err := l.UpdateRule(rules); err != nil {
			t.Fatalf("UpdateRule: %v", err)
		}
	}
}

// hy2 以前完全不执行面板规则，这里锁死修复后的行为。
func TestRuleOutboundEnforcesPanelRules(t *testing.T) {
	const tag = "hy2-rule-test"
	newTestLimiter(t, tag, &panel.Rules{
		Regexp:      []string{`(^|\.)tracker\.example\.com$`},
		InboundIP:   []string{"203.0.113.0/24"},
		InboundPort: []string{"6881-6999"},
	}, nil)

	next := &stubOutbound{}
	ob := newRuleOutbound(tag, next, nil)

	blocked := map[string]string{
		"域名黑名单":  "tracker.example.com:443",
		"IP 黑名单": "203.0.113.7:443",
		"端口黑名单":  "198.51.100.5:6881",
	}
	for name, addr := range blocked {
		if _, err := ob.TCP(addr); !errors.Is(err, errBlockedByRule) {
			t.Errorf("%s: TCP(%s) 应被拦截，实际 err=%v", name, addr, err)
		}
		if _, err := ob.UDP(addr); !errors.Is(err, errBlockedByRule) {
			t.Errorf("%s: UDP(%s) 应被拦截，实际 err=%v", name, addr, err)
		}
		// CheckUDP 是逐包调用的那个点，必须同样拦住
		if err := ob.CheckUDP(addr); !errors.Is(err, errBlockedByRule) {
			t.Errorf("%s: CheckUDP(%s) 应被拦截，实际 err=%v", name, addr, err)
		}
	}
	if len(next.tcpCalls)+len(next.udpCalls)+len(next.checkCalls) != 0 {
		t.Errorf("被拦截的请求不应下传到真实出站，实际 tcp=%v udp=%v check=%v",
			next.tcpCalls, next.udpCalls, next.checkCalls)
	}

	allowed := "93.184.216.34:443"
	if _, err := ob.TCP(allowed); err != nil {
		t.Errorf("正常地址不应被拦截: %v", err)
	}
	if err := ob.CheckUDP(allowed); err != nil {
		t.Errorf("正常地址的 CheckUDP 不应被拦截: %v", err)
	}
	if len(next.tcpCalls) != 1 || len(next.checkCalls) != 1 {
		t.Errorf("放行的请求应下传到真实出站，实际 tcp=%v check=%v", next.tcpCalls, next.checkCalls)
	}
}

// 拿不到 limiter 时必须放行，否则规则系统一出问题整个节点就废了。
func TestRuleOutboundFailsOpenWithoutLimiter(t *testing.T) {
	limiter.Init()
	next := &stubOutbound{}
	ob := newRuleOutbound("不存在的-tag", next, nil)
	if _, err := ob.TCP("example.com:443"); err != nil {
		t.Fatalf("无 limiter 时应放行，实际 err=%v", err)
	}
	if err := ob.CheckUDP("example.com:443"); err != nil {
		t.Fatalf("无 limiter 时应放行，实际 err=%v", err)
	}
}

func TestRuleOutboundIgnoresMalformedAddr(t *testing.T) {
	const tag = "hy2-badaddr"
	newTestLimiter(t, tag, nil, nil)
	next := &stubOutbound{}
	ob := newRuleOutbound(tag, next, nil)
	if _, err := ob.TCP("没有端口的地址"); err != nil {
		t.Fatalf("地址形态不认识时应放行给下游报错，实际 err=%v", err)
	}
}

var dhtQuery = []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe")

func TestBTRequestHookRejectsDHT(t *testing.T) {
	const tag = "hy2-bt-on"
	newTestLimiter(t, tag, nil, &conf.LimitConfig{BlockBittorrentUDP: true})

	h := newBTRequestHook(tag, nil, nil)
	if !h.Check(true, "1.2.3.4:6881") {
		t.Fatal("开启 BT 拦截后，UDP 请求必须交给 hook 检查")
	}
	// TCP 一侧刻意不接管，避免改变 hysteria 的 fast-open 语义
	if h.Check(false, "1.2.3.4:443") {
		t.Fatal("TCP 请求不应被 hook 接管")
	}

	addr := "1.2.3.4:6881"
	if err := h.UDP(dhtQuery, &addr); !errors.Is(err, errBittorrent) {
		t.Fatalf("DHT 首包应被拒绝，实际 err=%v", err)
	}
	dns := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := h.UDP(dns, &addr); err != nil {
		t.Fatalf("普通 UDP 首包不应被拒绝，实际 err=%v", err)
	}
}

func TestBTRequestHookDisabledByDefault(t *testing.T) {
	const tag = "hy2-bt-off"
	newTestLimiter(t, tag, nil, nil)

	h := newBTRequestHook(tag, nil, nil)
	if h.Check(true, "1.2.3.4:6881") {
		t.Fatal("未开启时不应接管 UDP 请求，否则等于悄悄改变默认行为")
	}
	addr := "1.2.3.4:6881"
	if err := h.UDP(dhtQuery, &addr); err != nil {
		t.Fatalf("未开启时不应拦截，实际 err=%v", err)
	}
}

// 面板下发含 bittorrent 的 protocol 审计规则时应自动开启，无需改 config.json。
func TestBTRequestHookEnabledByPanelProtocolRule(t *testing.T) {
	const tag = "hy2-bt-panel"
	newTestLimiter(t, tag, &panel.Rules{Protocol: []string{"BitTorrent"}}, nil)

	h := newBTRequestHook(tag, nil, nil)
	if !h.Check(true, "1.2.3.4:6881") {
		t.Fatal("面板 protocol 规则含 bittorrent 时应自动开启")
	}
	addr := "1.2.3.4:6881"
	if err := h.UDP(dhtQuery, &addr); !errors.Is(err, errBittorrent) {
		t.Fatalf("应拦截，实际 err=%v", err)
	}
}

type stubHook struct {
	checked  bool
	udpCalls int
}

func (s *stubHook) Check(isUDP bool, reqAddr string) bool { return s.checked }
func (s *stubHook) TCP(server.HyStream, *string) ([]byte, error) {
	return []byte("putback"), nil
}
func (s *stubHook) UDP([]byte, *string) error { s.udpCalls++; return nil }

// 原有的 sniff hook 行为必须保持不变。
func TestBTRequestHookPreservesInnerHook(t *testing.T) {
	const tag = "hy2-bt-inner"
	newTestLimiter(t, tag, nil, &conf.LimitConfig{BlockBittorrentUDP: true})

	inner := &stubHook{checked: true}
	h := newBTRequestHook(tag, inner, nil)

	if !h.Check(false, "x:443") {
		t.Fatal("内层 hook 想接管 TCP 时应放行给它")
	}
	if b, _ := h.TCP(nil, nil); string(b) != "putback" {
		t.Fatalf("TCP putback 数据应原样透传，实际 %q", b)
	}
	addr := "1.2.3.4:443"
	dns := []byte{0x12, 0x34, 0x01, 0x00}
	if err := h.UDP(dns, &addr); err != nil {
		t.Fatalf("非 BT 流量应转交内层 hook，实际 err=%v", err)
	}
	if inner.udpCalls != 1 {
		t.Fatalf("内层 hook 的 UDP 应被调用 1 次，实际 %d", inner.udpCalls)
	}

	// BT 流量在转交之前就被拒掉，内层 hook 不应再被调用
	if err := h.UDP(dhtQuery, &addr); !errors.Is(err, errBittorrent) {
		t.Fatalf("BT 首包应被拒绝，实际 err=%v", err)
	}
	if inner.udpCalls != 1 {
		t.Fatalf("BT 首包不应转交内层 hook，实际调用数 %d", inner.udpCalls)
	}
}

// 内层 hook 自己不想接管时，不能因为我们为了 BT 判定返回 true 就把请求塞给它。
func TestBTRequestHookDoesNotForceInnerHook(t *testing.T) {
	const tag = "hy2-bt-noforce"
	newTestLimiter(t, tag, nil, &conf.LimitConfig{BlockBittorrentUDP: true})

	inner := &stubHook{checked: false}
	h := newBTRequestHook(tag, inner, nil)
	addr := "1.2.3.4:443"
	if err := h.UDP([]byte{0x12, 0x34}, &addr); err != nil {
		t.Fatalf("不应报错，实际 err=%v", err)
	}
	if inner.udpCalls != 0 {
		t.Fatalf("内层 hook 不想接管时不应被调用，实际 %d 次", inner.udpCalls)
	}
}
