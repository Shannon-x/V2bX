package node

import (
	"context"

	"github.com/InazumaV/V2bX/api/panel"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) reportUserTrafficTask(ctx context.Context) (err error) {
	userTraffic, _ := c.server.GetUserTrafficSlice(c.tag, true)
	if len(userTraffic) > 0 {
		err = c.getAPIClient().ReportUserTrafficCtx(ctx, userTraffic)
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
		} else {
			log.WithField("tag", c.tag).Infof("Report %d users traffic", len(userTraffic))
			log.WithField("tag", c.tag).Debugf("User traffic: %+v", userTraffic)
		}
	}

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
