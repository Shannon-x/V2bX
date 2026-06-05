package sing

import (
	"encoding/base64"
	"errors"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/core"
	"github.com/sagernet/sing-box/option"
	"github.com/sirupsen/logrus"
)

func (b *Sing) AddUsers(p *core.AddUsersParams) (added int, err error) {
	_, found := b.box.Inbound().Get(p.Tag)
	if !found {
		return 0, errors.New("the inbound not found")
	}
	// W2.9 / W6: serialize same-tag mutations with a per-tag mutex. The
	// previous global b.users.mapLock blocked EVERY tag's AddUsers/DelUsers
	// for the duration of one tag's rebuildInbound (which can be hundreds
	// of ms). Now: uidMap updates take mapLock briefly, opts read/mutate +
	// rebuildInbound run under tagMu — same-tag stays serial, different
	// tags proceed in parallel.
	tagMu := b.tagMutex(p.Tag)
	tagMu.Lock()
	defer tagMu.Unlock()

	b.users.mapLock.Lock()
	for i := range p.Users {
		b.users.uidMap[p.Users[i].Uuid] = p.Users[i].Id
	}
	b.users.mapLock.Unlock()

	// Get existing inbound options to rebuild with new users
	// W2.3: read under optsMu to coordinate with AddNode/DelNode.
	b.optsMu.RLock()
	opts, ok := b.inboundOptions[p.Tag]
	b.optsMu.RUnlock()
	if !ok {
		return 0, errors.New("inbound options not found for tag: " + p.Tag)
	}
	// Append new users to the inbound options
	switch p.NodeInfo.Type {
	case "vless":
		if o, ok := opts.(*option.VLESSInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.VLESSUser{
					Name: p.Users[i].Uuid,
					Flow: p.VAllss.Flow,
					UUID: p.Users[i].Uuid,
				})
			}
		}
	case "vmess":
		if o, ok := opts.(*option.VMessInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.VMessUser{
					Name: p.Users[i].Uuid,
					UUID: p.Users[i].Uuid,
				})
			}
		}
	case "shadowsocks":
		if o, ok := opts.(*option.ShadowsocksInboundOptions); ok {
			for i := range p.Users {
				var password = p.Users[i].Uuid
				// W1.4 / audit #36: guard against panel-supplied short UUIDs
				// before slicing — otherwise we panic in the AddUsers path
				// and leave the inbound half-initialised.
				switch p.Shadowsocks.Cipher {
				case "2022-blake3-aes-128-gcm":
					if len(password) < 16 {
						logrus.WithFields(logrus.Fields{
							"tag":  p.Tag,
							"uuid": password,
						}).Warn("Shadowsocks 2022 (aes-128) user UUID < 16 bytes, skipping")
						continue
					}
					password = base64.StdEncoding.EncodeToString([]byte(password[:16]))
				case "2022-blake3-aes-256-gcm":
					if len(password) < 32 {
						logrus.WithFields(logrus.Fields{
							"tag":  p.Tag,
							"uuid": password,
						}).Warn("Shadowsocks 2022 (aes-256) user UUID < 32 bytes, skipping")
						continue
					}
					password = base64.StdEncoding.EncodeToString([]byte(password[:32]))
				}
				o.Users = append(o.Users, option.ShadowsocksUser{
					Name:     p.Users[i].Uuid,
					Password: password,
				})
			}
		}
	case "trojan":
		if o, ok := opts.(*option.TrojanInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.TrojanUser{
					Name:     p.Users[i].Uuid,
					Password: p.Users[i].Uuid,
				})
			}
		}
	case "tuic":
		if o, ok := opts.(*option.TUICInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.TUICUser{
					Name:     p.Users[i].Uuid,
					UUID:     p.Users[i].Uuid,
					Password: p.Users[i].Uuid,
				})
			}
		}
	case "hysteria":
		if o, ok := opts.(*option.HysteriaInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.HysteriaUser{
					Name:       p.Users[i].Uuid,
					AuthString: p.Users[i].Uuid,
				})
			}
		}
	case "hysteria2":
		if o, ok := opts.(*option.Hysteria2InboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.Hysteria2User{
					Name:     p.Users[i].Uuid,
					Password: p.Users[i].Uuid,
				})
			}
		}
	case "anytls":
		if o, ok := opts.(*option.AnyTLSInboundOptions); ok {
			for i := range p.Users {
				o.Users = append(o.Users, option.AnyTLSUser{
					Name:     p.Users[i].Uuid,
					Password: p.Users[i].Uuid,
				})
			}
		}
	}
	// Rebuild the inbound with updated user list
	err = b.rebuildInbound(p.Tag, p.NodeInfo.Type, opts)
	if err != nil {
		return 0, err
	}
	return len(p.Users), nil
}

// ReturnUserTraffic backfills traffic counters after a failed panel push.
// W3.1 / audit #13: mirror of Xray.ReturnUserTraffic; see that for rationale.
// sing's per-user key is the raw UUID rather than the format.UserTag string,
// hence the slightly different reverse-map walk.
func (b *Sing) ReturnUserTraffic(tag string, traffic []panel.UserTraffic) error {
	if len(traffic) == 0 {
		return nil
	}
	v, ok := b.hookServer.counter.Load(tag)
	if !ok {
		return nil
	}
	c := v.(*counter.TrafficCounter)
	delta := make(map[int][2]int64, len(traffic))
	for _, t := range traffic {
		delta[t.UID] = [2]int64{t.Upload, t.Download}
	}
	b.users.mapLock.RLock()
	uidToUUID := make(map[int]string, len(b.users.uidMap))
	for uuid, uid := range b.users.uidMap {
		uidToUUID[uid] = uuid
	}
	b.users.mapLock.RUnlock()
	for uid, ud := range delta {
		uuid, ok := uidToUUID[uid]
		if !ok {
			continue
		}
		ts := c.GetCounter(uuid)
		ts.UpCounter.Add(ud[0])
		ts.DownCounter.Add(ud[1])
		// W6 fix-up: see xray equivalent. Without MarkDirty the restored
		// traffic is invisible to the next IterateDirty(true) sweep.
		c.MarkDirty(uuid)
	}
	return nil
}

func (b *Sing) GetUserTraffic(tag, uuid string, reset bool) (up int64, down int64) {
	if v, ok := b.hookServer.counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		up = c.GetUpCount(uuid)
		down = c.GetDownCount(uuid)
		if reset {
			c.Reset(uuid)
		}
		return
	}
	return 0, 0
}

func (b *Sing) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	trafficSlice := make([]panel.UserTraffic, 0)
	hook := b.hookServer
	b.users.mapLock.RLock()
	defer b.users.mapLock.RUnlock()
	// W2.3: snapshot the threshold once instead of indexing the bare map
	// inside the Counter.Range callback (would race with AddNode/DelNode).
	b.optsMu.RLock()
	reportMin := b.nodeReportMinTrafficBytes[tag]
	b.optsMu.RUnlock()
	if v, ok := hook.counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		// W6 / B3: iterate dirty set only — see xray equivalent.
		walk := func(uuidKey string, traffic *counter.TrafficStorage) bool {
			uuid := uuidKey
			var up, down int64
			if reset {
				up = traffic.UpCounter.Swap(0)
				down = traffic.DownCounter.Swap(0)
			} else {
				up = traffic.UpCounter.Load()
				down = traffic.DownCounter.Load()
			}
			if up+down > reportMin {
				if b.users.uidMap[uuid] == 0 {
					c.Delete(uuid)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      b.users.uidMap[uuid],
					Upload:   up,
					Download: down,
				})
			} else if reset && (up > 0 || down > 0) {
				// Deleted user below threshold: clean up instead of accumulating forever
				if b.users.uidMap[uuid] == 0 {
					c.Delete(uuid)
					return true
				}
				// W6 follow-up #2: re-mark dirty so the added-back bytes are
				// revisited next period (see xray equivalent for rationale).
				traffic.UpCounter.Add(up)
				traffic.DownCounter.Add(down)
				c.MarkDirty(uuid)
			} else if reset && up == 0 && down == 0 {
				if b.users.uidMap[uuid] == 0 {
					c.Delete(uuid)
				}
			}
			return true
		}
		c.IterateDirty(reset, walk)
		// W6 review #5: occasional orphan sweep (see xray equivalent).
		if reset {
			c.MaybePruneIdle(func(uuid string) bool { return b.users.uidMap[uuid] != 0 })
		}
		if len(trafficSlice) == 0 {
			return nil, nil
		}
		return trafficSlice, nil
	}
	return nil, nil
}

func (b *Sing) DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error {
	_, found := b.box.Inbound().Get(tag)
	if !found {
		return errors.New("the inbound not found")
	}
	// W2.9 / W6: per-tag lock (same rationale as AddUsers).
	tagMu := b.tagMutex(tag)
	tagMu.Lock()
	defer tagMu.Unlock()

	// Clean up traffic counters (counter is sync.Map — no lock needed).
	deleteUUIDs := make(map[string]struct{}, len(users))
	for i := range users {
		if v, ok := b.hookServer.counter.Load(tag); ok {
			c := v.(*counter.TrafficCounter)
			c.Delete(users[i].Uuid)
		}
		deleteUUIDs[users[i].Uuid] = struct{}{}
	}
	// uidMap mutation under mapLock — narrow scope only.
	b.users.mapLock.Lock()
	for i := range users {
		delete(b.users.uidMap, users[i].Uuid)
	}
	b.users.mapLock.Unlock()

	// Remove users from inbound options
	// W2.3: read under optsMu.
	b.optsMu.RLock()
	opts, ok := b.inboundOptions[tag]
	b.optsMu.RUnlock()
	if !ok {
		return errors.New("inbound options not found for tag: " + tag)
	}
	switch info.Type {
	case "vless":
		if o, ok := opts.(*option.VLESSInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.VLESSUser) string { return u.Name })
		}
	case "vmess":
		if o, ok := opts.(*option.VMessInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.VMessUser) string { return u.Name })
		}
	case "shadowsocks":
		if o, ok := opts.(*option.ShadowsocksInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.ShadowsocksUser) string { return u.Name })
		}
	case "trojan":
		if o, ok := opts.(*option.TrojanInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.TrojanUser) string { return u.Name })
		}
	case "tuic":
		if o, ok := opts.(*option.TUICInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.TUICUser) string { return u.Name })
		}
	case "hysteria":
		if o, ok := opts.(*option.HysteriaInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.HysteriaUser) string { return u.Name })
		}
	case "hysteria2":
		if o, ok := opts.(*option.Hysteria2InboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.Hysteria2User) string { return u.Name })
		}
	case "anytls":
		if o, ok := opts.(*option.AnyTLSInboundOptions); ok {
			o.Users = filterUsers(o.Users, deleteUUIDs, func(u option.AnyTLSUser) string { return u.Name })
		}
	}

	// Rebuild the inbound with updated user list
	return b.rebuildInbound(tag, info.Type, opts)
}

// filterUsers removes users whose name is in the deleteSet.
func filterUsers[T any](users []T, deleteSet map[string]struct{}, getName func(T) string) []T {
	result := make([]T, 0, len(users))
	for _, u := range users {
		if _, del := deleteSet[getName(u)]; !del {
			result = append(result, u)
		}
	}
	return result
}
