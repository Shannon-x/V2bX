package xray

import (
	"context"
	"fmt"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/format"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/core/xray/app/dispatcher"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy"
	hyaccount "github.com/xtls/xray-core/proxy/hysteria/account"
)

func (c *Xray) GetUserManager(tag string) (proxy.UserManager, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	handler, err := c.ihm.GetHandler(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("no such inbound tag: %s", err)
	}
	inboundInstance, ok := handler.(proxy.GetInbound)
	if !ok {
		return nil, fmt.Errorf("handler %s is not implement proxy.GetInbound", tag)
	}
	userManager, ok := inboundInstance.GetInbound().(proxy.UserManager)
	if !ok {
		return nil, fmt.Errorf("handler %s is not implement proxy.UserManager", tag)
	}
	return userManager, nil
}

func (c *Xray) DelUsers(users []panel.UserInfo, tag string, _ *panel.NodeInfo) error {
	userManager, err := c.GetUserManager(tag)
	if err != nil {
		return fmt.Errorf("get user manager error: %s", err)
	}
	var user string
	c.users.mapLock.Lock()
	defer c.users.mapLock.Unlock()
	for i := range users {
		user = format.UserTag(tag, users[i].Uuid)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = userManager.RemoveUser(ctx, user)
		cancel()
		if err != nil {
			return err
		}
		delete(c.users.uidMap, user)
		if v, ok := c.dispatcher.Counter.Load(tag); ok {
			tc := v.(*counter.TrafficCounter)
			tc.Delete(user)
		}
		if v, ok := c.dispatcher.LinkManagers.Load(user); ok {
			lm := v.(*dispatcher.LinkManager)
			lm.CloseAll()
			c.dispatcher.LinkManagers.Delete(user)
		}
	}
	return nil
}

func (x *Xray) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	trafficSlice := make([]panel.UserTraffic, 0)
	x.users.mapLock.RLock()
	defer x.users.mapLock.RUnlock()
	// W2.2: snapshot the threshold once instead of indexing the bare map
	// inside the Counter.Range callback (which would race with AddNode /
	// DelNode / UpdateNodeReportMinTraffic and panic on `concurrent map
	// read and map write`).
	x.reportMu.RLock()
	reportMin := x.nodeReportMinTrafficBytes[tag]
	x.reportMu.RUnlock()
	if v, ok := x.dispatcher.Counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		// W6 / B3: iterate only users who actually received traffic since
		// the previous report. On a 100k-user node with ~100 active users
		// per period this is ~1000× faster than Counters.Range. Idle entries
		// are simply skipped (the cleanup of long-dead entries happens via
		// DelUsers / DelNode, not the report path).
		walk := func(emailKey string, traffic *counter.TrafficStorage) bool {
			email := emailKey
			var up, down int64
			if reset {
				// Atomic swap: read and reset in one operation, prevents traffic loss
				up = traffic.UpCounter.Swap(0)
				down = traffic.DownCounter.Swap(0)
			} else {
				up = traffic.UpCounter.Load()
				down = traffic.DownCounter.Load()
			}
			if up+down > reportMin {
				if x.users.uidMap[email] == 0 {
					c.Delete(email)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      x.users.uidMap[email],
					Upload:   up,
					Download: down,
				})
			} else if reset && (up > 0 || down > 0) {
				// Deleted user below threshold: clean up instead of accumulating forever
				if x.users.uidMap[email] == 0 {
					c.Delete(email)
					return true
				}
				// Below threshold, add back to avoid losing small amounts.
				// W6 follow-up #2: MUST re-mark dirty — IterateDirty(true)
				// already swapped the dirty map to empty, so without this the
				// added-back bytes are stranded and never reported until the
				// user happens to send more traffic. Mirrors the ReturnUserTraffic
				// backfill fix.
				traffic.UpCounter.Add(up)
				traffic.DownCounter.Add(down)
				c.MarkDirty(email)
			} else if reset && up == 0 && down == 0 {
				// Completely idle entry — clean up if user is no longer active
				if x.users.uidMap[email] == 0 {
					c.Delete(email)
				}
			}
			return true
		}
		// W6 / B3: walk dirty-set instead of the full Counters map.
		c.IterateDirty(reset, walk)
		// W6 review #5: occasional full sweep to reclaim orphan TrafficStorage
		// for users that left uidMap and went idle (the dirty path never
		// revisits them). Only on report periods (reset) and only every Nth.
		if reset {
			c.MaybePruneIdle(func(email string) bool { return x.users.uidMap[email] != 0 })
		}
		if len(trafficSlice) == 0 {
			return nil, nil
		}
		return trafficSlice, nil
	}
	return nil, nil
}

// ReturnUserTraffic backfills traffic counters after a failed panel push.
// W3.1 / audit #13: reverses the Swap(0) done by GetUserTrafficSlice when
// reset=true, so a failing ReportUserTraffic does not permanently lose the
// period's accounting. atomic.Add preserves any concurrent traffic that
// arrived between the failed push and the backfill.
func (x *Xray) ReturnUserTraffic(tag string, traffic []panel.UserTraffic) error {
	if len(traffic) == 0 {
		return nil
	}
	v, ok := x.dispatcher.Counter.Load(tag)
	if !ok {
		return nil
	}
	c := v.(*counter.TrafficCounter)
	// Build a UID -> (up, down) lookup once.
	delta := make(map[int][2]int64, len(traffic))
	for _, t := range traffic {
		delta[t.UID] = [2]int64{t.Upload, t.Download}
	}
	// Reverse the uidMap (email -> uid) once; cheap for failure-only path.
	x.users.mapLock.RLock()
	uidToEmail := make(map[int]string, len(x.users.uidMap))
	for email, uid := range x.users.uidMap {
		uidToEmail[uid] = email
	}
	x.users.mapLock.RUnlock()
	for uid, ud := range delta {
		email, ok := uidToEmail[uid]
		if !ok {
			continue
		}
		ts := c.GetCounter(email)
		ts.UpCounter.Add(ud[0])
		ts.DownCounter.Add(ud[1])
		// W6 fix-up: re-mark dirty so the next IterateDirty(reset=true) call
		// actually picks up the restored traffic. Without this, the W3.1
		// backfill silently disappears (the user has zero "new" traffic
		// since the failed push, so dirty stays clear).
		c.MarkDirty(email)
	}
	return nil
}

func (c *Xray) AddUsers(p *vCore.AddUsersParams) (added int, err error) {
	c.users.mapLock.Lock()
	defer c.users.mapLock.Unlock()
	for i := range p.Users {
		c.users.uidMap[format.UserTag(p.Tag, p.Users[i].Uuid)] = p.Users[i].Id
	}
	var users []*protocol.User
	switch p.NodeInfo.Type {
	case "vmess":
		users = buildVmessUsers(p.Tag, p.Users)
	case "vless":
		users = buildVlessUsers(p.Tag, p.Users, p.VAllss.Flow)
	case "trojan":
		users = buildTrojanUsers(p.Tag, p.Users)
	case "shadowsocks":
		users = buildSSUsers(p.Tag,
			p.Users,
			p.Shadowsocks.Cipher,
			p.Shadowsocks.ServerKey)
	case "hysteria2":
		users = buildHysteria2Users(p.Tag, p.Users)
	default:
		return 0, fmt.Errorf("unsupported node type: %s", p.NodeInfo.Type)
	}
	man, err := c.GetUserManager(p.Tag)
	if err != nil {
		return 0, fmt.Errorf("get user manager error: %s", err)
	}
	for _, u := range users {
		mUser, err := u.ToMemoryUser()
		if err != nil {
			return 0, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = man.AddUser(ctx, mUser)
		cancel()
		if err != nil {
			return 0, err
		}
	}
	return len(users), nil
}

func buildHysteria2Users(tag string, userInfo []panel.UserInfo) (users []*protocol.User) {
	users = make([]*protocol.User, len(userInfo))
	for i := range userInfo {
		users[i] = buildHysteria2User(tag, &userInfo[i])
	}
	return users
}

func buildHysteria2User(tag string, userInfo *panel.UserInfo) (user *protocol.User) {
	hysteria2Account := &hyaccount.Account{
		Auth: userInfo.Uuid,
	}
	return &protocol.User{
		Level:   0,
		Email:   format.UserTag(tag, userInfo.Uuid),
		Account: serial.ToTypedMessage(hysteria2Account),
	}
}
