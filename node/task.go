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
		case "none", "", "file", "self", "remote":
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
		_ = c.dynamicSpeedLimitPeriodic.Start(false)
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

	if newN != nil {
		c.pendingNode = newN
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

	// Preserve successfully fetched snapshots if a later request or apply fails:
	// a subsequent conditional request may return 304 instead of resending them.
	if newU != nil {
		c.pendingUsers = newU
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

	if ctx.Err() != nil {
		return nil
	}

	newN = c.pendingNode
	if newN != nil {
		// Metadata refresh. The inbound listener itself is reconciled by the
		// applyInboundConfig check that runs every poll (below) — a
		// security-field change (port/TLS/network/cipher) is picked up there.
		// This path only publishes the new NodeInfo for the metadata/rule/user
		// updates and keeps active users connected for metadata-only changes.
		log.WithField("tag", c.tag).Info("Node config updated, refreshing metadata")
		// W2.4: atomic publish — readers (reportUserTrafficTask, hot path)
		// always observe a consistent NodeInfo.
		c.info.Store(newN)

		// Update alive list
		if newA != nil {
			c.limiter.ReplaceAliveCounts(newA)
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

		// Updating a running task must not wait for that task's own callback.
		if c.nodeInfoMonitorPeriodic != nil {
			c.nodeInfoMonitorPeriodic.SetInterval(newN.PullInterval)
		}
		if c.userReportPeriodic != nil {
			c.userReportPeriodic.SetInterval(newN.PushInterval)
		}

		c.pendingNode = nil
	}

	// H-10: reconcile the running inbound with the desired (panel) config on
	// EVERY poll. appliedSig is the config the live listener was actually built
	// with; if it differs from the panel's — a security field just changed, or
	// a previous rebuild failed — (re)build the inbound. On failure appliedSig
	// stays behind, so this fires AGAIN next poll: a persistent, loud
	// "config NOT applied" alarm in `v2bx log` until the panel config is fixed.
	// It must live outside the `if newN != nil` block because the panel's
	// 304/hash cache makes newN nil once the (bad) config has been seen once —
	// so relying on newN would warn only a single time and then go silent.
	usersApplied := false
	if desired := c.info.Load(); desired != nil && c.appliedSig != inboundSignature(desired) {
		log.WithField("tag", c.tag).Info("Node inbound config changed, applying (port/TLS/network/cipher)")
		if rerr := c.rebuildInbound(desired); rerr != nil {
			log.WithFields(log.Fields{"tag": c.tag, "err": rerr}).
				Error("!!! 节点配置未生效：面板新配置(端口/TLS/传输/加密)应用失败，请检查面板该节点配置 !!!")
			return nil
		}
		usersApplied = true
	}

	// update alive list
	if newA != nil {
		c.limiter.ReplaceAliveCounts(newA)
	}

	// nil means no new snapshot (304); an explicit empty list removes everyone.
	if c.pendingUsers == nil {
		return nil
	}
	newU = c.pendingUsers
	deleted, added, modified := compareUserList(c.userList, newU)
	curInfo := c.info.Load()
	if !usersApplied && (len(deleted) > 0 || len(added) > 0) {
		err = vCore.ApplyUserUpdate(c.server, &vCore.UpdateUsersParams{
			AddUsersParams: vCore.AddUsersParams{Tag: c.tag, NodeInfo: curInfo, Users: newU},
			Added:          added, Deleted: deleted,
		})
		if err != nil {
			log.WithFields(log.Fields{"tag": c.tag, "err": err}).Error("Update users failed; will retry snapshot")
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
	c.pendingUsers = nil
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
