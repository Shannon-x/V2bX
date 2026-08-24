package dispatcher

import (
	"time"

	"github.com/InazumaV/V2bX/common/bittorrent"
	"github.com/InazumaV/V2bX/common/throttle"
	"github.com/xtls/xray-core/common/buf"
)

// btDropLog 给 BT 丢包日志限流：一个跑 DHT 的客户端每秒能产生上百个包，
// 不限流会瞬间刷爆日志并拖慢数据面。每个 inbound tag 最多 1 条 / 30 秒。
var btDropLog = throttle.New(30 * time.Second)

// 逐包过滤出站 UDP 中的 BitTorrent 报文。
//
// 为什么单靠嗅探器不够：xray 的 UDP 入站（shadowsocks / trojan / socks）为每个
// 客户端会话只建立一条 link —— transport/internet/udp/dispatcher.go 的
// getInboundRay 完全忽略 dest 参数，命中缓存就直接复用；shadowsocks/server.go
// 在 cone 模式下还会把 dest 钉死在首包目标上。于是「嗅探 + 路由 + limiter 判定」
// 在一个 UDP 会话里只发生一次，判定对象是第一个包。
//
// BT 客户端的首包几乎总是 DNS 查询或 QUIC 握手，会话因此被判成 dns/quic，
// 随后成千上万个发往不同 peer 的 DHT 包沿着同一条 link 出站，
// 既不会再被嗅探，也不会再过任何一条 route.json 规则。这正是
// route.json 里写了 {"protocol":["bittorrent"]} 却依然收到 DHT 投诉的原因。
//
// 这里在 link 的读取侧插一层，对每个 UDP 包复用 bittorrent.SniffUDP 判定，
// 命中就丢弃该包并放行其余流量 —— 不掐断整条会话，避免连带打断用户的 DNS。
type btUDPFilter struct {
	reader buf.Reader
	onHit  func()
}

func (f *btUDPFilter) filter(mb buf.MultiBuffer) buf.MultiBuffer {
	if len(mb) == 0 {
		return mb
	}
	kept := mb[:0]
	for _, b := range mb {
		if b != nil && !b.IsEmpty() {
			if bittorrent.SniffUDP(b.Bytes()) {
				if f.onHit != nil {
					f.onHit()
				}
				b.Release()
				continue
			}
		}
		kept = append(kept, b)
	}
	return kept
}

func (f *btUDPFilter) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := f.reader.ReadMultiBuffer()
	if err != nil {
		return mb, err
	}
	return f.filter(mb), nil
}

// btUDPFilterWithTimeout 在内层 reader 支持超时读取时保留该能力。
// vless / shadowsocks_2022 这类出站会对 link.Reader 做
// buf.TimeoutReader 断言，丢掉这个接口会让链式出站退化。
type btUDPFilterWithTimeout struct {
	btUDPFilter
	timeoutReader buf.TimeoutReader
}

func (f *btUDPFilterWithTimeout) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	mb, err := f.timeoutReader.ReadMultiBufferTimeout(d)
	if err != nil {
		return mb, err
	}
	return f.filter(mb), nil
}

// newBTUDPFilterReader 包装 reader；内层实现 buf.TimeoutReader 时同样对外暴露该接口。
func newBTUDPFilterReader(r buf.Reader, onHit func()) buf.Reader {
	base := btUDPFilter{reader: r, onHit: onHit}
	if tr, ok := r.(buf.TimeoutReader); ok {
		return &btUDPFilterWithTimeout{btUDPFilter: base, timeoutReader: tr}
	}
	return &base
}
