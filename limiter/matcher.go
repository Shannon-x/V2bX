package limiter

import (
	"context"
	"encoding/json"
	gonet "net"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	log "github.com/sirupsen/logrus"
	"github.com/xtls/xray-core/app/router"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/features/routing"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// xrayRouteMatcher wraps an Xray router instance to match route/route_ip rules
// using Xray's native domain/IP matching engine (geosite, geoip, domain:, etc.).
// This mirrors v2node's approach of passing routes directly to Xray's RouterConfig.
type xrayRouteMatcher struct {
	router *router.Router
}

// buildXrayRouteMatcher builds a full Xray router from panel RouteRules,
// exactly like v2node's GetCustomConfig() builds coreRouterConfig.
//
// Each rule becomes a native Xray routing rule with:
//   - "domain" type rules: domain match → outboundTag
//   - "ip" type rules:     ip match    → outboundTag
//
// This supports ALL Xray-native match patterns:
//   - geosite:category-ai-!cn  (predefined domain list from geosite.dat)
//   - domain:example.com       (subdomain match: example.com + *.example.com)
//   - full:example.com         (exact match)
//   - keyword match            (bare string, substring)
//   - regexp:pattern           (regex)
//   - geoip:cn / geoip:!cn     (predefined IP list from geoip.dat)
//   - 1.2.3.0/24               (CIDR)
//   - 1.2.3.4                  (single IP)
func buildXrayRouteMatcher(rules []panel.RouteRule, defaultOut string) *xrayRouteMatcher {
	if len(rules) == 0 && defaultOut == "" {
		return nil
	}

	domainStrategy := "AsIs"
	coreRouterConfig := &coreConf.RouterConfig{
		RuleList:       make([]json.RawMessage, 0),
		DomainStrategy: &domainStrategy,
	}

	for i, rule := range rules {
		if len(rule.Match) == 0 {
			continue
		}

		var ruleJSON map[string]interface{}

		switch rule.Type {
		case "domain":
			// v2node custom.go: {"domain": route.Match, "outboundTag": outbound.Tag}
			ruleJSON = map[string]interface{}{
				"type":        "field",
				"domain":      rule.Match,
				"outboundTag": rule.OutboundTag,
			}
		case "ip":
			// v2node custom.go: {"ip": route.Match, "outboundTag": outbound.Tag}
			ruleJSON = map[string]interface{}{
				"type":        "field",
				"ip":          rule.Match,
				"outboundTag": rule.OutboundTag,
			}
		default:
			continue
		}

		rawRule, err := json.Marshal(ruleJSON)
		if err != nil {
			log.Warnf("Failed to marshal route rule %d: %v", i, err)
			continue
		}
		coreRouterConfig.RuleList = append(coreRouterConfig.RuleList, rawRule)
	}

	// default_out: match all tcp,udp traffic (like v2node's default_out handling)
	if defaultOut != "" {
		ruleJSON := map[string]interface{}{
			"type":        "field",
			"network":     "tcp,udp",
			"outboundTag": defaultOut,
		}
		rawRule, err := json.Marshal(ruleJSON)
		if err == nil {
			coreRouterConfig.RuleList = append(coreRouterConfig.RuleList, rawRule)
		}
	}

	if len(coreRouterConfig.RuleList) == 0 {
		return nil
	}

	// Build() parses all domain/IP patterns, loads geosite.dat / geoip.dat as needed
	routerConfig, err := coreRouterConfig.Build()
	if err != nil {
		log.Warnf("Failed to build Xray router config for route rules: %v", err)
		return nil
	}

	// Create a standalone router instance for matching only (no dns/ohm needed)
	r := &router.Router{}
	if err := r.Init(context.Background(), routerConfig, nil, nil, nil); err != nil {
		log.Warnf("Failed to init Xray router for route rules: %v", err)
		return nil
	}

	log.Infof("Built Xray route matcher with %d rules", len(coreRouterConfig.RuleList))

	return &xrayRouteMatcher{
		router: r,
	}
}

// match checks both domain and IP against route rules.
// Returns the outbound tag if matched, empty string otherwise.
func (m *xrayRouteMatcher) match(domain, ip string) string {
	if m == nil || m.router == nil {
		return ""
	}

	ctx := &simpleRoutingContext{}

	// Set domain if available
	if domain != "" {
		ctx.targetDomain = strings.ToLower(domain)
	}

	// Set target IP if available
	if ip != "" {
		if parsed := gonet.ParseIP(ip); parsed != nil {
			ctx.targetIPs = []xnet.IP{xnet.IP(parsed)}
		}
	}

	route, err := m.router.PickRoute(ctx)
	if err != nil {
		return ""
	}
	return route.GetOutboundTag()
}

// simpleRoutingContext implements routing.Context for the Xray router to query.
// Only provides target domain / target IPs for route rule matching.
type simpleRoutingContext struct {
	targetDomain string
	targetIPs    []xnet.IP
}

func (c *simpleRoutingContext) GetInboundTag() string            { return "" }
func (c *simpleRoutingContext) GetSourceIPs() []xnet.IP          { return nil }
func (c *simpleRoutingContext) GetSourcePort() xnet.Port         { return 0 }
func (c *simpleRoutingContext) GetTargetIPs() []xnet.IP          { return c.targetIPs }
func (c *simpleRoutingContext) GetTargetPort() xnet.Port         { return 0 }
func (c *simpleRoutingContext) GetLocalIPs() []xnet.IP           { return nil }
func (c *simpleRoutingContext) GetLocalPort() xnet.Port          { return 0 }
func (c *simpleRoutingContext) GetTargetDomain() string          { return c.targetDomain }
func (c *simpleRoutingContext) GetNetwork() xnet.Network         { return xnet.Network_TCP }
func (c *simpleRoutingContext) GetProtocol() string              { return "" }
func (c *simpleRoutingContext) GetUser() string                  { return "" }
func (c *simpleRoutingContext) GetVlessRoute() xnet.Port         { return 0 }
func (c *simpleRoutingContext) GetAttributes() map[string]string { return nil }
func (c *simpleRoutingContext) GetSkipDNSResolve() bool          { return true }

// Compile-time check: simpleRoutingContext must implement routing.Context
var _ routing.Context = (*simpleRoutingContext)(nil)
