package xray

import (
	"fmt"
	"strings"

	"encoding/json"

	conf2 "github.com/InazumaV/V2bX/conf"
	log "github.com/sirupsen/logrus"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

// BuildOutbound build freedom outbund config for addoutbound
func buildOutbound(config *conf2.Options, tag string) (*core.OutboundHandlerConfig, error) {
	outboundDetourConfig := &conf.OutboundDetourConfig{}
	outboundDetourConfig.Protocol = "freedom"
	outboundDetourConfig.Tag = tag

	// Build Send IP address
	if config.SendIP != "" {
		outboundDetourConfig.SendThrough = &config.SendIP
	}

	// Freedom resolves domains according to streamSettings.sockopt.domainStrategy.
	// The Freedom "settings.domainStrategy" field is deprecated in Xray-core:
	// it is still migrated automatically, with a warning on every build, and
	// is due to be removed.
	if config.XrayOptions.EnableDNS {
		domainStrategy := config.XrayOptions.DNSType
		if domainStrategy == "" {
			domainStrategy = "UseIP"
		}
		setSockoptDomainStrategy(outboundDetourConfig, domainStrategy)
	}
	return outboundDetourConfig.Build()
}

// buildCustomOutbound builds an outbound from raw JSON config provided by the panel's
// default_out route action_value. The tag is overridden to match the node tag so that
// traffic for this node is routed through the custom outbound.
func buildCustomOutbound(rawJSON string, tag string) (*core.OutboundHandlerConfig, error) {
	outboundDetourConfig := &conf.OutboundDetourConfig{}
	if err := json.Unmarshal([]byte(rawJSON), outboundDetourConfig); err != nil {
		return nil, fmt.Errorf("unmarshal custom outbound config error: %s", err)
	}
	// Override tag to match the node tag
	outboundDetourConfig.Tag = tag
	migrateLegacyFreedomDomainStrategy(outboundDetourConfig)
	return outboundDetourConfig.Build()
}

// migrateLegacyFreedomDomainStrategy moves a Freedom outbound's legacy
// "settings.domainStrategy" / "settings.targetStrategy" into
// "streamSettings.sockopt.domainStrategy" and drops it from settings.
//
// Older configs, including the custom_outbound.json written by earlier install
// scripts (its IPv4_out is the target of the route "final" rule), use:
//
//	{"protocol": "freedom", "settings": {"domainStrategy": "UseIPv4v6"}}
//
// Xray-core still migrates this itself, but logs a deprecation warning every
// time the outbound is built and says the field will be removed. Once it is
// removed, such files would silently fall back to AsIs (system resolver, no
// Xray DNS, no IPv4/IPv6 preference). Doing the move here keeps existing files
// working without edits. An explicit sockopt.domainStrategy wins.
func migrateLegacyFreedomDomainStrategy(c *conf.OutboundDetourConfig) {
	if c == nil || c.Settings == nil {
		return
	}
	switch strings.ToLower(c.Protocol) {
	case "freedom", "direct":
	default:
		return
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(*c.Settings, &settings); err != nil {
		// Leave the error to Build(), which reports it with full context.
		return
	}
	var domainStrategy, targetStrategy string
	found := false
	for key, raw := range settings {
		var dst *string
		switch strings.ToLower(key) {
		case "domainstrategy":
			dst = &domainStrategy
		case "targetstrategy":
			dst = &targetStrategy
		default:
			continue
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return
		}
		delete(settings, key)
		found = true
	}
	if !found {
		return
	}
	strategy := targetStrategy
	if strategy == "" {
		strategy = domainStrategy
	}
	cleaned, err := json.Marshal(settings)
	if err != nil {
		return
	}
	raw := json.RawMessage(cleaned)
	c.Settings = &raw
	if strategy == "" || strings.EqualFold(strategy, "AsIs") {
		return
	}
	if c.StreamSetting != nil && c.StreamSetting.SocketSettings != nil &&
		c.StreamSetting.SocketSettings.DomainStrategy != "" {
		return
	}
	setSockoptDomainStrategy(c, strategy)
	log.WithFields(log.Fields{
		"tag":      c.Tag,
		"strategy": strategy,
	}).Debug("Moved Freedom settings.domainStrategy to streamSettings.sockopt.domainStrategy")
}

func setSockoptDomainStrategy(c *conf.OutboundDetourConfig, strategy string) {
	if c.StreamSetting == nil {
		c.StreamSetting = &conf.StreamConfig{}
	}
	if c.StreamSetting.SocketSettings == nil {
		c.StreamSetting.SocketSettings = &conf.SocketConfig{}
	}
	c.StreamSetting.SocketSettings.DomainStrategy = strategy
}
