package hy2

import (
	"sync"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"go.uber.org/zap"
)

var _ vCore.Core = (*Hysteria2)(nil)

// Hysteria2 holds the running hysteria2 server instances.
//
// W2.1 / audit #5: Hy2nodes was a bare map mutated from AddNode / DelNode and
// concurrently read by Close / UpdateNodeReportMinTraffic / GetUserTrafficSlice
// from independent goroutines (panel periodic task, fsnotify reload, user-add
// flow). nodesMu serializes those accesses to prevent
// `fatal error: concurrent map read and map write`.
type Hysteria2 struct {
	nodesMu  sync.RWMutex
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
	// W2.1: snapshot under read lock to avoid concurrent map iteration with
	// a DelNode / AddNode that arrives during shutdown.
	h.nodesMu.RLock()
	nodes := make([]Hysteria2node, 0, len(h.Hy2nodes))
	for _, n := range h.Hy2nodes {
		nodes = append(nodes, n)
	}
	h.nodesMu.RUnlock()
	for _, n := range nodes {
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

// UpdateDNS is a no-op for hy2: DNS-unlock routing is an xray-core feature.
// Implemented to satisfy the vCore.Core interface.
func (h *Hysteria2) UpdateDNS(tag string, info *panel.NodeInfo) error {
	return nil
}

// UpdateNodeReportMinTraffic refreshes the per-node minimum-traffic threshold
// used to filter ReportUserTraffic payloads. Mirrors the Xray equivalent.
//
// W1.7 / audit #41: previously a no-op, so panel-driven threshold changes had
// no effect on hysteria2 nodes until restart.
// W2.1: snapshot the node pointer under read lock; ReportMinTrafficBytes is
// stored as atomic.Int64 inside HookServer (see hook.go) so the actual write
// is safe outside the lock.
func (h *Hysteria2) UpdateNodeReportMinTraffic(tag string, info *panel.NodeInfo, config *conf.Options) {
	h.nodesMu.RLock()
	node, ok := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
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
	hook.ReportMinTrafficBytes.Store(reportMin * 1024)
}

func (h *Hysteria2) AddNodeCustomOutbounds(info *panel.NodeInfo, opts *conf.Options) error {
	// Not supported for hysteria2 currently, quietly ignore.
	_ = opts
	return nil
}
