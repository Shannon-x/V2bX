package sing

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/common/rate"
	"github.com/InazumaV/V2bX/common/throttle"

	"github.com/InazumaV/V2bX/limiter"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	N "github.com/sagernet/sing/common/network"
)

// safeUserField escapes panel-supplied identifiers (typically the user UUID)
// before they appear in a log line. W4.6 / audit #58: a UUID containing
// newline / ANSI / control bytes could otherwise spoof an entire log line.
// strconv.Quote escapes the lot and adds surrounding quotes so the field
// is also unambiguous in log parsing.
func safeUserField(s string) string {
	return strconv.Quote(s)
}

// btDropLog 给 BT 丢包日志限流：一个跑 DHT 的客户端每秒能产生上百个包，
// 不限流会瞬间刷爆日志并拖慢数据面。每个 inbound 最多 1 条 / 30 秒。
var btDropLog = throttle.New(30 * time.Second)

var _ adapter.ConnectionTracker = (*HookServer)(nil)

type HookServer struct {
	counter sync.Map //map[string]*counter.TrafficCounter
}

func (h *HookServer) ModeList() []string {
	return nil
}

func (h *HookServer) RoutedConnection(_ context.Context, conn net.Conn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) (retConn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("[", m.Inbound, "] panic in RoutedConnection: ", r)
			retConn = conn
		}
	}()
	l, err := limiter.GetLimiter(m.Inbound)
	if err != nil {
		log.Warn("get limiter for ", m.Inbound, " error: ", err)
		return conn
	}
	taguuid := format.UserTag(m.Inbound, m.User)
	ip := m.Source.Addr.String()
	if db, r := l.CheckLimit(taguuid, ip, true, true); r {
		conn.Close()
		log.Error("[", m.Inbound, "] ", "Limited ", safeUserField(m.User), " by ip or conn")
		return conn
	} else if db != nil {
		conn = rate.NewConnRateLimiter(conn, db)
	}
	if l != nil {
		destStr := m.Destination.AddrString()
		protocol := m.Protocol
		if l.CheckDomainRule(destStr) {
			log.Error(fmt.Sprintf(
				"User %s access domain %s reject by rule",
				safeUserField(m.User),
				destStr))
			conn.Close()
			return conn
		}
		// Block IP rules
		if m.Destination.Addr.IsValid() && !m.Destination.IsFqdn() {
			ipStr := m.Destination.Addr.String()
			if l.CheckIPRule(ipStr) {
				log.Error(fmt.Sprintf(
					"User %s access IP %s reject by rule",
					safeUserField(m.User),
					ipStr))
				conn.Close()
				return conn
			}
		}
		// Block port rules
		if l.CheckPortRule(int(m.Destination.Port)) {
			log.Error(fmt.Sprintf(
				"User %s access port %d reject by rule",
				safeUserField(m.User),
				m.Destination.Port))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					safeUserField(m.User),
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	// W2.5 / W6 / audit #23 #57 / B1: Load-first; LoadOrStore alloc only on
	// cold miss. Eliminates one heap alloc per connection in steady state.
	var t *counter.TrafficCounter
	if v, ok := h.counter.Load(m.Inbound); ok {
		t = v.(*counter.TrafficCounter)
	} else {
		actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
		t = actual.(*counter.TrafficCounter)
	}
	// W6 fix-up: pass (parent, uuid) so the conn wrapper can MarkDirty on
	// every byte movement — without this, sing users never appear in
	// IterateDirty and their traffic is silently dropped by the report task.
	conn = counter.NewConnCounter(conn, t, m.User)
	return conn
}

func (h *HookServer) RoutedPacketConnection(_ context.Context, conn N.PacketConn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) (retConn N.PacketConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("[", m.Inbound, "] panic in RoutedPacketConnection: ", r)
			retConn = conn
		}
	}()
	l, err := limiter.GetLimiter(m.Inbound)
	if err != nil {
		log.Warn("get limiter for ", m.Inbound, " error: ", err)
		return conn
	}
	ip := m.Source.Addr.String()
	taguuid := format.UserTag(m.Inbound, m.User)
	if db, r := l.CheckLimit(taguuid, ip, false, false); r {
		conn.Close()
		log.Error("[", m.Inbound, "] ", "Limited ", safeUserField(m.User), " by ip or conn")
		return conn
	} else if db != nil {
		//conn = rate.NewPacketConnCounter(conn, db)
	}
	if l != nil {
		destStr := m.Destination.AddrString()
		// 这里必须用嗅探结果 m.Protocol，而不是 m.Destination.Network()。
		// sing 的 M.Socksaddr.Network() 是个常量，恒返回字面量 "socks"
		// （sing/common/metadata/addr.go:18），所以旧写法等于把每条 UDP 连接的
		// 协议名都报成 "socks"：面板下发的 protocol 审计规则（典型值 bittorrent）
		// 在 UDP 路径上永远不可能命中，而 BT 的 DHT/uTP/UDP Tracker 恰恰全在 UDP 上。
		protocol := m.Protocol
		if l.CheckDomainRule(destStr) {
			log.Error(fmt.Sprintf(
				"User %s access domain %s reject by rule",
				safeUserField(m.User),
				destStr))
			conn.Close()
			return conn
		}
		// Block IP rules
		if m.Destination.Addr.IsValid() && !m.Destination.IsFqdn() {
			ipStr := m.Destination.Addr.String()
			if l.CheckIPRule(ipStr) {
				log.Error(fmt.Sprintf(
					"User %s access IP %s reject by rule",
					safeUserField(m.User),
					ipStr))
				conn.Close()
				return conn
			}
		}
		// Block port rules
		if l.CheckPortRule(int(m.Destination.Port)) {
			log.Error(fmt.Sprintf(
				"User %s access port %d reject by rule",
				safeUserField(m.User),
				m.Destination.Port))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					safeUserField(m.User),
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	// 逐包 BitTorrent 过滤。sing-box 自带的 bittorrent 嗅探器没有 DHT/KRPC，
	// 只靠路由规则挡不住机房投诉里的 DHT 流量，详见 bittorrent_filter.go。
	// 必须在计数器之前包装，否则会被 sing 的 unwrap 机制连同计数器一起剥掉。
	if l != nil && l.BlockBittorrentUDP() {
		user := m.User
		inbound := m.Inbound
		conn = newBTPacketFilter(conn, func() {
			if btDropLog.Allow(inbound) {
				log.Error(fmt.Sprintf(
					"User %s outbound bittorrent UDP packet dropped by rule",
					safeUserField(user)))
			}
		})
	}
	// W2.5 / W6 / audit #23 #57 / B1: Load-first; LoadOrStore alloc only on
	// cold miss (same as TCP path).
	var t *counter.TrafficCounter
	if v, ok := h.counter.Load(m.Inbound); ok {
		t = v.(*counter.TrafficCounter)
	} else {
		actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
		t = actual.(*counter.TrafficCounter)
	}
	// W6 fix-up: see TCP path comment above — same reason.
	conn = counter.NewPacketConnCounter(conn, t, m.User)
	return conn
}
