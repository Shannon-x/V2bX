package xray

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/core/xray/app/dispatcher"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
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
	outBoundConfig, err := buildOutbound(config, tag)
	if err != nil {
		return fmt.Errorf("build outbound error: %s", err)
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
