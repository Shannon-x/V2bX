package hy2

import (
	"net"
	"sync"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/counter"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/apernet/hysteria/core/v2/server"
)

var _ server.Authenticator = &V2bX{}

type V2bX struct {
	usersMap map[string]int
	mutex    sync.RWMutex
}

func (v *V2bX) Authenticate(addr net.Addr, auth string, tx uint64) (ok bool, id string) {
	v.mutex.RLock()
	defer v.mutex.RUnlock()
	if _, exists := v.usersMap[auth]; exists {
		return true, auth
	}
	return false, ""
}

// ReturnUserTraffic backfills traffic counters after a failed panel push.
// W3.1 / audit #13: mirror of Xray.ReturnUserTraffic; see that for rationale.
// hy2 keys per-user counters by UUID (h.Auth.usersMap is uuid -> uid).
func (h *Hysteria2) ReturnUserTraffic(tag string, traffic []panel.UserTraffic) error {
	if len(traffic) == 0 {
		return nil
	}
	h.nodesMu.RLock()
	node, ok := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if !ok {
		return nil
	}
	hook, ok := node.TrafficLogger.(*HookServer)
	if !ok {
		return nil
	}
	v, ok := hook.Counter.Load(tag)
	if !ok {
		return nil
	}
	c := v.(*counter.TrafficCounter)

	delta := make(map[int][2]int64, len(traffic))
	for _, t := range traffic {
		delta[t.UID] = [2]int64{t.Upload, t.Download}
	}
	h.Auth.mutex.RLock()
	uidToUUID := make(map[int]string, len(h.Auth.usersMap))
	for uuid, uid := range h.Auth.usersMap {
		uidToUUID[uid] = uuid
	}
	h.Auth.mutex.RUnlock()
	for uid, ud := range delta {
		uuid, ok := uidToUUID[uid]
		if !ok {
			continue
		}
		ts := c.GetCounter(uuid)
		ts.UpCounter.Add(ud[0])
		ts.DownCounter.Add(ud[1])
		// W6 fix-up: see xray equivalent. MarkDirty is required so the
		// next IterateDirty(true) sees the restored traffic.
		c.MarkDirty(uuid)
	}
	return nil
}

func (h *Hysteria2) AddUsers(p *vCore.AddUsersParams) (added int, err error) {
	h.Auth.mutex.Lock()
	for _, user := range p.Users {
		h.Auth.usersMap[user.Uuid] = user.Id
	}
	h.Auth.mutex.Unlock()
	return len(p.Users), nil
}

func (h *Hysteria2) DelUsers(users []panel.UserInfo, tag string, _ *panel.NodeInfo) error {
	// W2.1: snapshot pointer under read lock; Counter operates outside.
	h.nodesMu.RLock()
	node, ok := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if ok {
		if hook, ok := node.TrafficLogger.(*HookServer); ok {
			if v, ok := hook.Counter.Load(tag); ok {
				c := v.(*counter.TrafficCounter)
				for _, user := range users {
					c.Delete(user.Uuid)
				}
			}
		}
	}
	h.Auth.mutex.Lock()
	for _, user := range users {
		delete(h.Auth.usersMap, user.Uuid)
	}
	h.Auth.mutex.Unlock()
	return nil
}

func (h *Hysteria2) GetUserTrafficSlice(tag string, reset bool) ([]panel.UserTraffic, error) {
	trafficSlice := make([]panel.UserTraffic, 0)
	h.Auth.mutex.RLock()
	defer h.Auth.mutex.RUnlock()
	// W2.1: snapshot the node under nodesMu read lock instead of indexing twice.
	h.nodesMu.RLock()
	node, ok := h.Hy2nodes[tag]
	h.nodesMu.RUnlock()
	if !ok {
		return nil, nil
	}
	hook := node.TrafficLogger.(*HookServer)
	if v, ok := hook.Counter.Load(tag); ok {
		c := v.(*counter.TrafficCounter)
		// W6 / B3: dirty-set iteration; see xray/sing equivalent.
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
			if up+down > hook.ReportMinTrafficBytes.Load() {
				if h.Auth.usersMap[uuid] == 0 {
					c.Delete(uuid)
					return true
				}
				trafficSlice = append(trafficSlice, panel.UserTraffic{
					UID:      h.Auth.usersMap[uuid],
					Upload:   up,
					Download: down,
				})
			} else if reset && (up > 0 || down > 0) {
				// Deleted user below threshold: clean up instead of accumulating forever
				if h.Auth.usersMap[uuid] == 0 {
					c.Delete(uuid)
					return true
				}
				// W6 follow-up #2: re-mark dirty (see xray equivalent).
				traffic.UpCounter.Add(up)
				traffic.DownCounter.Add(down)
				c.MarkDirty(uuid)
			} else if h.Auth.usersMap[uuid] == 0 {
				// Deleted user with zero traffic: clean up the idle entry
				c.Delete(uuid)
			}
			return true
		}
		c.IterateDirty(reset, walk)
		// W6 review #5: occasional orphan sweep (see xray equivalent).
		// h.Auth.mutex is already held (RLock) for this whole function.
		if reset {
			c.MaybePruneIdle(func(uuid string) bool { return h.Auth.usersMap[uuid] != 0 })
		}
		if len(trafficSlice) == 0 {
			return nil, nil
		}
		return trafficSlice, nil
	}
	return nil, nil
}
