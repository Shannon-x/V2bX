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
func (n *Node) Start(nodes []conf.NodeConfig, core vCore.Core) (err error) {
	started := make([]*Controller, 0, len(nodes))
	var current *Controller
	defer func() {
		panicValue := recover()
		if err == nil && panicValue == nil {
			return
		}
		log.WithField("err", err).Warn("Rolling back partially-started node controllers")
		closeController := func(c *Controller) {
			if c == nil {
				return
			}
			defer func() {
				if r := recover(); r != nil {
					log.Errorf("Rollback controller panic: %v", r)
				}
			}()
			if closeErr := c.Close(); closeErr != nil {
				log.WithError(closeErr).Error("Rollback close node controller error")
			}
		}
		closeController(current)
		for i := len(started) - 1; i >= 0; i-- {
			closeController(started[i])
		}
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	for i := range nodes {
		p, clientErr := panel.New(&nodes[i].ApiConfig)
		if clientErr != nil {
			return fmt.Errorf("create panel client for [%s-%s-%d] error: %w",
				nodes[i].ApiConfig.APIHost, nodes[i].ApiConfig.NodeType, nodes[i].ApiConfig.NodeID, clientErr)
		}
		current = NewController(core, p, &nodes[i])
		if startErr := current.Start(); startErr != nil {
			return fmt.Errorf("start node controller [%s-%s-%d] error: %w",
				nodes[i].ApiConfig.APIHost, nodes[i].ApiConfig.NodeType, nodes[i].ApiConfig.NodeID, startErr)
		}
		started = append(started, current)
		current = nil
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
