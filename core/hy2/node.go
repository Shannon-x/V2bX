package hy2

import (
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	"github.com/apernet/hysteria/core/v2/server"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type Hysteria2node struct {
	Hy2server     server.Server
	Tag           string
	Logger        *zap.Logger
	EventLogger   server.EventLogger
	TrafficLogger server.TrafficLogger
}

func (h *Hysteria2) AddNode(tag string, info *panel.NodeInfo, config *conf.Options) error {
	var err error
	hyconfig := &server.Config{}
	var c serverConfig
	v := viper.New()
	if len(config.Hysteria2ConfigPath) != 0 {
		v.SetConfigFile(config.Hysteria2ConfigPath)
		if err := v.ReadInConfig(); err != nil {
			h.Logger.Fatal("failed to read server config", zap.Error(err))
		}
		if err := v.Unmarshal(&c); err != nil {
			h.Logger.Fatal("failed to parse server config", zap.Error(err))
		}
	}
	hook := &HookServer{
		Tag:    tag,
		logger: h.Logger,
	}
	hook.ReportMinTrafficBytes.Store(func() int64 {
		if info.NodeReportMinTraffic > 0 {
			return int64(info.NodeReportMinTraffic) * 1024
		}
		return config.ReportMinTraffic * 1024
	}())
	n := Hysteria2node{
		Tag:    tag,
		Logger: h.Logger,
		EventLogger: &serverLogger{
			Tag:    tag,
			logger: h.Logger,
		},
		TrafficLogger: hook,
	}

	hyconfig, err = n.getHyConfig(info, config, &c)
	if err != nil {
		return err
	}
	hyconfig.Authenticator = h.Auth
	s, err := server.NewServer(hyconfig)
	if err != nil {
		return err
	}
	n.Hy2server = s
	// W2.1: nodesMu protects Hy2nodes against concurrent Range / DelNode.
	h.nodesMu.Lock()
	h.Hy2nodes[tag] = n
	h.nodesMu.Unlock()
	go func() {
		if err := s.Serve(); err != nil {
			if !strings.Contains(err.Error(), "quic: server closed") {
				h.Logger.Error("Server Error", zap.Error(err))
			}
		}
	}()
	return nil
}

func (h *Hysteria2) DelNode(tag string) error {
	// W2.1: take write lock around lookup-and-delete so we can't race a
	// concurrent AddNode for the same tag.
	h.nodesMu.Lock()
	node, ok := h.Hy2nodes[tag]
	if ok {
		delete(h.Hy2nodes, tag)
	}
	h.nodesMu.Unlock()
	if !ok {
		return nil
	}
	// 清理 HookServer 中的流量计数器
	if hook, ok := node.TrafficLogger.(*HookServer); ok {
		hook.Counter.Delete(tag)
	}
	if err := node.Hy2server.Close(); err != nil {
		return err
	}
	return nil
}
