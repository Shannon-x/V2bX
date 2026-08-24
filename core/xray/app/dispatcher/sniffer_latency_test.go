package dispatcher

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol/bittorrent"
	"github.com/xtls/xray-core/common/protocol/quic"
)

// newUDPSnifferChain 复刻 NewSniffer 注册的 UDP 侧嗅探器，
// 与 sniffer.go 里的注册保持一致。
func newUDPSnifferChain() *Sniffer {
	return &Sniffer{sniffer: []protocolSnifferWithMetadata{
		{func(c context.Context, b []byte) (SniffResult, error) { return quic.SniffQUIC(b) }, false, net.Network_UDP},
		{func(c context.Context, b []byte) (SniffResult, error) {
			h, err := SniffBittorrentUDP(b)
			if err != nil {
				return nil, err
			}
			return h, nil
		}, false, net.Network_UDP},
	}}
}

// 首包延迟才是用户能感知的东西，逐包那 3ns 不是。
//
// 关键在 sniffer() 的循环（default.go）：Sniff 返回 common.ErrNoClue 时
// totalAttempt++ 然后**再读一次**，最多阻塞到 200ms 的 cacheDeadline 用完。
// 也就是说只要嗅探链里还有人「说不准」，这条 UDP 流的第一个包就要等。
//
// 上游 bittorrent.SniffUTP 对长度不足 20 字节的包返回 ErrNoClue
// （bittorrent.go:38-40），而短 UDP 包在真实流量里很常见
// （DNS 响应、游戏心跳、QUIC 之外的各类小报文）。
// 本地实现对拿不准的一律给确定性错误，让嗅探立刻结束。
func TestShortUDPPacketDoesNotStallSniffing(t *testing.T) {
	ctx := context.Background()
	short := []byte{0x01, 0x02, 0x03, 0x04, 0x05} // 5 字节，短于 uTP 头

	// 先确认上游行为：对短包「说不准」
	if _, err := bittorrent.SniffUTP(short); err != common.ErrNoClue {
		t.Fatalf("前提不成立：上游 SniffUTP 对短包返回 %v，期望 ErrNoClue", err)
	}
	// QUIC 对短包是确定性拒绝，所以 pending 与否完全取决于 BT 嗅探器
	if _, err := quic.SniffQUIC(short); err == common.ErrNoClue {
		t.Fatalf("前提不成立：SniffQUIC 对短包返回了 ErrNoClue")
	}

	// 复刻打补丁前的嗅探链
	oldChain := &Sniffer{sniffer: []protocolSnifferWithMetadata{
		{func(c context.Context, b []byte) (SniffResult, error) { return quic.SniffQUIC(b) }, false, net.Network_UDP},
		{func(c context.Context, b []byte) (SniffResult, error) { return bittorrent.SniffUTP(b) }, false, net.Network_UDP},
	}}
	_, oldErr := oldChain.Sniff(ctx, short, net.Network_UDP)
	if oldErr != common.ErrNoClue {
		t.Fatalf("旧链对短包应返回 ErrNoClue（触发再等一轮），实际 %v", oldErr)
	}

	// 现在的嗅探链。这里手工复刻 NewSniffer 的 UDP 部分而不直接调它，
	// 是因为 NewSniffer 里的 fakedns 嗅探器需要完整的 xray 运行时上下文。
	_, newErr := newUDPSnifferChain().Sniff(ctx, short, net.Network_UDP)
	if newErr == common.ErrNoClue {
		t.Fatal("新链仍然返回 ErrNoClue，首包还是会白等最多 200ms")
	}
	t.Logf("旧链: %v（sniffer() 会再等一轮，最坏 +200ms）", oldErr)
	t.Logf("新链: %v（立即结束嗅探，无额外等待）", newErr)
}

// 常见 UDP 流量不应触发任何等待。
func TestCommonUDPPayloadsSniffWithoutStalling(t *testing.T) {
	ctx := context.Background()
	dnsQuery := []byte{
		0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x03, 'w', 'w', 'w', 0x06, 'g', 'o', 'o', 'g', 'l', 'e', 0x03, 'c', 'o', 'm', 0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	stun := append([]byte{0x00, 0x01, 0x00, 0x00, 0x21, 0x12, 0xA4, 0x42}, make([]byte, 12)...)
	ntp := make([]byte, 48)
	ntp[0] = 0x1B

	for name, payload := range map[string][]byte{
		"DNS 查询": dnsQuery,
		"STUN":   stun,
		"NTP":    ntp,
	} {
		if _, err := newUDPSnifferChain().Sniff(ctx, payload, net.Network_UDP); err == common.ErrNoClue {
			t.Errorf("%s 触发了 ErrNoClue，会让首包多等一轮", name)
		}
	}
}
