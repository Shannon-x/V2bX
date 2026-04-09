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
	c.nodeReportMinTrafficBytes[tag] = reportMin * 1024
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
	if info.Rules.RawDefaultOut != "" {
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

func (c *Xray) UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options) {
	reportMin := config.ReportMinTraffic
	if info.NodeReportMinTraffic > 0 {
		reportMin = int64(info.NodeReportMinTraffic)
	}
	c.nodeReportMinTrafficBytes[tag] = reportMin * 1024
}

func (c *Xray) AddNodeCustomOutbounds(info *panel.NodeInfo) error {
	for _, route := range info.Rules.RouteRules {
		if route.RawOutbound != "" {
			// This is a custom JSON outbound. Try to parse it.
			outbound := &coreConf.OutboundDetourConfig{}
			err := json.Unmarshal([]byte(route.RawOutbound), outbound)
			if err != nil {
				log.Errorf("Failed to unmarshal custom outbound JSON for tag %s: %v", route.OutboundTag, err)
				continue
			}

			// Build the Xray OutboundHandlerConfig
			customConfig, err := outbound.Build()
			if err != nil {
				log.Errorf("Failed to build custom outbound for tag %s: %v", route.OutboundTag, err)
				continue
			}

			// Remove existing handler gracefully (for hot-reloading modified outbounds)
			_ = c.removeOutbound(customConfig.Tag)

			err = c.addOutbound(customConfig)
			if err != nil {
				log.Errorf("Failed to inject custom outbound %s into Xray: %v", customConfig.Tag, err)
			} else {
				log.Infof("Successfully injected custom JSON outbound: [%s]", customConfig.Tag)
			}
		}
	}

	if info.Rules.RawDefaultOut != "" {
		outbound := &coreConf.OutboundDetourConfig{}
		err := json.Unmarshal([]byte(info.Rules.RawDefaultOut), outbound)
		if err == nil {
			if customConfig, err := outbound.Build(); err == nil {
				_ = c.removeOutbound(customConfig.Tag)
				if err = c.addOutbound(customConfig); err != nil {
					log.Errorf("Failed to inject default_out %s into Xray: %v", customConfig.Tag, err)
				} else {
					log.Infof("Successfully injected custom default JSON outbound: [%s]", customConfig.Tag)
				}
			}
		} else {
			log.Errorf("Failed to unmarshal default_out custom JSON: %v", err)
		}
	}
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
	// 清理 dispatcher 中的流量计数器
	c.dispatcher.Counter.Delete(tag)

	// 清理 nodeReportMinTrafficBytes
	delete(c.nodeReportMinTrafficBytes, tag)

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

	err := c.removeInbound(tag)
	if err != nil {
		return fmt.Errorf("remove in error: %s", err)
	}
	err = c.removeOutbound(tag)
	if err != nil {
		return fmt.Errorf("remove out error: %s", err)
	}
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
