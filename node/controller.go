package node

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/task"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/limiter"
	log "github.com/sirupsen/logrus"
)

type Controller struct {
	server    vCore.Core
	apiClient *panel.Client
	tag       string
	limiter   *limiter.Limiter
	// W2.4 / audit #16: traffic is mutated from nodeInfoMonitor and
	// SpeedChecker on independent tickers. trafficMu serializes the map
	// accesses (Go forbids concurrent map write + delete even if values
	// aren't aliased).
	traffic   map[string]int64
	trafficMu sync.Mutex
	userList  []panel.UserInfo
	aliveMap  map[int]int
	// W2.4 / audit #4 #16: info is replaced on every nodeInfoMonitor tick
	// and concurrently read by reportUserTrafficTask. atomic.Pointer keeps
	// reads racefree without forcing every caller through a mutex.
	info                      atomic.Pointer[panel.NodeInfo]
	nodeInfoMonitorPeriodic   *task.Task
	userReportPeriodic        *task.Task
	renewCertPeriodic         *task.Task
	dynamicSpeedLimitPeriodic *task.Task
	onlineIpReportPeriodic    *task.Task
	*conf.Options
	apiConfig *conf.ApiConfig
	apiMutex  sync.RWMutex
}

// NewController return a Node controller with default parameters.
func NewController(server vCore.Core, api *panel.Client, nodeConf *conf.NodeConfig) *Controller {
	controller := &Controller{
		server:    server,
		Options:   &nodeConf.Options,
		apiConfig: &nodeConf.ApiConfig,
		apiClient: api,
	}
	return controller
}

// Start implement the Start() function of the service interface
func (c *Controller) Start() error {
	// First fetch Node Info
	var err error
	node, err := c.getAPIClient().GetNodeInfo()
	if err != nil {
		return fmt.Errorf("get node info error: %s", err)
	}
	// Update user
	c.userList, err = c.getAPIClient().GetUserList()
	if err != nil {
		return fmt.Errorf("get user list error: %s", err)
	}
	if len(c.userList) == 0 {
		return errors.New("add users error: not have any user")
	}
	c.aliveMap, err = c.getAPIClient().GetUserAlive()
	if err != nil {
		return fmt.Errorf("failed to get user alive list: %s", err)
	}
	if len(c.Options.Name) == 0 {
		c.tag = c.buildNodeTag(node)
	} else {
		c.tag = c.Options.Name
	}
	// W6 review #6: the per-user key is format.UserTag(tag,uuid) = "tag|uuid".
	// A tag containing "|" corrupts every tag/uuid split — including the
	// DelNode prefix-scan that reclaims LinkManagers (it would mis-match a
	// sibling node's keys or fail to match its own, leaking ManagedWriters).
	// Reject such a tag up front rather than corrupting state.
	if strings.Contains(c.tag, "|") {
		return fmt.Errorf("node tag %q must not contain '|' (reserved as the tag/uuid separator)", c.tag)
	}

	// W6 review #1: self-rollback. Once we register the limiter and bring up
	// the inbound listener (AddNode), a later failure (AddUsers, etc.) used to
	// return without undoing them — leaving an orphan listener accepting
	// connections that nothing would ever tear down (the outer node.Start
	// rollback only Closes controllers it already appended, not this failing
	// one). Track what we registered and unwind on any error path.
	limiterAdded := false
	nodeAdded := false
	started := false
	defer func() {
		if started {
			return
		}
		if nodeAdded {
			if derr := c.server.DelNode(c.tag); derr != nil {
				log.WithField("tag", c.tag).Warnf("rollback DelNode error: %v", derr)
			}
		}
		if limiterAdded {
			limiter.DeleteLimiter(c.tag)
		}
	}()

	// add limiter
	l := limiter.AddLimiter(node.Type, c.tag, &c.LimitConfig, c.userList, c.aliveMap)
	limiterAdded = true
	// add rule limiter
	if err = l.UpdateRule(&node.Rules); err != nil {
		return fmt.Errorf("update rule error: %s", err)
	}
	c.limiter = l
	if node.Security == panel.Tls {
		err = c.requestCert()
		if err != nil {
			return fmt.Errorf("request cert error: %s", err)
		}
	}
	// Add new tag
	err = c.server.AddNode(c.tag, node, c.Options)
	if err != nil {
		return fmt.Errorf("add new node error: %s", err)
	}
	nodeAdded = true

	err = c.server.AddNodeCustomOutbounds(node, c.Options)
	if err != nil {
		log.WithField("tag", c.tag).Errorf("Add custom outbounds error: %v", err)
	}

	added, err := c.server.AddUsers(&vCore.AddUsersParams{
		Tag:      c.tag,
		Users:    c.userList,
		NodeInfo: node,
	})
	if err != nil {
		return fmt.Errorf("add users error: %s", err)
	}
	log.WithField("tag", c.tag).Infof("Added %d new users", added)
	c.info.Store(node)
	c.startTasks(node)
	started = true // success — disarm the rollback defer
	return nil
}

// Close implement the Close() function of the service interface
func (c *Controller) Close() error {
	limiter.DeleteLimiter(c.tag)
	if c.nodeInfoMonitorPeriodic != nil {
		c.nodeInfoMonitorPeriodic.Close()
	}
	if c.userReportPeriodic != nil {
		c.userReportPeriodic.Close()
	}
	if c.renewCertPeriodic != nil {
		c.renewCertPeriodic.Close()
	}
	if c.dynamicSpeedLimitPeriodic != nil {
		c.dynamicSpeedLimitPeriodic.Close()
	}
	if c.onlineIpReportPeriodic != nil {
		c.onlineIpReportPeriodic.Close()
	}
	err := c.server.DelNode(c.tag)
	if err != nil {
		return fmt.Errorf("del node error: %s", err)
	}
	// W3.3 / audit #47 #55: close the panel HTTP client so idle TLS
	// connections (10 per host × 90s) don't leak across reloads.
	c.apiMutex.Lock()
	if c.apiClient != nil {
		c.apiClient.Close()
	}
	c.apiMutex.Unlock()
	return nil
}

func (c *Controller) buildNodeTag(node *panel.NodeInfo) string {
	return fmt.Sprintf("[%s]-%s:%d", c.getAPIClient().APIHost, node.Type, node.Id)
}

func (c *Controller) getAPIClient() *panel.Client {
	c.apiMutex.RLock()
	defer c.apiMutex.RUnlock()
	return c.apiClient
}

func (c *Controller) reloadAPIClient() {
	c.apiMutex.Lock()
	defer c.apiMutex.Unlock()

	log.Warnf("[%s] Rebuilding API client to recover from task timeout...", c.tag)

	newClient, err := panel.New(c.apiConfig)
	if err != nil {
		log.Errorf("[%s] Failed to rebuild API client: %v", c.tag, err)
		return
	}

	if c.apiClient != nil {
		c.apiClient.Close()
	}
	c.apiClient = newClient
	log.Infof("[%s] API client recursively rebuilt successfully", c.tag)
}
