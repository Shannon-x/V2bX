package limiter

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	log "github.com/sirupsen/logrus"
)

// PortRange represents a min-max port range for port-based blocking
type PortRange struct {
	Min int
	Max int
}

func (l *Limiter) CheckDomainRule(destination string) (reject bool) {
	l.RuleMu.RLock()
	defer l.RuleMu.RUnlock()
	for i := range l.DomainRules {
		if l.DomainRules[i].MatchString(destination) {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) CheckProtocolRule(protocol string) (reject bool) {
	l.RuleMu.RLock()
	defer l.RuleMu.RUnlock()
	for i := range l.ProtocolRules {
		if l.ProtocolRules[i] == protocol {
			reject = true
			break
		}
	}
	return
}

// CheckIPRule checks if the destination IP matches any blocked IP/CIDR
func (l *Limiter) CheckIPRule(ipStr string) (reject bool) {
	l.RuleMu.RLock()
	defer l.RuleMu.RUnlock()
	if len(l.IPRules) == 0 {
		return false
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range l.IPRules {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckPortRule checks if the destination port matches any blocked port/range
func (l *Limiter) CheckPortRule(port int) (reject bool) {
	l.RuleMu.RLock()
	defer l.RuleMu.RUnlock()
	for _, pr := range l.PortRules {
		if port >= pr.Min && port <= pr.Max {
			return true
		}
	}
	return false
}

// CheckRouteRule checks if destination matches a route rule and returns the target outbound tag.
// Uses Xray's native router engine which supports geosite:, domain:, full:, regexp:, geoip:, IP/CIDR.
// Returns empty string if no rule matches.
func (l *Limiter) CheckRouteRule(destDomain string, destIP string) string {
	l.RuleMu.RLock()
	defer l.RuleMu.RUnlock()
	if l.RouteMatcher != nil {
		return l.RouteMatcher.match(destDomain, destIP)
	}
	return ""
}

func (l *Limiter) UpdateRule(rule *panel.Rules) error {
	l.RuleMu.Lock()
	defer l.RuleMu.Unlock()

	// Domain rules (block)
	l.DomainRules = make([]*regexp.Regexp, 0, len(rule.Regexp))
	for i := range rule.Regexp {
		re, err := regexp.Compile(rule.Regexp[i])
		if err != nil {
			return fmt.Errorf("compile rule regexp %q error: %w", rule.Regexp[i], err)
		}
		l.DomainRules = append(l.DomainRules, re)
	}
	// Protocol rules
	l.ProtocolRules = rule.Protocol

	// IP rules (block_ip)
	l.IPRules = make([]*net.IPNet, 0, len(rule.InboundIP))
	for _, ipStr := range rule.InboundIP {
		if !strings.Contains(ipStr, "/") {
			// Single IP, convert to /32 or /128
			ip := net.ParseIP(ipStr)
			if ip == nil {
				continue
			}
			if ip.To4() != nil {
				ipStr = ipStr + "/32"
			} else {
				ipStr = ipStr + "/128"
			}
		}
		_, cidr, err := net.ParseCIDR(ipStr)
		if err != nil {
			continue
		}
		l.IPRules = append(l.IPRules, cidr)
	}

	// Port rules (block_port)
	l.PortRules = make([]PortRange, 0, len(rule.InboundPort))
	for _, portStr := range rule.InboundPort {
		if strings.Contains(portStr, "-") {
			parts := strings.SplitN(portStr, "-", 2)
			min, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			max, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				l.PortRules = append(l.PortRules, PortRange{Min: min, Max: max})
			}
		} else {
			port, err := strconv.Atoi(strings.TrimSpace(portStr))
			if err == nil {
				l.PortRules = append(l.PortRules, PortRange{Min: port, Max: port})
			}
		}
	}

	// Route rules — build Xray-native matcher supporting geosite/geoip/domain/etc.
	// This replaces the old regexp-based matching that couldn't handle geosite: patterns.
	// Mirrors v2node's approach: panel match values are passed directly to Xray's router config.
	l.RouteRules = rule.RouteRules
	l.DefaultOutbound = rule.DefaultOut
	l.RouteMatcher = buildXrayRouteMatcher(rule.RouteRules, rule.DefaultOut)
	if l.RouteMatcher != nil {
		log.Infof("Route matcher built with %d route rules", len(rule.RouteRules))
	}

	return nil
}
