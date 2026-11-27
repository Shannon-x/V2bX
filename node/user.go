package node

import (
	"context"
	"sync"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	log "github.com/sirupsen/logrus"
)

// closeFlushTimeout bounds the final traffic push made while a node closes,
// including the wait for a periodic push that is still in flight. Node.Close
// flushes all nodes at once, so a slow or unreachable panel delays shutdown
// by this much in total, not per node.
const closeFlushTimeout = 10 * time.Second

// pushUserTraffic takes the traffic accrued since the last push and sends it
// to the panel. If the push fails, the traffic is added back so the next push
// retries it. It returns what it took, for the online-device report.
//
// The caller must hold reportMu, so the periodic push and the final flush
// take turns: a failed periodic push always adds its traffic back before the
// flush reads the counters.
func (c *Controller) pushUserTraffic(ctx context.Context) ([]panel.UserTraffic, error) {
	userTraffic, _ := c.server.GetUserTrafficSlice(c.tag, true)
	if len(userTraffic) == 0 {
		return nil, nil
	}
	err := c.getAPIClient().ReportUserTrafficCtx(ctx, userTraffic)
	if err != nil {
		// W3.1 / audit #13: GetUserTrafficSlice already swapped the
		// counters to zero. Push failed, so add the deltas back on top
		// of any traffic that accrued during the in-flight call —
		// otherwise this period's accounting is lost forever and
		// users effectively get free traffic on every panel hiccup.
		if rerr := c.server.ReturnUserTraffic(c.tag, userTraffic); rerr != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": rerr,
			}).Warn("Failed to backfill traffic after report failure; some traffic accounting lost")
		} else {
			log.WithFields(log.Fields{
				"tag":   c.tag,
				"err":   err,
				"users": len(userTraffic),
			}).Info("Report user traffic failed; backfilled counters")
		}
		return userTraffic, err
	}
	log.WithField("tag", c.tag).Infof("Report %d users traffic", len(userTraffic))
	log.WithField("tag", c.tag).Debugf("User traffic: %+v", userTraffic)
	return userTraffic, nil
}

// flushUserTraffic pushes the traffic accrued since the last periodic push.
// Close runs it before DelNode drops the node's counters. Without it every
// restart and every config reload lost up to one push interval of every
// user's traffic. It runs at most once per controller.
func (c *Controller) flushUserTraffic() {
	if !c.flushed.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeFlushTimeout)
	defer cancel()
	if !lockWithContext(ctx, &c.reportMu) {
		log.WithField("tag", c.tag).Warn("Final traffic push skipped: a periodic push was still in flight when the close timeout ran out")
		return
	}
	defer c.reportMu.Unlock()
	traffic, err := c.pushUserTraffic(ctx)
	if err != nil {
		var up, down int64
		for _, t := range traffic {
			up += t.Upload
			down += t.Download
		}
		log.WithFields(log.Fields{
			"tag":      c.tag,
			"err":      err,
			"users":    len(traffic),
			"upload":   up,
			"download": down,
		}).Warn("Final traffic push failed while closing; this traffic will not be billed")
		return
	}
	if len(traffic) > 0 {
		log.WithField("tag", c.tag).Infof("Flushed %d users traffic before close", len(traffic))
	}
}

// lockWithContext acquires mu, giving up when ctx is done.
func lockWithContext(ctx context.Context, mu *sync.Mutex) bool {
	if mu.TryLock() {
		return true
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
			if mu.TryLock() {
				return true
			}
		}
	}
}

func (c *Controller) reportUserTrafficTask(ctx context.Context) (err error) {
	c.reportMu.Lock()
	userTraffic, _ := c.pushUserTraffic(ctx)
	c.reportMu.Unlock()

	onlineDevice, deviceErr := c.limiter.GetOnlineDevice()
	if deviceErr != nil {
		log.Print(deviceErr)
	} else {
		minTraffic := c.Options.DeviceOnlineMinTraffic
		// W2.4: atomic snapshot of NodeInfo; tolerate the brief window where
		// info is nil during startup.
		curInfo := c.info.Load()
		if curInfo != nil && curInfo.DeviceOnlineMinTraffic > 0 {
			minTraffic = int64(curInfo.DeviceOnlineMinTraffic)
		}
		data, reported := buildOnlineDeviceReport(onlineDevice, userTraffic, minTraffic)
		aliveDelta, reportErr := c.getAPIClient().ReportNodeOnlineUsersWithDeltaCtx(ctx, &data)
		if reportErr != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": reportErr,
			}).Info("Report online users failed")
		} else {
			// Apply the panel's post-snapshot count immediately instead of
			// waiting for the next pull interval before unblocking recovered users.
			c.limiter.MergeAliveCounts(aliveDelta)
			log.WithField("tag", c.tag).Infof("Total %d online users, %d Reported", len(onlineDevice), reported)
			log.WithField("tag", c.tag).Debugf("Online users: %+v", data)
		}
	}

	userTraffic = nil
	return nil
}

func buildOnlineDeviceReport(onlineDevice []panel.OnlineUser, userTraffic []panel.UserTraffic, minTraffic int64) (map[int][]string, int) {
	nocountUID := make(map[int]struct{})
	for _, traffic := range userTraffic {
		if traffic.Upload+traffic.Download < minTraffic*1000 {
			nocountUID[traffic.UID] = struct{}{}
		}
	}

	data := make(map[int][]string)
	reported := 0
	for _, online := range onlineDevice {
		if _, skip := nocountUID[online.UID]; skip {
			continue
		}
		data[online.UID] = append(data[online.UID], online.IP)
		reported++
	}
	return data, reported
}

func compareUserList(old, new []panel.UserInfo) (deleted, added, modified []panel.UserInfo) {
	oldMap := make(map[string]panel.UserInfo, len(old))
	for _, u := range old {
		oldMap[u.Uuid] = u
	}

	for _, u := range new {
		if o, ok := oldMap[u.Uuid]; !ok {
			added = append(added, u)
		} else {
			if o.SpeedLimit != u.SpeedLimit || o.DeviceLimit != u.DeviceLimit {
				modified = append(modified, u)
			}
			delete(oldMap, u.Uuid)
		}
	}

	for _, o := range oldMap {
		deleted = append(deleted, o)
	}

	return deleted, added, modified
}
