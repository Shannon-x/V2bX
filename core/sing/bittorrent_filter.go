package sing

import (
	"github.com/InazumaV/V2bX/common/bittorrent"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// btPacketFilter 逐包丢弃客户端发往外网的 BitTorrent UDP 报文。
//
// 为什么 sing-box 自己的路由规则不够：sing-box 的 bittorrent 嗅探器只注册了
// BitTorrent(TCP 握手)、UTP、UDPTracker 三个（route/rule/rule_action.go 的
// RuleActionSniff.build），**没有 DHT/KRPC 嗅探器**。而机房告警抓的正是 DHT。
// 所以哪怕把 {"action":"sniff","sniffer":["bittorrent"]} 和 protocol 规则配全，
// DHT 依然会原样出网。
//
// 这一层挂在入站 PacketConn 的读取侧，即客户端 → 外网方向，逐包判定。
//
// 注意包装顺序：必须在 counter.NewPacketConnCounter 之前套上。
// sing 的 UnwrapCountPacketReader 会把计数器剥掉并把它的 CountFunc 收走
// （sing/common/network/counter.go:74-93），剥到本类型时因为我们不实现
// ReaderWithUpstream 而停下，于是流量统计不受影响，过滤层也留在链路里。
//
// 零拷贝快路径不受影响：本类型实现了 N.PacketReadWaitCreator，
// 把底层 conn 的 PacketReadWaiter 包一层后原样交出去
// （sing/common/bufio/copy_direct.go:100 的 copyPacketWaitWithPool 会走到它），
// 所以 CopyPacket 依旧走 WaitReadPacket 分支，不会退化成每包一次缓冲区分配。
// 净增开销就是每个包一次 bittorrent.SniffUDP —— 实测正常流量约 2.7ns、零分配。
//
// 被丢弃的 BT 包不计入用户流量：它们从未离开本机，不应计费。
type btPacketFilter struct {
	N.PacketConn
	onHit func()
}

func newBTPacketFilter(conn N.PacketConn, onHit func()) N.PacketConn {
	return &btPacketFilter{PacketConn: conn, onHit: onHit}
}

func (f *btPacketFilter) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	for {
		destination, err := f.PacketConn.ReadPacket(buffer)
		if err != nil {
			return destination, err
		}
		if buffer.Len() == 0 || !bittorrent.SniffUDP(buffer.Bytes()) {
			return destination, nil
		}
		if f.onHit != nil {
			f.onHit()
		}
		// 丢掉这个包继续读下一个：只掐 BT，不掐整条会话，
		// 用户在同一个 UDP 会话里的 DNS / QUIC 不受影响。
		buffer.Reset()
	}
}

var _ N.PacketReadWaitCreator = (*btPacketFilter)(nil)

// CreateReadWaiter 把底层 conn 的零拷贝读等待器包一层再交出去，
// 保住 sing 的 WaitReadPacket 快路径。底层不支持时返回 false，
// CopyPacket 会自然回落到 ReadPacket 分支，行为一致。
func (f *btPacketFilter) CreateReadWaiter() (N.PacketReadWaiter, bool) {
	waiter, created := bufio.CreatePacketReadWaiter(f.PacketConn)
	if !created {
		return nil, false
	}
	return &btPacketReadWaiter{PacketReadWaiter: waiter, onHit: f.onHit}, true
}

type btPacketReadWaiter struct {
	N.PacketReadWaiter
	onHit func()
}

func (w *btPacketReadWaiter) WaitReadPacket() (*buf.Buffer, M.Socksaddr, error) {
	for {
		buffer, destination, err := w.PacketReadWaiter.WaitReadPacket()
		if err != nil {
			return buffer, destination, err
		}
		if buffer == nil || buffer.Len() == 0 || !bittorrent.SniffUDP(buffer.Bytes()) {
			return buffer, destination, nil
		}
		if w.onHit != nil {
			w.onHit()
		}
		// 调用方（copyPacketWaitWithPool）拿到 buffer 后本来就负责释放，
		// 这里丢包即代它释放，然后继续等下一个。
		buffer.Release()
	}
}
