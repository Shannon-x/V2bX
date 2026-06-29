package node

import (
	"context"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/task"
	vCore "github.com/InazumaV/V2bX/core"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) startTasks(node *panel.NodeInfo) {
	// fetch node info task
	// W3.2 / W3.4: use ExecuteCtx so the watchdog timeout actually cancels
	// the in-flight resty calls instead of leaking the goroutine.
	c.nodeInfoMonitorPeriodic = &task.Task{
		Name:       "nodeInfoMonitor",
		Interval:   node.PullInterval,
		ExecuteCtx: c.nodeInfoMonitor,
		Reload:     c.reloadAPIClient,
	}
	// fetch user list task
	c.userReportPeriodic = &task.Task{
		Name:       "reportUserTrafficTask",
		Interval:   node.PushInterval,
		ExecuteCtx: c.reportUserTrafficTask,
		Reload:     c.reloadAPIClient,
	}
	log.WithField("tag", c.tag).Info("Start monitor node status")
	// delay to start nodeInfoMonitor
	_ = c.nodeInfoMonitorPeriodic.Start(false)
	log.WithField("tag", c.tag).Info("Start report node status")
	_ = c.userReportPeriodic.Start(false)
	if node.Security == panel.Tls {
		switch c.CertConfig.CertMode {
		case "none", "", "file", "self":
		default:
			c.renewCertPeriodic = &task.Task{
				Name:     "renewCertTask",
				Interval: time.Hour * 24,
				Execute:  c.renewCertTask,
				Reload:   c.reloadAPIClient,
			}
			log.WithField("tag", c.tag).Info("Start renew cert")
			// delay to start renewCert
			_ = c.renewCertPeriodic.Start(true)
		}
	}
	if c.LimitConfig.EnableDynamicSpeedLimit {
		// W2.4: initialize under trafficMu in case startTasks is ever called
		// concurrently with a stale ticker firing (rebuild-on-reload path).
		c.trafficMu.Lock()
		c.traffic = make(map[string]int64)
		c.trafficMu.Unlock()
		c.dynamicSpeedLimitPeriodic = &task.Task{
			Name:       "dynamicSpeedLimitTask",
			Interval:   time.Duration(c.LimitConfig.DynamicSpeedLimitConfig.Periodic) * time.Second,
			ExecuteCtx: c.SpeedChecker,
		}
		log.Printf("[%s: %d] Start dynamic speed limit", c.getAPIClient().NodeType, c.getAPIClient().NodeId)
	}
}

func (c *Controller) nodeInfoMonitor(ctx context.Context) (err error) {
	api := c.getAPIClient()
	// get node info — W3.4: ctx propagates the task watchdog deadline so an
	// unresponsive panel can't hang the three serial requests indefinitely.
	newN, err := api.GetNodeInfoCtx(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Error("Get node info failed")
		return nil
	}

	// get user info
	newU, err := api.GetUserListCtx(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Error("Get user list failed")
		return nil
	}

	// get user alive
	newA, err := api.GetUserAliveCtx(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Error("Get alive list failed")
		return nil
	}

	if newN != nil {
		// Node config hash changed — update metadata only, DO NOT tear down
		// the inbound handler to avoid disconnecting all active users.
		// Full inbound rebuild is only done on config file change (via watcher).
		log.WithField("tag", c.tag).Info("Node config updated, refreshing metadata")
		// W2.4: atomic publish — readers (reportUserTrafficTask, hot path)
		// always observe a consistent NodeInfo.
		c.info.Store(newN)

		// Update alive list
		if newA != nil {
			c.limiter.AliveList.Store(&newA)
		}

		// Update rules
		if err = c.limiter.UpdateRule(&newN.Rules); err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Update Rule failed")
		}

		// Update custom outbounds dynamically
		if err = c.server.AddNodeCustomOutbounds(newN, c.Options); err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Add custom outbounds failed")
		}

		// Update nodeReportMinTraffic in core
		c.server.UpdateNodeReportMinTraffic(c.tag, newN, c.Options)

		// Re-apply panel DNS-unlock routes on hot reload so editing a DNS route
		// in the panel takes effect without restarting V2bX. For xray this
		// re-renders the DNS file (no-op when RawDNS is unchanged, thanks to the
		// bytes.Equal guard in saveDnsConfig); the config watcher then reloads.
		// Closes the gap where updateDNSConfig only ran on initial AddNode.
		if err = c.server.UpdateDNS(c.tag, newN); err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Update DNS failed")
		}

		// Check interval changes
		if c.nodeInfoMonitorPeriodic.Interval != newN.PullInterval &&
			newN.PullInterval != 0 {
			c.nodeInfoMonitorPeriodic.Interval = newN.PullInterval
			c.nodeInfoMonitorPeriodic.Close()
			_ = c.nodeInfoMonitorPeriodic.Start(false)
		}
		if c.userReportPeriodic.Interval != newN.PushInterval &&
			newN.PushInterval != 0 {
			c.userReportPeriodic.Interval = newN.PushInterval
			c.userReportPeriodic.Close()
			_ = c.userReportPeriodic.Start(false)
		}

		// Fall through to user update logic below (don't return early)
		if newU != nil {
			// User list also arrived with the node update
		}
	}

	// update alive list
	if newA != nil {
		c.limiter.AliveList.Store(&newA)
	}

	// check users
	if len(newU) == 0 {
		return nil
	}
	deleted, added, modified := compareUserList(c.userList, newU)
	// W2.4: snapshot the current NodeInfo pointer once for the rest of this
	// tick; downstream calls must not re-Load to avoid mismatches.
	curInfo := c.info.Load()
	if len(deleted) > 0 {
		// have deleted users
		err = c.server.DelUsers(deleted, c.tag, curInfo)
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Delete users failed")
			return nil
		}
	}
	if len(added) > 0 {
		// have added users
		_, err = c.server.AddUsers(&vCore.AddUsersParams{
			Tag:      c.tag,
			NodeInfo: curInfo,
			Users:    added,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Error("Add users failed")
			return nil
		}
	}
	if len(added) > 0 || len(deleted) > 0 || len(modified) > 0 {
		// update Limiter
		c.limiter.UpdateUser(c.tag, added, deleted, modified)
		// clear traffic record
		if c.LimitConfig.EnableDynamicSpeedLimit {
			// W2.4: trafficMu serializes with SpeedChecker.
			c.trafficMu.Lock()
			for i := range deleted {
				delete(c.traffic, deleted[i].Uuid)
			}
			c.trafficMu.Unlock()
		}
	}
	c.userList = newU
	if len(added)+len(deleted)+len(modified) != 0 {
		log.WithField("tag", c.tag).
			Infof("%d user deleted, %d user added, %d user modified", len(deleted), len(added), len(modified))
	}
	return nil
}

func (c *Controller) SpeedChecker(_ context.Context) error {
	// W2.4: snapshot keys under trafficMu so we can release the lock before
	// the (potentially slow) UpdateDynamicSpeedLimit call. Concurrent
	// nodeInfoMonitor / startTasks reinitialisation are now safe.
	c.trafficMu.Lock()
	type kv struct {
		u string
		t int64
	}
	due := make([]kv, 0, len(c.traffic))
	for u, t := range c.traffic {
		if t >= c.LimitConfig.DynamicSpeedLimitConfig.Traffic {
			due = append(due, kv{u, t})
		}
	}
	for _, x := range due {
		delete(c.traffic, x.u)
	}
	c.trafficMu.Unlock()
	for _, x := range due {
		err := c.limiter.UpdateDynamicSpeedLimit(c.tag, x.u,
			c.LimitConfig.DynamicSpeedLimitConfig.SpeedLimit,
			time.Now().Add(time.Duration(c.LimitConfig.DynamicSpeedLimitConfig.ExpireTime)*time.Minute))
		if err != nil {
			log.WithField("err", err).Error("Update dynamic speed limit failed")
		}
	}
	return nil
}
