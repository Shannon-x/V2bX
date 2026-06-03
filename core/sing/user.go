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
	b.users.mapLock.Lock()
	defer b.users.mapLock.Unlock()
	for i := range p.Users {
		b.users.uidMap[p.Users[i].Uuid] = p.Users[i].Id
	}
	// Get existing inbound options to rebuild with new users
	opts, ok := b.inboundOptions[p.Tag]
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
	if v, ok := hook.counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		c.Counters.Range(func(key, value interface{}) bool {
			uuid := key.(string)
			traffic := value.(*counter.TrafficStorage)
			var up, down int64
			if reset {
				up = traffic.UpCounter.Swap(0)
				down = traffic.DownCounter.Swap(0)
			} else {
				up = traffic.UpCounter.Load()
				down = traffic.DownCounter.Load()
			}
			if up+down > b.nodeReportMinTrafficBytes[tag] {
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
				traffic.UpCounter.Add(up)
				traffic.DownCounter.Add(down)
			} else if reset && up == 0 && down == 0 {
				if b.users.uidMap[uuid] == 0 {
					c.Delete(uuid)
				}
			}
			return true
		})
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
	b.users.mapLock.Lock()
	defer b.users.mapLock.Unlock()

	// Clean up traffic counters
	deleteUUIDs := make(map[string]struct{}, len(users))
	for i := range users {
		if v, ok := b.hookServer.counter.Load(tag); ok {
			c := v.(*counter.TrafficCounter)
			c.Delete(users[i].Uuid)
		}
		delete(b.users.uidMap, users[i].Uuid)
		deleteUUIDs[users[i].Uuid] = struct{}{}
	}

	// Remove users from inbound options
	opts, ok := b.inboundOptions[tag]
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
