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

	if onlineDevice, err := c.limiter.GetOnlineDevice(); err != nil {
		log.Print(err)
	} else if len(onlineDevice) > 0 {
		var result []panel.OnlineUser
		var nocountUID = make(map[int]struct{})
		minTraffic := c.Options.DeviceOnlineMinTraffic
		// W2.4: atomic snapshot of NodeInfo; tolerate the brief window where
		// info is nil during startup.
		curInfo := c.info.Load()
		if curInfo != nil && curInfo.DeviceOnlineMinTraffic > 0 {
			minTraffic = int64(curInfo.DeviceOnlineMinTraffic)
		}
		for _, traffic := range userTraffic {
			total := traffic.Upload + traffic.Download
			if total < minTraffic*1000 {
				nocountUID[traffic.UID] = struct{}{}
			}
		}
		for _, online := range onlineDevice {
			if _, ok := nocountUID[online.UID]; !ok {
				result = append(result, online)
			}
		}
		data := make(map[int][]string)
		for _, onlineuser := range result {
			data[onlineuser.UID] = append(data[onlineuser.UID], onlineuser.IP)
		}
		if err = c.getAPIClient().ReportNodeOnlineUsersCtx(ctx, &data); err != nil {
			log.WithFields(log.Fields{
				"tag": c.tag,
				"err": err,
			}).Info("Report online users failed")
		} else {
			log.WithField("tag", c.tag).Infof("Total %d online users, %d Reported", len(onlineDevice), len(result))
			log.WithField("tag", c.tag).Debugf("Online users: %+v", data)
		}
	}

	userTraffic = nil
	return nil
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
