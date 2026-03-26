package limiter

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/common/rate"
	"github.com/InazumaV/V2bX/conf"
)

var limiters sync.Map // map[string]*Limiter

func Init() {
	limiters = sync.Map{}
}

type Limiter struct {
	DomainRules     []*regexp.Regexp
	ProtocolRules   []string
	IPRules         []*net.IPNet         // block_ip: CIDR networks to block
	PortRules       []PortRange          // block_port: port ranges to block
	RouteRules      []panel.RouteRule    // route/route_ip/direct/proxy rules (compiled)
	RouteDomainRe   []*regexp.Regexp     // compiled domain regexps for RouteRules
	DefaultOutbound string               // default_out: custom default outbound tag
	SpeedLimit      int

	// User online IP tracking: RWMutex + map replaces nested sync.Map
	// for better performance under high write concurrency.
	onlineMu      sync.RWMutex
	userOnlineIP  map[string]map[string]int // key: TagUUID, value: {IP -> UID}
	oldOnlineMu   sync.RWMutex
	oldUserOnline map[string]int // key: IP, value: UID

	UUIDtoUID     sync.Map       // Key: UUID, value: Uid (lock-free, read-heavy)
	UserLimitInfo *sync.Map      // Key: TagUUID value: UserLimitInfo
	SpeedLimiter  *sync.Map      // key: TagUUID, value: *rate.DynamicBucket
	AliveList     atomic.Pointer[map[int]int]
}

type UserLimitInfo struct {
	UID               int
	SpeedLimit        int
	DeviceLimit       int
	DynamicSpeedLimit int
	ExpireTime        int64
	OverLimit         bool
}

func AddLimiter(tag string, l *conf.LimitConfig, users []panel.UserInfo, aliveList map[int]int) *Limiter {
	info := &Limiter{
		SpeedLimit:    l.SpeedLimit,
		userOnlineIP:  make(map[string]map[string]int),
		oldUserOnline: make(map[string]int),
		UserLimitInfo: new(sync.Map),
		SpeedLimiter:  new(sync.Map),
	}
	info.AliveList.Store(&aliveList)
	for i := range users {
		info.UUIDtoUID.Store(users[i].Uuid, users[i].Id)
		userLimit := &UserLimitInfo{
			UID: users[i].Id,
		}
		if users[i].SpeedLimit != 0 {
			userLimit.SpeedLimit = users[i].SpeedLimit
		}
		if users[i].DeviceLimit != 0 {
			userLimit.DeviceLimit = users[i].DeviceLimit
		}
		info.UserLimitInfo.Store(format.UserTag(tag, users[i].Uuid), userLimit)
	}
	limiters.Store(tag, info)
	return info
}

func GetLimiter(tag string) (info *Limiter, err error) {
	if v, ok := limiters.Load(tag); ok {
		return v.(*Limiter), nil
	}
	return nil, errors.New("not found")
}

func DeleteLimiter(tag string) {
	limiters.Delete(tag)
}

func (l *Limiter) UpdateUser(tag string, added []panel.UserInfo, deleted []panel.UserInfo, modified []panel.UserInfo) {
	if len(deleted) > 0 {
		// Copy-on-write for AliveList to avoid concurrent map write panic
		if al := l.AliveList.Load(); al != nil {
			newAl := make(map[int]int, len(*al))
			for k, v := range *al {
				newAl[k] = v
			}
			for i := range deleted {
				delete(newAl, deleted[i].Id)
			}
			l.AliveList.Store(&newAl)
		}
	}
	for i := range deleted {
		taguuid := format.UserTag(tag, deleted[i].Uuid)
		l.UserLimitInfo.Delete(taguuid)
		l.SpeedLimiter.Delete(taguuid)
		l.UUIDtoUID.Delete(deleted[i].Uuid)
		// Clean up online IP tracking
		l.onlineMu.Lock()
		delete(l.userOnlineIP, taguuid)
		l.onlineMu.Unlock()
	}
	// Handle modified users: update limits in-place without disrupting connections
	for i := range modified {
		taguuid := format.UserTag(tag, modified[i].Uuid)
		if v, ok := l.UserLimitInfo.Load(taguuid); ok {
			u := v.(*UserLimitInfo)
			u.SpeedLimit = modified[i].SpeedLimit
			u.DeviceLimit = modified[i].DeviceLimit
		}
		// Hot-swap the rate limit bucket for existing connections
		limit := int64(determineSpeedLimit(l.SpeedLimit, modified[i].SpeedLimit)) * 1000000 / 8
		if limit > 0 {
			if v, ok := l.SpeedLimiter.Load(taguuid); ok {
				v.(*rate.DynamicBucket).Update(limit)
			} else {
				db := rate.NewDynamicBucket(limit)
				l.SpeedLimiter.Store(taguuid, db)
			}
		} else {
			l.SpeedLimiter.Delete(taguuid)
		}
	}
	for i := range added {
		userLimit := &UserLimitInfo{
			UID: added[i].Id,
		}
		if added[i].SpeedLimit != 0 {
			userLimit.SpeedLimit = added[i].SpeedLimit
			userLimit.ExpireTime = 0
		}
		if added[i].DeviceLimit != 0 {
			userLimit.DeviceLimit = added[i].DeviceLimit
		}
		l.UserLimitInfo.Store(format.UserTag(tag, added[i].Uuid), userLimit)
		l.UUIDtoUID.Store(added[i].Uuid, added[i].Id)
	}
}

func (l *Limiter) UpdateDynamicSpeedLimit(tag, uuid string, limit int, expire time.Time) error {
	if v, ok := l.UserLimitInfo.Load(format.UserTag(tag, uuid)); ok {
		info := v.(*UserLimitInfo)
		info.DynamicSpeedLimit = limit
		info.ExpireTime = expire.Unix()

		// Hot-swap the rate limit bucket atomically — existing connections
		// see the update immediately via DynamicBucket.Get()
		taguuid := format.UserTag(tag, uuid)
		newLimit := int64(determineSpeedLimit(l.SpeedLimit, limit)) * 1000000 / 8
		if newLimit > 0 {
			if v, ok := l.SpeedLimiter.Load(taguuid); ok {
				v.(*rate.DynamicBucket).Update(newLimit)
			}
		}
	} else {
		return errors.New("not found")
	}
	return nil
}

func (l *Limiter) getAliveIp(uid int) int {
	if al := l.AliveList.Load(); al != nil {
		return (*al)[uid]
	}
	return 0
}

// CheckLimit returns a *rate.DynamicBucket so that existing connections
// automatically see rate updates via DynamicBucket.Get().
func (l *Limiter) CheckLimit(taguuid string, ip string, isTcp bool, noSSUDP bool) (bucket *rate.DynamicBucket, Reject bool) {
	ip = strings.TrimPrefix(ip, "::ffff:")

	nodeLimit := l.SpeedLimit
	userLimit := 0
	deviceLimit := 0
	var uid int
	if v, ok := l.UserLimitInfo.Load(taguuid); ok {
		u := v.(*UserLimitInfo)
		deviceLimit = u.DeviceLimit
		uid = u.UID
		if u.ExpireTime < time.Now().Unix() && u.ExpireTime != 0 {
			if u.SpeedLimit != 0 {
				userLimit = u.SpeedLimit
				u.DynamicSpeedLimit = 0
				u.ExpireTime = 0
			} else {
				l.UserLimitInfo.Delete(taguuid)
			}
		} else {
			userLimit = determineSpeedLimit(u.SpeedLimit, u.DynamicSpeedLimit)
		}
	} else {
		return nil, true
	}

	// Device limit check — only for source-TCP connections (matching v2node)
	if noSSUDP {
		l.onlineMu.Lock()
		ipMap, exists := l.userOnlineIP[taguuid]
		if !exists {
			// First connection from this user in this cycle
			ipMap = make(map[string]int, 4)
			ipMap[ip] = uid
			l.userOnlineIP[taguuid] = ipMap
			l.onlineMu.Unlock()

			// Check if this IP was in previous cycle (old online record)
			l.oldOnlineMu.RLock()
			oldUid, oldExists := l.oldUserOnline[ip]
			l.oldOnlineMu.RUnlock()
			if oldExists && oldUid == uid {
				// Returning connection from previous cycle — always allow
				l.oldOnlineMu.Lock()
				delete(l.oldUserOnline, ip)
				l.oldOnlineMu.Unlock()
			} else if deviceLimit > 0 {
				// Use local IP count (1, since we just created the map)
				// plus cross-check with panel alive count
				aliveIp := l.getAliveIp(uid)
				if aliveIp > 1 && deviceLimit <= aliveIp {
					// Over device limit — rollback
					l.onlineMu.Lock()
					delete(l.userOnlineIP, taguuid)
					l.onlineMu.Unlock()
					return nil, true
				}
			}
		} else {
			if _, ipExists := ipMap[ip]; !ipExists {
				// New IP for existing user — use LOCAL IP count for device limit
				localCount := len(ipMap) + 1 // +1 for the new IP about to be added
				l.oldOnlineMu.RLock()
				oldUid, oldExists := l.oldUserOnline[ip]
				l.oldOnlineMu.RUnlock()
				if oldExists && oldUid == uid {
					// Returning IP from previous cycle — always allow
					ipMap[ip] = uid
					l.onlineMu.Unlock()
					l.oldOnlineMu.Lock()
					delete(l.oldUserOnline, ip)
					l.oldOnlineMu.Unlock()
				} else if deviceLimit > 0 && deviceLimit < localCount {
					// Over device limit based on LOCAL count (accurate, not stale)
					l.onlineMu.Unlock()
					return nil, true
				} else {
					ipMap[ip] = uid
					l.onlineMu.Unlock()
				}
			} else {
				l.onlineMu.Unlock()
			}
		}
	}

	limit := int64(determineSpeedLimit(nodeLimit, userLimit)) * 1000000 / 8
	if limit > 0 {
		// Return existing DynamicBucket — connections share it and see live updates
		if v, ok := l.SpeedLimiter.Load(taguuid); ok {
			return v.(*rate.DynamicBucket), false
		}
		db := rate.NewDynamicBucket(limit)
		if v, loaded := l.SpeedLimiter.LoadOrStore(taguuid, db); loaded {
			return v.(*rate.DynamicBucket), false
		}
		return db, false
	}
	return nil, false
}

func (l *Limiter) GetOnlineDevice() ([]panel.OnlineUser, error) {
	var onlineUser []panel.OnlineUser
	newOldOnline := make(map[string]int)

	// Per-entry deletion (matching v2node pattern) instead of bulk swap.
	// This avoids the race where ALL users lose tracking simultaneously,
	// which combined with stale aliveIp data causes periodic mass rejections.
	l.onlineMu.Lock()
	for taguuid, ipMap := range l.userOnlineIP {
		for ip, uid := range ipMap {
			newOldOnline[ip] = uid
			onlineUser = append(onlineUser, panel.OnlineUser{UID: uid, IP: ip})
		}
		delete(l.userOnlineIP, taguuid)
	}
	l.onlineMu.Unlock()

	l.oldOnlineMu.Lock()
	l.oldUserOnline = newOldOnline
	l.oldOnlineMu.Unlock()

	return onlineUser, nil
}


type UserIpList struct {
	Uid    int      `json:"Uid"`
	IpList []string `json:"Ips"`
}
