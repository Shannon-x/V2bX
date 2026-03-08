package limiter

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/InazumaV/V2bX/conf"
	"github.com/juju/ratelimit"
)

var limiters sync.Map // map[string]*Limiter

func Init() {
	limiters = sync.Map{}
}

type Limiter struct {
	DomainRules   []*regexp.Regexp
	ProtocolRules []string
	SpeedLimit    int
	UserOnlineIP  *sync.Map      // Key: TagUUID, value: {Key: Ip, value: Uid}
	OldUserOnline *sync.Map      // Key: Ip, value: Uid
	UUIDtoUID     sync.Map       // Key: UUID, value: Uid (lock-free)
	UserLimitInfo *sync.Map      // Key: TagUUID value: UserLimitInfo
	SpeedLimiter  *sync.Map      // key: TagUUID, value: *DynamicBucket
	AliveList     atomic.Pointer[map[int]int]
}

// DynamicBucket supports atomic hot-swap of rate limit bucket
type DynamicBucket struct {
	bucket atomic.Pointer[ratelimit.Bucket]
}

func NewDynamicBucket(limit int64) *DynamicBucket {
	db := &DynamicBucket{}
	b := ratelimit.NewBucketWithQuantum(time.Second, limit, limit)
	db.bucket.Store(b)
	return db
}

func (db *DynamicBucket) Wait(n int64) {
	if b := db.bucket.Load(); b != nil {
		b.Wait(n)
	}
}

func (db *DynamicBucket) Update(limit int64) {
	b := ratelimit.NewBucketWithQuantum(time.Second, limit, limit)
	db.bucket.Store(b)
}

func (db *DynamicBucket) Bucket() *ratelimit.Bucket {
	return db.bucket.Load()
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
		UserOnlineIP:  new(sync.Map),
		UserLimitInfo: new(sync.Map),
		SpeedLimiter:  new(sync.Map),
		OldUserOnline: new(sync.Map),
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
	if v, ok := limiters.LoadAndDelete(tag); ok {
		l := v.(*Limiter)
		l.SpeedLimiter.Range(func(key, _ interface{}) bool {
			l.SpeedLimiter.Delete(key)
			return true
		})
		l.UserOnlineIP.Range(func(key, _ interface{}) bool {
			l.UserOnlineIP.Delete(key)
			return true
		})
		l.UserLimitInfo.Range(func(key, _ interface{}) bool {
			l.UserLimitInfo.Delete(key)
			return true
		})
		l.OldUserOnline.Range(func(key, _ interface{}) bool {
			l.OldUserOnline.Delete(key)
			return true
		})
	}
}

func (l *Limiter) UpdateUser(tag string, added []panel.UserInfo, deleted []panel.UserInfo) {
	for i := range deleted {
		l.UserLimitInfo.Delete(format.UserTag(tag, deleted[i].Uuid))
		l.UserOnlineIP.Delete(format.UserTag(tag, deleted[i].Uuid))
		l.SpeedLimiter.Delete(format.UserTag(tag, deleted[i].Uuid))
		l.UUIDtoUID.Delete(deleted[i].Uuid)
		if al := l.AliveList.Load(); al != nil {
			delete(*al, deleted[i].Id)
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

		// Hot-swap the rate limit bucket atomically
		taguuid := format.UserTag(tag, uuid)
		newLimit := int64(determineSpeedLimit(l.SpeedLimit, limit)) * 1000000 / 8
		if newLimit > 0 {
			if v, ok := l.SpeedLimiter.Load(taguuid); ok {
				v.(*DynamicBucket).Update(newLimit)
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

func (l *Limiter) CheckLimit(taguuid string, ip string, isTcp bool, noSSUDP bool) (Bucket *ratelimit.Bucket, Reject bool) {
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
	if noSSUDP {
		newipMap := new(sync.Map)
		newipMap.Store(ip, uid)
		aliveIp := l.getAliveIp(uid)
		if v, loaded := l.UserOnlineIP.LoadOrStore(taguuid, newipMap); loaded {
			oldipMap := v.(*sync.Map)
			if _, loaded := oldipMap.LoadOrStore(ip, uid); !loaded {
				if v, loaded := l.OldUserOnline.Load(ip); loaded {
					if v.(int) == uid {
						l.OldUserOnline.Delete(ip)
					}
				} else if deviceLimit > 0 {
					if deviceLimit <= aliveIp {
						oldipMap.Delete(ip)
						return nil, true
					}
				}
			}
		} else if v, ok := l.OldUserOnline.Load(ip); ok {
			if v.(int) == uid {
				l.OldUserOnline.Delete(ip)
			}
		} else {
			if deviceLimit > 0 {
				if deviceLimit <= aliveIp {
					l.UserOnlineIP.Delete(taguuid)
					return nil, true
				}
			}
		}
	}

	limit := int64(determineSpeedLimit(nodeLimit, userLimit)) * 1000000 / 8
	if limit > 0 {
		// Check existing bucket first to avoid unnecessary allocation
		if v, ok := l.SpeedLimiter.Load(taguuid); ok {
			return v.(*DynamicBucket).Bucket(), false
		}
		db := NewDynamicBucket(limit)
		if v, loaded := l.SpeedLimiter.LoadOrStore(taguuid, db); loaded {
			return v.(*DynamicBucket).Bucket(), false
		}
		return db.Bucket(), false
	}
	return nil, false
}

func (l *Limiter) GetOnlineDevice() (*[]panel.OnlineUser, error) {
	var onlineUser []panel.OnlineUser
	l.OldUserOnline = new(sync.Map)
	l.UserOnlineIP.Range(func(key, value interface{}) bool {
		taguuid := key.(string)
		ipMap := value.(*sync.Map)
		ipMap.Range(func(key, value interface{}) bool {
			uid := value.(int)
			ip := key.(string)
			l.OldUserOnline.Store(ip, uid)
			onlineUser = append(onlineUser, panel.OnlineUser{UID: uid, IP: ip})
			return true
		})
		l.UserOnlineIP.Delete(taguuid)
		return true
	})

	return &onlineUser, nil
}

type UserIpList struct {
	Uid    int      `json:"Uid"`
	IpList []string `json:"Ips"`
}
