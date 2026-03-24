package limiter

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
)

// PortRange represents a min-max port range for port-based blocking
type PortRange struct {
	Min int
	Max int
}

func (l *Limiter) CheckDomainRule(destination string) (reject bool) {
	for i := range l.DomainRules {
		if l.DomainRules[i].MatchString(destination) {
			reject = true
			break
		}
	}
	return
}

func (l *Limiter) CheckProtocolRule(protocol string) (reject bool) {
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
	for _, pr := range l.PortRules {
		if port >= pr.Min && port <= pr.Max {
			return true
		}
	}
	return false
}

// CheckRouteRule checks if destination matches a route rule and returns the target outbound tag.
// Returns empty string if no rule matches.
func (l *Limiter) CheckRouteRule(destDomain string, destIP string) string {
	for i, rule := range l.RouteRules {
		switch rule.Type {
		case "domain":
			if destDomain != "" && i < len(l.RouteDomainRe) && l.RouteDomainRe[i] != nil {
				if l.RouteDomainRe[i].MatchString(destDomain) {
					return rule.OutboundTag
				}
			}
		case "ip":
			if destIP != "" {
				ip := net.ParseIP(destIP)
				if ip == nil {
					continue
				}
				for _, m := range rule.Match {
					_, cidr, err := net.ParseCIDR(m)
					if err != nil {
						// Try as single IP
						if net.ParseIP(m) != nil && m == destIP {
							return rule.OutboundTag
						}
						continue
					}
					if cidr.Contains(ip) {
						return rule.OutboundTag
					}
				}
			}
		}
	}
	return ""
}

func (l *Limiter) UpdateRule(rule *panel.Rules) error {
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

	// Route rules
	l.RouteRules = rule.RouteRules
	l.RouteDomainRe = make([]*regexp.Regexp, len(rule.RouteRules))
	for i, rr := range rule.RouteRules {
		if rr.Type == "domain" && len(rr.Match) > 0 {
			// Combine all match patterns into one regexp with OR
			combined := strings.Join(rr.Match, "|")
			re, err := regexp.Compile(combined)
			if err != nil {
				// fallback: try individual patterns
				continue
			}
			l.RouteDomainRe[i] = re
		}
	}

	// Default outbound
	l.DefaultOutbound = rule.DefaultOut

	return nil
}
