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
	NodeType        string
	RuleMu          sync.RWMutex // Protects rule slices from data race on hot-reloads
	DomainRules     []*regexp.Regexp
	ProtocolRules   []string
	IPRules         []*net.IPNet      // block_ip: CIDR networks to block
	PortRules       []PortRange       // block_port: port ranges to block
	RouteRules      []panel.RouteRule // route/route_ip/direct/proxy rules (source data)
	RouteMatcher    *xrayRouteMatcher // Xray-native route matcher (geosite/geoip/domain/ip)
	DefaultOutbound string            // default_out: custom default outbound tag
	SpeedLimit      int

	// User online IP tracking: sync.Map for high concurrency lock-free scale
	UserOnlineIP *sync.Map // Key: TagUUID, value: *sync.Map {Key: Ip, value: Uid}

	oldOnlineMu   sync.RWMutex // specialized tiny lock just to swap OldUserOnline smoothly
	OldUserOnline *sync.Map    // Key: Ip, value: Uid

	UUIDtoUID     sync.Map  // Key: UUID, value: Uid (lock-free, read-heavy)
	UserLimitInfo *sync.Map // Key: TagUUID value: UserLimitInfo
	SpeedLimiter  *sync.Map // key: TagUUID, value: *rate.DynamicBucket
	aliveMu       sync.Mutex
	AliveList     atomic.Pointer[map[int]int]
}

// UserLimitInfo carries the per-user limit state. All mutable fields are
// atomic because they are written from background goroutines (panel sync
// task, dynamic-speed-limit task, hy2 logger callbacks) and concurrently
// read from connection-handling goroutines (hy2 hook, xray dispatcher,
// sing hook). W2.6 / W2.7 / audit #26 #27 #42 #48.
//
// UID is set once at construction and not mutated, so it stays a plain int.
type UserLimitInfo struct {
	UID               int
	SpeedLimit        atomic.Int64
	DeviceLimit       atomic.Int64
	DynamicSpeedLimit atomic.Int64
	ExpireTime        atomic.Int64
	OverLimit         atomic.Bool
	// deviceMu serializes admission of previously-unreported IPs for this
	// device-limited user. Without it, a burst can let several new IPs all
	// observe the same stale panel count before any of them is reported.
	deviceMu sync.Mutex

	// W6.1 / W3.6 后半 / audit #3: short-window cache so hy2 logger's
	// per-stream callbacks don't re-run the full CheckLimit (which walks
	// sync.Map, applies device-limit math, and may trigger online-IP
	// reorder) for every Connect/TCPRequest/UDPRequest event. The cached
	// rejected decision is reused for up to CheckCacheWindow.
	//
	// Trade-off: device-limit changes (user removed a device) are detected
	// at most CheckCacheWindow late on the cached side. For Reality / hy2
	// where streams are short-lived this is invisible in practice.
	lastCheckNanos    atomic.Int64
	lastCheckRejected atomic.Bool
}

// CheckCacheWindow bounds how stale a UserLimitInfo.lastCheck* read can be
// before callers must re-run the full CheckLimit. 2s is short enough that
// a freshly-OverLimit user is reset within one keepalive window, long
// enough to elide repeated work across hy2's three per-flow callbacks.
const CheckCacheWindow = 2 * time.Second

// CachedCheck returns (rejected, hit) — if hit is true, the rejected bool
// is the cached CheckLimit result valid for CheckCacheWindow. Callers MUST
// fall back to a fresh CheckLimit + StoreCheckResult when hit is false.
//
// W6.1 / W3.6 后半.
func (u *UserLimitInfo) CachedCheck(now time.Time) (rejected bool, hit bool) {
	last := u.lastCheckNanos.Load()
	if last == 0 {
		return false, false
	}
	if now.UnixNano()-last > int64(CheckCacheWindow) {
		return false, false
	}
	return u.lastCheckRejected.Load(), true
}

// StoreCheckResult records a fresh CheckLimit decision into the cache.
// Safe to call concurrently from multiple goroutines; last writer wins,
// which is fine — the cache is best-effort.
func (u *UserLimitInfo) StoreCheckResult(now time.Time, rejected bool) {
	u.lastCheckRejected.Store(rejected)
	u.lastCheckNanos.Store(now.UnixNano())
}

func AddLimiter(nodeType string, tag string, l *conf.LimitConfig, users []panel.UserInfo, aliveList map[int]int) *Limiter {
	info := &Limiter{
		NodeType:      nodeType,
		SpeedLimit:    l.SpeedLimit,
		UserOnlineIP:  new(sync.Map),
		OldUserOnline: new(sync.Map),
		UserLimitInfo: new(sync.Map),
		SpeedLimiter:  new(sync.Map),
	}
	info.AliveList.Store(&aliveList)
	for i := range users {
		info.UUIDtoUID.Store(users[i].Uuid, users[i].Id)
		userLimit := &UserLimitInfo{
			UID: users[i].Id,
		}
		userLimit.SpeedLimit.Store(int64(users[i].SpeedLimit))
		userLimit.DeviceLimit.Store(int64(users[i].DeviceLimit))
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
		l.aliveMu.Lock()
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
		l.aliveMu.Unlock()
	}
	for i := range deleted {
		taguuid := format.UserTag(tag, deleted[i].Uuid)
		l.UserLimitInfo.Delete(taguuid)
		l.SpeedLimiter.Delete(taguuid)
		l.UUIDtoUID.Delete(deleted[i].Uuid)
		// Clean up online IP tracking
		l.UserOnlineIP.Delete(taguuid)
	}
	// Handle modified users: update limits in-place without disrupting connections.
	// W2.6: atomic stores so concurrent CheckLimit readers never see a torn write.
	for i := range modified {
		taguuid := format.UserTag(tag, modified[i].Uuid)
		if v, ok := l.UserLimitInfo.Load(taguuid); ok {
			u := v.(*UserLimitInfo)
			u.SpeedLimit.Store(int64(modified[i].SpeedLimit))
			u.DeviceLimit.Store(int64(modified[i].DeviceLimit))
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
		userLimit.SpeedLimit.Store(int64(added[i].SpeedLimit))
		userLimit.DeviceLimit.Store(int64(added[i].DeviceLimit))
		l.UserLimitInfo.Store(format.UserTag(tag, added[i].Uuid), userLimit)
		l.UUIDtoUID.Store(added[i].Uuid, added[i].Id)
	}
}

func (l *Limiter) UpdateDynamicSpeedLimit(tag, uuid string, limit int, expire time.Time) error {
	if v, ok := l.UserLimitInfo.Load(format.UserTag(tag, uuid)); ok {
		info := v.(*UserLimitInfo)
		// W2.6: atomic stores so concurrent CheckLimit readers see a coherent
		// (DynamicSpeedLimit, ExpireTime) update — not a torn pair.
		info.DynamicSpeedLimit.Store(int64(limit))
		info.ExpireTime.Store(expire.Unix())

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

// ReplaceAliveCounts publishes a complete panel snapshot. The small writer
// mutex prevents a concurrent report delta from being lost between Load and
// Store; readers remain lock-free through atomic.Pointer.
func (l *Limiter) ReplaceAliveCounts(counts map[int]int) {
	replacement := make(map[int]int, len(counts))
	for uid, count := range counts {
		if count > 0 {
			replacement[uid] = count
		}
	}
	l.aliveMu.Lock()
	l.AliveList.Store(&replacement)
	l.aliveMu.Unlock()
}

// MergeAliveCounts applies the per-user count delta returned by a successful
// device report. A zero count removes the user from the sparse AliveList map.
// Copy-on-write keeps concurrent CheckLimit readers race-free.
func (l *Limiter) MergeAliveCounts(delta map[int]int) {
	if len(delta) == 0 {
		return
	}
	l.aliveMu.Lock()
	defer l.aliveMu.Unlock()
	current := l.AliveList.Load()
	merged := make(map[int]int, len(delta))
	if current != nil {
		merged = make(map[int]int, len(*current)+len(delta))
		for uid, count := range *current {
			merged[uid] = count
		}
	}
	for uid, count := range delta {
		if count > 0 {
			merged[uid] = count
		} else {
			delete(merged, uid)
		}
	}
	l.AliveList.Store(&merged)
}

// trackDevice registers the source IP and rejects it when the panel's last
// global count plus this node's not-yet-reported IPs would exceed deviceLimit.
// The panel count alone is necessarily one reporting cycle behind; including
// local pending IPs closes the same-node burst bypass without adding a network
// call to the connection hot path.
func (l *Limiter) trackDevice(taguuid, ip string, info *UserLimitInfo, deviceLimit int) bool {
	if deviceLimit > 0 {
		info.deviceMu.Lock()
		defer info.deviceMu.Unlock()
	}

	var ipMap *sync.Map
	if value, ok := l.UserOnlineIP.Load(taguuid); ok {
		ipMap = value.(*sync.Map)
	} else {
		candidate := new(sync.Map)
		value, _ := l.UserOnlineIP.LoadOrStore(taguuid, candidate)
		ipMap = value.(*sync.Map)
	}

	if _, loaded := ipMap.LoadOrStore(ip, info.UID); loaded {
		return false
	}
	if deviceLimit <= 0 {
		return false
	}

	l.oldOnlineMu.RLock()
	oldOnline := l.OldUserOnline
	l.oldOnlineMu.RUnlock()

	// An IP present in the previous successful reporting window is already
	// included in AliveList, so it must not be counted again as pending.
	if oldUID, ok := oldOnline.Load(ip); ok && oldUID.(int) == info.UID {
		return false
	}

	pending := 0
	ipMap.Range(func(key, value any) bool {
		candidateIP, ok := key.(string)
		if !ok || value.(int) != info.UID {
			return true
		}
		if oldUID, existed := oldOnline.Load(candidateIP); existed && oldUID.(int) == info.UID {
			return true
		}
		pending++
		return true
	})

	if l.getAliveIp(info.UID)+pending > deviceLimit {
		ipMap.Delete(ip)
		return true
	}
	return false
}

// CheckLimit returns a *rate.DynamicBucket so that existing connections
// automatically see rate updates via DynamicBucket.Get().
func (l *Limiter) CheckLimit(taguuid string, ip string, isTcp bool, noSSUDP bool) (bucket *rate.DynamicBucket, Reject bool) {
	ip = strings.TrimPrefix(ip, "::ffff:")

	nodeLimit := l.SpeedLimit
	userLimit := 0
	deviceLimit := 0
	var userLimitInfo *UserLimitInfo
	if v, ok := l.UserLimitInfo.Load(taguuid); ok {
		u := v.(*UserLimitInfo)
		userLimitInfo = u
		// W2.6: atomic loads — CheckLimit runs on every new connection and
		// races with UpdateUser/UpdateDynamicSpeedLimit/expiry resets.
		deviceLimit = int(u.DeviceLimit.Load())
		expire := u.ExpireTime.Load()
		speedLimit := int(u.SpeedLimit.Load())
		if expire < time.Now().Unix() && expire != 0 {
			if speedLimit != 0 {
				userLimit = speedLimit
				u.DynamicSpeedLimit.Store(0)
				u.ExpireTime.Store(0)
			} else {
				l.UserLimitInfo.Delete(taguuid)
			}
		} else {
			userLimit = determineSpeedLimit(speedLimit, int(u.DynamicSpeedLimit.Load()))
		}
	} else {
		return nil, true
	}

	// Device limit check — only for source-TCP connections (matching v2node)
	if noSSUDP || l.NodeType == "hysteria2" {
		if l.trackDevice(taguuid, ip, userLimitInfo, deviceLimit) {
			return nil, true
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
	newOldOnline := new(sync.Map)

	l.UserOnlineIP.Range(func(key, value interface{}) bool {
		taguuid := key.(string)
		var info *UserLimitInfo
		if value, ok := l.UserLimitInfo.Load(taguuid); ok {
			info = value.(*UserLimitInfo)
			info.deviceMu.Lock()
			defer info.deviceMu.Unlock()
		}
		ipMap := value.(*sync.Map)
		ipMap.Range(func(key, value interface{}) bool {
			uid := value.(int)
			ip := key.(string)
			newOldOnline.Store(ip, uid)
			onlineUser = append(onlineUser, panel.OnlineUser{UID: uid, IP: ip})
			return true
		})
		l.UserOnlineIP.Delete(taguuid) // Reset online device
		return true
	})

	l.oldOnlineMu.Lock()
	l.OldUserOnline = newOldOnline
	l.oldOnlineMu.Unlock()

	return onlineUser, nil
}

type UserIpList struct {
	Uid    int      `json:"Uid"`
	IpList []string `json:"Ips"`
}
