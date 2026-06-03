package sing

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/common/rate"

	"github.com/InazumaV/V2bX/limiter"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	N "github.com/sagernet/sing/common/network"
)

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
		log.Error("[", m.Inbound, "] ", "Limited ", m.User, " by ip or conn")
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
				m.User,
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
					m.User,
					ipStr))
				conn.Close()
				return conn
			}
		}
		// Block port rules
		if l.CheckPortRule(int(m.Destination.Port)) {
			log.Error(fmt.Sprintf(
				"User %s access port %d reject by rule",
				m.User,
				m.Destination.Port))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					m.User,
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	// W2.5 / audit #23 #57: LoadOrStore eliminates the Load+Store race that
	// allowed two concurrent first-connection events on the same inbound to
	// each construct a TrafficCounter and have one orphaned (its recorded
	// traffic invisible to subsequent reads).
	actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
	t := actual.(*counter.TrafficCounter)
	conn = counter.NewConnCounter(conn, t.GetCounter(m.User))
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
		log.Error("[", m.Inbound, "] ", "Limited ", m.User, " by ip or conn")
		return conn
	} else if db != nil {
		//conn = rate.NewPacketConnCounter(conn, db)
	}
	if l != nil {
		destStr := m.Destination.AddrString()
		protocol := m.Destination.Network()
		if l.CheckDomainRule(destStr) {
			log.Error(fmt.Sprintf(
				"User %s access domain %s reject by rule",
				m.User,
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
					m.User,
					ipStr))
				conn.Close()
				return conn
			}
		}
		// Block port rules
		if l.CheckPortRule(int(m.Destination.Port)) {
			log.Error(fmt.Sprintf(
				"User %s access port %d reject by rule",
				m.User,
				m.Destination.Port))
			conn.Close()
			return conn
		}
		if len(protocol) != 0 {
			if l.CheckProtocolRule(protocol) {
				log.Error(fmt.Sprintf(
					"User %s access protocol %s reject by rule",
					m.User,
					protocol))
				conn.Close()
				return conn
			}
		}
	}
	// W2.5 / audit #23 #57: same LoadOrStore fix as for TCP.
	actual, _ := h.counter.LoadOrStore(m.Inbound, counter.NewTrafficCounter())
	t := actual.(*counter.TrafficCounter)
	conn = counter.NewPacketConnCounter(conn, t.GetCounter(m.User))
	return conn
}
