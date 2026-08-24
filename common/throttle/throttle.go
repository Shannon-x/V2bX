// Package throttle 提供按 key 限频的日志闸门。
//
// 用途：BT 拦截这类事件的触发频率极高——一个跑 DHT 的客户端每秒就能产生
// 上百个被丢弃的包。逐个记日志会瞬间刷爆磁盘并把丢包路径变成 I/O 瓶颈，
// 但完全不记又会让运维无法确认规则是否真的在生效。
package throttle

import (
	"sync"
	"sync/atomic"
	"time"
)

// Gate 对每个 key 独立限频，并发安全，零值不可用（请用 New 构造）。
type Gate struct {
	interval time.Duration
	last     sync.Map // key -> *atomic.Int64（上次放行的 UnixNano；0 表示从未放行）
}

func New(interval time.Duration) *Gate {
	return &Gate{interval: interval}
}

// Allow 判断此刻是否应当为 key 输出一条日志。
func (g *Gate) Allow(key string) bool {
	v, _ := g.last.LoadOrStore(key, new(atomic.Int64))
	slot := v.(*atomic.Int64)
	now := time.Now().UnixNano()
	prev := slot.Load()
	// prev 为 0 表示这个 key 从未放行过，必须立刻放行。
	// 不能只靠 now-prev 判断：interval 较大时，now-0 反而会小于 interval，
	// 导致第一条日志被静默吞掉——那正是最需要被看到的一条。
	if prev != 0 && now-prev < int64(g.interval) {
		return false
	}
	return slot.CompareAndSwap(prev, now)
}
