package xray

import (
	"fmt"

	"encoding/json"

	conf2 "github.com/InazumaV/V2bX/conf"
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

	// Freedom Protocol setting
	var domainStrategy = "Asis"
	if config.XrayOptions.EnableDNS {
		if config.XrayOptions.DNSType != "" {
			domainStrategy = config.XrayOptions.DNSType
		} else {
			domainStrategy = "UseIP"
		}
	}
	proxySetting := &conf.FreedomConfig{
		DomainStrategy: domainStrategy,
	}
	var setting json.RawMessage
	setting, err := json.Marshal(proxySetting)
	if err != nil {
		return nil, fmt.Errorf("marshal proxy config error: %s", err)
	}
	outboundDetourConfig.Settings = &setting
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
	return outboundDetourConfig.Build()
}
