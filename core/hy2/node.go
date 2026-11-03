package hy2

import (
	"errors"
	"fmt"
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
	serveDone     <-chan struct{}
}

func (h *Hysteria2) AddNode(tag string, info *panel.NodeInfo, config *conf.Options) error {
	h.nodesMu.RLock()
	closed := h.closed
	_, exists := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if closed {
		return fmt.Errorf("hysteria2 core is closed")
	}
	if exists {
		return fmt.Errorf("hysteria2 node %q already exists", tag)
	}
	var err error
	hyconfig := &server.Config{}
	var c serverConfig
	v := viper.New()
	if len(config.Hysteria2ConfigPath) != 0 {
		v.SetConfigFile(config.Hysteria2ConfigPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read hysteria2 server config: %w", err)
		}
		if err := v.Unmarshal(&c); err != nil {
			return fmt.Errorf("parse hysteria2 server config: %w", err)
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
		return errors.Join(err, closeUnstartedConfig(hyconfig))
	}
	n.Hy2server = s
	done := make(chan struct{})
	n.serveDone = done
	// W2.1: nodesMu protects Hy2nodes against concurrent Range / DelNode.
	h.nodesMu.Lock()
	if h.closed {
		h.nodesMu.Unlock()
		return errors.Join(fmt.Errorf("hysteria2 core is closed"), s.Close())
	}
	if _, exists := h.Hy2nodes[tag]; exists {
		h.nodesMu.Unlock()
		return errors.Join(fmt.Errorf("hysteria2 node %q already exists", tag), s.Close())
	}
	h.Hy2nodes[tag] = n
	h.nodesMu.Unlock()
	go func() {
		defer close(done)
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
	return node.close()
}

func (node *Hysteria2node) close() error {
	err := node.Hy2server.Close()
	if node.serveDone != nil {
		<-node.serveDone
	}
	// Release counters after the listener has stopped accepting work.
	if hook, ok := node.TrafficLogger.(*HookServer); ok {
		hook.Counter.Delete(node.Tag)
	}
	return err
}
