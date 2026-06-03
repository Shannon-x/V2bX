package node

import (
	"fmt"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	log "github.com/sirupsen/logrus"
)

type Node struct {
	controllers []*Controller
}

func New() *Node {
	return &Node{}
}

// Start brings up every configured node controller. If any controller fails
// to start, ALL previously-started controllers are rolled back so the caller
// is never left with a half-initialised set — which would otherwise leak the
// limiters, periodic tasks, and (post-Wave 1) idle TLS conns of the started
// half until process exit, and would also panic the subsequent Close() loop
// on the nil slot at the failure index.
//
// W5.1 / audit #61.
func (n *Node) Start(nodes []conf.NodeConfig, core vCore.Core) error {
	started := make([]*Controller, 0, len(nodes))
	rollback := func(reason error) {
		log.WithField("err", reason).Warn("Rolling back partially-started node controllers")
		for _, c := range started {
			if cerr := c.Close(); cerr != nil {
				log.WithField("err", cerr).Error("Rollback close node controller error")
			}
		}
	}
	for i := range nodes {
		p, err := panel.New(&nodes[i].ApiConfig)
		if err != nil {
			rollback(err)
			return fmt.Errorf("create panel client for [%s-%s-%d] error: %s",
				nodes[i].ApiConfig.APIHost,
				nodes[i].ApiConfig.NodeType,
				nodes[i].ApiConfig.NodeID,
				err)
		}
		ctrl := NewController(core, p, &nodes[i])
		if err := ctrl.Start(); err != nil {
			rollback(err)
			return fmt.Errorf("start node controller [%s-%s-%d] error: %s",
				nodes[i].ApiConfig.APIHost,
				nodes[i].ApiConfig.NodeType,
				nodes[i].ApiConfig.NodeID,
				err)
		}
		started = append(started, ctrl)
	}
	n.controllers = started
	return nil
}

func (n *Node) Close() {
	for _, c := range n.controllers {
		// Guard against a nil slot just in case — defensive after the W5.1
		// refactor (the new Start no longer leaves nils, but Close should
		// remain safe in the face of any future direct-mutation bug).
		if c == nil {
			continue
		}
		err := c.Close()
		if err != nil {
			log.WithField("err", err).Error("Close node controller error")
		}
	}
	n.controllers = nil
}
