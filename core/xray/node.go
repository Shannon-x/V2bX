package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/core/xray/app/dispatcher"
	log "github.com/sirupsen/logrus"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// customOutboundDefaultAllowed gates the panel-supplied default_out JSON
// through the same CustomOutbound policy as the per-route custom outbounds.
// W6 review #3. Returns true only if custom outbound loading is enabled AND
// the default_out's protocol is permitted by the (opt-in) whitelist. On a
// parse failure it returns false (fall back to freedom) rather than loading
// an unverifiable outbound.
func customOutboundDefaultAllowed(config *conf.Options, rawJSON string) bool {
	var cfg *conf.CustomOutboundConfig
	if config != nil {
		cfg = config.CustomOutbound
	}
	if !conf.IsCustomOutboundEnabled(cfg) {
		log.Warn("default_out custom outbound rejected: CustomOutbound.Enabled=false")
		return false
	}
	probe := &coreConf.OutboundDetourConfig{}
	if err := json.Unmarshal([]byte(rawJSON), probe); err != nil {
		log.Warnf("default_out custom outbound rejected: unparseable JSON: %v", err)
		return false
	}
	proto := strings.ToLower(probe.Protocol)
	if !conf.IsCustomOutboundAllowed(cfg, proto) {
		log.Warnf("default_out custom outbound rejected: protocol %q not in CustomOutbound.AllowedProtocols=%v (falling back to freedom)",
			proto, func() []string {
				if cfg != nil {
					return cfg.AllowedProtocols
				}
				return nil
			}())
		return false
	}
	return true
}

type DNSConfig struct {
	Servers []interface{} `json:"servers"`
	Tag     string        `json:"tag"`
}

func (c *Xray) AddNode(tag string, info *panel.NodeInfo, config *conf.Options) error {
	// Use panel-provided threshold if available, otherwise use local config
	reportMin := config.ReportMinTraffic
	if info.NodeReportMinTraffic > 0 {
		reportMin = int64(info.NodeReportMinTraffic)
	}
	// W2.2: protect against concurrent GetUserTrafficSlice read.
	c.reportMu.Lock()
	c.nodeReportMinTrafficBytes[tag] = reportMin * 1024
	c.reportMu.Unlock()
	err := updateDNSConfig(info)
	if err != nil {
		return fmt.Errorf("build dns error: %s", err)
	}
	inboundConfig, err := buildInbound(config, info, tag)
	if err != nil {
		return fmt.Errorf("build inbound error: %s", err)
	}
	err = c.addInbound(inboundConfig)
	if err != nil {
		return fmt.Errorf("add inbound error: %s", err)
	}

	// Build outbound: use custom default_out if configured, otherwise freedom
	var outBoundConfig *core.OutboundHandlerConfig
	// W6 review #3: default_out is the highest-value egress sink (all traffic
	// for the tag), so it MUST pass the same CustomOutbound policy gate as the
	// per-route custom outbounds in AddNodeCustomOutbounds. Previously it was
	// loaded unconditionally here, letting a panel-pushed RawDefaultOut bypass
	// CustomOutbound.Enabled=false and the AllowedProtocols whitelist entirely.
	if info.Rules.RawDefaultOut != "" && customOutboundDefaultAllowed(config, info.Rules.RawDefaultOut) {
		// Panel provided a full custom outbound JSON (e.g. SOCKS proxy)
		outBoundConfig, err = buildCustomOutbound(info.Rules.RawDefaultOut, tag)
		if err != nil {
			log.WithFields(log.Fields{
				"tag": tag,
				"err": err,
			}).Warn("Failed to build custom default_out outbound, falling back to freedom")
			outBoundConfig, err = buildOutbound(config, tag)
			if err != nil {
				return fmt.Errorf("build outbound error: %s", err)
			}
		} else {
			log.WithField("tag", tag).Infof("Using custom default_out outbound: %s", info.Rules.DefaultOut)
		}
	} else if info.Rules.DefaultOut != "" {
		// Only a tag was provided, no raw config — log warning
		log.WithFields(log.Fields{
			"tag":         tag,
			"default_out": info.Rules.DefaultOut,
		}).Warn("default_out tag specified but no raw outbound config provided, using freedom")
		outBoundConfig, err = buildOutbound(config, tag)
		if err != nil {
			return fmt.Errorf("build outbound error: %s", err)
		}
	} else {
		outBoundConfig, err = buildOutbound(config, tag)
		if err != nil {
			return fmt.Errorf("build outbound error: %s", err)
		}
	}

	err = c.addOutbound(outBoundConfig)
	if err != nil {
		return fmt.Errorf("add outbound error: %s", err)
	}
	return nil
}

// UpdateDNS re-applies panel DNS-unlock routes on a hot config reload. See the
// vCore.Core interface doc. updateDNSConfig is idempotent — saveDnsConfig skips
// the write when the rendered DNS file is byte-identical — so calling this on
// every node-info change is cheap and cannot loop the config watcher.
func (c *Xray) UpdateDNS(tag string, info *panel.NodeInfo) error {
	return updateDNSConfig(info)
}

func (c *Xray) UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options) {
	reportMin := config.ReportMinTraffic
	if info.NodeReportMinTraffic > 0 {
		reportMin = int64(info.NodeReportMinTraffic)
	}
	// W2.2: see AddNode comment.
	c.reportMu.Lock()
	c.nodeReportMinTrafficBytes[tag] = reportMin * 1024
	c.reportMu.Unlock()
}

// AddNodeCustomOutbounds loads panel-supplied raw outbound JSON.
//
// W6 / audit #8: DEFAULT IS PERMISSIVE — every panel-pushed outbound is
// accepted (matches pre-W6 behavior; panel-driven routing is a core
// V2bX feature). The CustomOutboundConfig is an OPTIONAL hardening knob
// for multi-tenant deployers who don't fully trust the panel:
//
//   - omitted / nil                              → accept ALL (default)
//   - Enabled=false                              → reject ALL
//   - AllowedProtocols=["freedom","socks","..."] → accept only listed
//   - AllowedProtocols=["*"]                     → accept ALL (explicit)
//
// AUDIT_REPORT §3.3 documents the trust-boundary reminder: an
// unrestricted panel must be treated as node-root-equivalent.
func (c *Xray) AddNodeCustomOutbounds(info *panel.NodeInfo, opts *conf.Options) error {
	var cfg *conf.CustomOutboundConfig
	if opts != nil {
		cfg = opts.CustomOutbound
	}
	if !conf.IsCustomOutboundEnabled(cfg) {
		// Explicit opt-out — log at debug to avoid spamming when many
		// nodes have it off.
		log.Debugf("Custom outbound loading disabled via config; skipping panel outbounds")
		return nil
	}
	loadIfAllowed := func(rawJSON, contextLabel string) {
		if rawJSON == "" {
			return
		}
		outbound := &coreConf.OutboundDetourConfig{}
		if err := json.Unmarshal([]byte(rawJSON), outbound); err != nil {
			log.Errorf("Failed to unmarshal custom outbound JSON for %s: %v", contextLabel, err)
			return
		}
		// Look up the protocol BEFORE building so we can refuse cheaply.
		// OutboundDetourConfig.Protocol is the field that holds e.g.
		// "freedom" / "socks" / "vmess".
		proto := strings.ToLower(outbound.Protocol)
		if !conf.IsCustomOutboundAllowed(cfg, proto) {
			// Only reached when the deployer EXPLICITLY set a non-empty
			// AllowedProtocols that doesn't include this proto — default
			// config accepts everything.
			log.Warnf("Custom outbound %s rejected: protocol %q not in CustomOutbound.AllowedProtocols=%v. "+
				"Add %q to the list (or use [\"*\"]) to accept it.",
				contextLabel, proto, cfg.AllowedProtocols, proto)
			return
		}
		customConfig, err := outbound.Build()
		if err != nil {
			log.Errorf("Failed to build custom outbound for %s: %v", contextLabel, err)
			return
		}
		_ = c.removeOutbound(customConfig.Tag) // hot-reload tolerant
		if err = c.addOutbound(customConfig); err != nil {
			log.Errorf("Failed to inject custom outbound %s into Xray: %v", customConfig.Tag, err)
			return
		}
		log.Infof("Successfully injected custom outbound [%s] proto=%s (%s)", customConfig.Tag, proto, contextLabel)
	}

	for _, route := range info.Rules.RouteRules {
		loadIfAllowed(route.RawOutbound, "route:"+route.OutboundTag)
	}
	loadIfAllowed(info.Rules.RawDefaultOut, "default_out")
	return nil
}

func (c *Xray) addInbound(config *core.InboundHandlerConfig) error {
	rawHandler, err := core.CreateObject(c.Server, config)
	if err != nil {
		return err
	}
	handler, ok := rawHandler.(inbound.Handler)
	if !ok {
		return fmt.Errorf("not an InboundHandler: %s", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.ihm.AddHandler(ctx, handler); err != nil {
		return err
	}
	return nil
}

func (c *Xray) addOutbound(config *core.OutboundHandlerConfig) error {
	rawHandler, err := core.CreateObject(c.Server, config)
	if err != nil {
		return err
	}
	handler, ok := rawHandler.(outbound.Handler)
	if !ok {
		return fmt.Errorf("not an InboundHandler: %s", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.ohm.AddHandler(ctx, handler); err != nil {
		return err
	}
	return nil
}

func (c *Xray) DelNode(tag string) error {
	// W2.8 / audit #35: remove the inbound handler FIRST so xray stops
	// accepting new connections / dispatching them through getLink. Otherwise
	// concurrent fresh connections would re-populate LinkManagers under the
	// same tag after our Range+Delete, leaking goroutines and FDs.
	if err := c.removeInbound(tag); err != nil {
		return fmt.Errorf("remove in error: %s", err)
	}
	if err := c.removeOutbound(tag); err != nil {
		return fmt.Errorf("remove out error: %s", err)
	}

	// Now safe to clean up per-tag bookkeeping — no new entries will appear.
	c.dispatcher.Counter.Delete(tag)

	// W2.2: protected by reportMu.
	c.reportMu.Lock()
	delete(c.nodeReportMinTrafficBytes, tag)
	c.reportMu.Unlock()

	// 清理该节点所有用户的 LinkManagers
	// LinkManagers 的 key 格式是 format.UserTag(tag, uuid) = "tag|uuid"
	prefix := tag + "|"
	c.dispatcher.LinkManagers.Range(func(key, value interface{}) bool {
		email := key.(string)
		if strings.HasPrefix(email, prefix) {
			value.(*dispatcher.LinkManager).CloseAll()
			c.dispatcher.LinkManagers.Delete(key)
		}
		return true
	})
	return nil
}

func (c *Xray) removeInbound(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.ihm.RemoveHandler(ctx, tag)
}

func (c *Xray) removeOutbound(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := c.ohm.RemoveHandler(ctx, tag)
	return err
}
