package sing

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/core"
	"github.com/sagernet/sing-box/option"
)

func (b *Sing) AddUsers(p *core.AddUsersParams) (int, error) {
	tagMu := b.tagMutex(p.Tag)
	tagMu.Lock()
	defer tagMu.Unlock()
	return b.applyUsers(p, false, nil)
}

// UpdateUsers replaces one complete panel snapshot with one listener rebuild.
// Candidate options are copied first; failed creation keeps the previous options
// and user accounting and attempts to restore the previous listener.
func (b *Sing) UpdateUsers(p *core.UpdateUsersParams) error {
	tagMu := b.tagMutex(p.Tag)
	tagMu.Lock()
	defer tagMu.Unlock()
	_, err := b.applyUsers(&p.AddUsersParams, true, p.Deleted)
	return err
}

func (b *Sing) applyUsers(p *core.AddUsersParams, replace bool, deleted []panel.UserInfo) (int, error) {
	if _, found := b.box.Inbound().Get(p.Tag); !found {
		return 0, errors.New("the inbound not found")
	}
	b.optsMu.RLock()
	old, ok := b.inboundOptions[p.Tag]
	b.optsMu.RUnlock()
	if !ok {
		return 0, errors.New("inbound options not found for tag: " + p.Tag)
	}
	deleteSet := make(map[string]struct{}, len(deleted))
	for _, u := range deleted {
		deleteSet[u.Uuid] = struct{}{}
	}
	opts, err := userOptions(old, p, replace, deleteSet)
	if err != nil {
		return 0, err
	}
	if err = b.rebuildInbound(p.Tag, p.Type, opts); err != nil {
		if restoreErr := b.rebuildInbound(p.Tag, p.Type, old); restoreErr != nil {
			return 0, errors.Join(err, fmt.Errorf("restore old inbound: %w", restoreErr))
		}
		return 0, err
	}
	b.optsMu.Lock()
	b.inboundOptions[p.Tag] = opts
	b.optsMu.Unlock()

	// UUIDs are scoped to the node: removing a user on one node must not
	// erase another node's accounting for the same subscription UUID.
	b.users.mapLock.Lock()
	if replace {
		prefix := p.Tag + "|"
		for key := range b.users.uidMap {
			if strings.HasPrefix(key, prefix) {
				delete(b.users.uidMap, key)
			}
		}
	} else {
		for _, u := range deleted {
			delete(b.users.uidMap, format.UserTag(p.Tag, u.Uuid))
		}
	}
	for _, u := range p.Users {
		b.users.uidMap[format.UserTag(p.Tag, u.Uuid)] = u.Id
	}
	b.users.mapLock.Unlock()
	if v, ok := b.hookServer.counter.Load(p.Tag); ok {
		for _, u := range deleted {
			v.(*counter.TrafficCounter).Delete(u.Uuid)
		}
	}
	return len(p.Users), nil
}

// mergeInboundUsers allocates a new slice so candidate creation never mutates
// options retained by the current listener or a rollback.
func mergeInboundUsers[T any](old []T, p *core.AddUsersParams, replace bool, deleted map[string]struct{}, name func(T) string, convert func(panel.UserInfo) T) []T {
	capacity := len(p.Users)
	if !replace {
		capacity += len(old)
	}
	users := make([]T, 0, capacity)
	if !replace {
		for _, u := range old {
			if _, found := deleted[name(u)]; !found {
				users = append(users, u)
			}
		}
	}
	for _, u := range p.Users {
		users = append(users, convert(u))
	}
	return users
}

func userOptions(old any, p *core.AddUsersParams, replace bool, deleted map[string]struct{}) (any, error) {
	switch old := old.(type) {
	case *option.VLESSInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.VLESSUser) string { return u.Name },
			func(u panel.UserInfo) option.VLESSUser {
				return option.VLESSUser{Name: u.Uuid, UUID: u.Uuid, Flow: p.VAllss.Flow}
			})
		return &o, nil
	case *option.VMessInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.VMessUser) string { return u.Name },
			func(u panel.UserInfo) option.VMessUser { return option.VMessUser{Name: u.Uuid, UUID: u.Uuid} })
		return &o, nil
	case *option.ShadowsocksInboundOptions:
		o := *old
		passwords := make(map[string]string, len(p.Users))
		for _, u := range p.Users {
			password := u.Uuid
			length := 0
			switch o.Method {
			case "2022-blake3-aes-128-gcm":
				length = 16
			case "2022-blake3-aes-256-gcm":
				length = 32
			}
			if length > 0 {
				if len(password) < length {
					return nil, fmt.Errorf("shadowsocks user UUID is shorter than %d bytes", length)
				}
				password = base64.StdEncoding.EncodeToString([]byte(password[:length]))
			}
			passwords[u.Uuid] = password
		}
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.ShadowsocksUser) string { return u.Name },
			func(u panel.UserInfo) option.ShadowsocksUser {
				return option.ShadowsocksUser{Name: u.Uuid, Password: passwords[u.Uuid]}
			})
		// Empty users must remain a multi-user server with no credentials,
		// rather than falling back to single-user authentication.
		o.Managed = len(o.Users) == 0
		return &o, nil
	case *option.TrojanInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.TrojanUser) string { return u.Name },
			func(u panel.UserInfo) option.TrojanUser { return option.TrojanUser{Name: u.Uuid, Password: u.Uuid} })
		return &o, nil
	case *option.TUICInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.TUICUser) string { return u.Name },
			func(u panel.UserInfo) option.TUICUser {
				return option.TUICUser{Name: u.Uuid, UUID: u.Uuid, Password: u.Uuid}
			})
		return &o, nil
	case *option.HysteriaInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.HysteriaUser) string { return u.Name },
			func(u panel.UserInfo) option.HysteriaUser {
				return option.HysteriaUser{Name: u.Uuid, AuthString: u.Uuid}
			})
		return &o, nil
	case *option.Hysteria2InboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.Hysteria2User) string { return u.Name },
			func(u panel.UserInfo) option.Hysteria2User {
				return option.Hysteria2User{Name: u.Uuid, Password: u.Uuid}
			})
		return &o, nil
	case *option.AnyTLSInboundOptions:
		o := *old
		o.Users = mergeInboundUsers(old.Users, p, replace, deleted,
			func(u option.AnyTLSUser) string { return u.Name },
			func(u panel.UserInfo) option.AnyTLSUser { return option.AnyTLSUser{Name: u.Uuid, Password: u.Uuid} })
		return &o, nil
	default:
		return nil, fmt.Errorf("unsupported inbound user options %T", old)
	}
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
	for key, uid := range b.users.uidMap {
		if uuid, ok := strings.CutPrefix(key, tag+"|"); ok {
			uidToUUID[uid] = uuid
		}
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
		// Mark the restored bytes for the next reporting pass.
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
		// Scan stored users once per report and collect only dirty counters.
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
				if b.users.uidMap[format.UserTag(tag, uuid)] == 0 {
					c.Delete(uuid)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      b.users.uidMap[format.UserTag(tag, uuid)],
					Upload:   up,
					Download: down,
				})
			} else if reset && (up > 0 || down > 0) {
				// Deleted user below threshold: clean up instead of accumulating forever
				if b.users.uidMap[format.UserTag(tag, uuid)] == 0 {
					c.Delete(uuid)
					return true
				}
				// Keep below-threshold bytes marked for the next reporting pass.
				traffic.UpCounter.Add(up)
				traffic.DownCounter.Add(down)
				c.MarkDirty(uuid)
			} else if reset && up == 0 && down == 0 {
				if b.users.uidMap[format.UserTag(tag, uuid)] == 0 {
					c.Delete(uuid)
				}
			}
			return true
		}
		c.IterateDirty(reset, walk)
		// Periodically remove idle counters belonging to deleted users,
		// including entries skipped by the dirty-only callback.
		if reset {
			c.MaybePruneIdle(func(uuid string) bool { return b.users.uidMap[format.UserTag(tag, uuid)] != 0 })
		}
		if len(trafficSlice) == 0 {
			return nil, nil
		}
		return trafficSlice, nil
	}
	return nil, nil
}

func (b *Sing) DelUsers(users []panel.UserInfo, tag string, info *panel.NodeInfo) error {
	tagMu := b.tagMutex(tag)
	tagMu.Lock()
	defer tagMu.Unlock()
	_, err := b.applyUsers(&core.AddUsersParams{Tag: tag, NodeInfo: info}, false, users)
	return err
}
