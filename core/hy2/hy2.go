package hy2

import (
	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"go.uber.org/zap"
)

var _ vCore.Core = (*Hysteria2)(nil)

type Hysteria2 struct {
	Hy2nodes map[string]Hysteria2node
	Auth     *V2bX
	Logger   *zap.Logger
}

func init() {
	vCore.RegisterCore("hysteria2", New)
}

func New(c *conf.CoreConfig) (vCore.Core, error) {
	loglever := "error"
	if c.Hysteria2Config.LogConfig.Level != "" {
		loglever = c.Hysteria2Config.LogConfig.Level
	}
	log, err := initLogger(loglever, "console")
	if err != nil {
		return nil, err
	}
	return &Hysteria2{
		Hy2nodes: make(map[string]Hysteria2node),
		Auth: &V2bX{
			usersMap: make(map[string]int),
		},
		Logger: log,
	}, nil
}

func (h *Hysteria2) Protocols() []string {
	return []string{
		"hysteria2",
	}
}

func (h *Hysteria2) Start() error {
	return nil
}

func (h *Hysteria2) Close() error {
	for _, n := range h.Hy2nodes {
		err := n.Hy2server.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *Hysteria2) Type() string {
	return "hysteria2"
}

// UpdateNodeReportMinTraffic refreshes the per-node minimum-traffic threshold
// used to filter ReportUserTraffic payloads. Mirrors the Xray equivalent.
//
// W1.7 / audit #41: previously a no-op, so panel-driven threshold changes had
// no effect on hysteria2 nodes until restart. Wave 2 will add a proper lock
// around Hy2nodes; for now we tolerate the existing bare-map access pattern.
func (h *Hysteria2) UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options) {
	node, ok := h.Hy2nodes[tag]
	if !ok {
		return
	}
	hook, ok := node.TrafficLogger.(*HookServer)
	if !ok {
		return
	}
	reportMin := config.ReportMinTraffic
	if info.NodeReportMinTraffic > 0 {
		reportMin = int64(info.NodeReportMinTraffic)
	}
	hook.ReportMinTrafficBytes = reportMin * 1024
}

func (h *Hysteria2) AddNodeCustomOutbounds(info *panel.NodeInfo) error {
	// Not supported for hysteria2 currently, quietly ignore.
	return nil
}
