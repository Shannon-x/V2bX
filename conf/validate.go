package conf

import (
	"fmt"
	"strings"
)

// Validate checks configuration for common mistakes and returns warnings/errors.
func (c *Conf) Validate() []string {
	var warnings []string

	if len(c.NodeConfig) == 0 {
		warnings = append(warnings, "no nodes configured")
	}
	if len(c.CoresConfig) == 0 {
		warnings = append(warnings, "no cores configured")
	}

	for i, n := range c.NodeConfig {
		prefix := fmt.Sprintf("Nodes[%d]", i)
		if n.ApiConfig.APIHost == "" {
			warnings = append(warnings, prefix+": ApiHost is empty")
		}
		if n.ApiConfig.Key == "" {
			warnings = append(warnings, prefix+": ApiKey is empty")
		}
		if n.ApiConfig.NodeID <= 0 {
			warnings = append(warnings, prefix+": NodeID must be positive")
		}
		if n.ApiConfig.NodeType == "" {
			warnings = append(warnings, prefix+": NodeType is empty")
		}
		if n.ApiConfig.Timeout <= 0 {
			n.ApiConfig.Timeout = 30
			warnings = append(warnings, prefix+": Timeout not set, using default 30s")
		}
		if n.Options.LimitConfig.SpeedLimit < 0 {
			warnings = append(warnings, prefix+": SpeedLimit cannot be negative")
		}
		if n.Options.LimitConfig.EnableDynamicSpeedLimit && n.Options.LimitConfig.DynamicSpeedLimitConfig == nil {
			warnings = append(warnings, prefix+": EnableDynamicSpeedLimit is true but DynamicSpeedLimitConfig is nil")
		}
		if n.Options.LimitConfig.DynamicSpeedLimitConfig != nil {
			dsc := n.Options.LimitConfig.DynamicSpeedLimitConfig
			if dsc.Periodic <= 0 {
				warnings = append(warnings, prefix+": DynamicSpeedLimit Periodic must be positive")
			}
			if dsc.Traffic <= 0 {
				warnings = append(warnings, prefix+": DynamicSpeedLimit Traffic threshold must be positive")
			}
		}
		if !strings.HasPrefix(n.ApiConfig.APIHost, "http://") && !strings.HasPrefix(n.ApiConfig.APIHost, "https://") {
			warnings = append(warnings, prefix+": ApiHost should start with http:// or https://")
		}
	}

	return warnings
}
