// Package bittorrent 提供与内核无关的 BitTorrent 报文识别，
// 供 xray dispatcher 与 hysteria2 出站两条路径共用。
//
// 存在的理由：三个内核自带的嗅探能力都盖不住机房投诉里最常见的那一类流量。
//
//	xray-core   只有 TCP 明文握手 + uTP，且 uTP 实现有算术缺陷永不命中，
//	            没有 DHT、没有 UDP Tracker。
//	sing-box    有 TCP 握手 + uTP + UDP Tracker，没有 DHT。
//	hysteria2   完全没有 BT 识别，ACL 语法也无法按应用层协议匹配。
//
// 而 DHT（BEP 5，bencode 编码的 KRPC over UDP）恰恰是「与大量互联网对端交互」
// 这类投诉的主要来源。本包补齐 DHT / UDP Tracker / uTP 三类 UDP 侧识别。
package bittorrent

import (
	"bytes"
	"encoding/binary"
)

// BEP 15 UDP Tracker：connect 请求前 8 字节是固定 magic。
const udpTrackerProtocolID uint64 = 0x41727101980

// SniffUDP 判断一个 UDP 报文是否属于 BitTorrent：
// DHT(KRPC)、UDP Tracker、uTP 三者任一命中即为真。
func SniffUDP(b []byte) bool {
	return SniffKRPC(b) || SniffUDPTracker(b) || SniffUTP(b)
}

// SniffKRPC 识别 BEP 5 的 DHT 报文。KRPC 是 bencode 编码的字典，
// 键按字典序排列，固定含有 "t"(transaction id) 和 "y"(消息类型 q/r/e)：
//
//	查询：d1:ad2:id20:<20B>...e1:q9:get_peers1:t2:aa1:y1:qe
//	响应：d1:rd2:id20:<20B>...e1:t2:aa1:y1:re
//	错误：d1:eli201e23:...e1:t2:aa1:y1:ee
//
// 部分实现会在响应里插入 "2:ip" 键，所以不能只靠前缀，
// 用「以 d 开头 + 以 e 结尾 + 含 1:y1:x」这个组合判定，误报率极低。
func SniffKRPC(b []byte) bool {
	if len(b) < 16 || b[0] != 'd' || b[len(b)-1] != 'e' {
		return false
	}
	// 前缀快路径，覆盖绝大多数报文，避免对长包做多次全量扫描。
	if bytes.HasPrefix(b, []byte("d1:ad2:id20:")) ||
		bytes.HasPrefix(b, []byte("d1:rd2:id20:")) ||
		bytes.HasPrefix(b, []byte("d1:eli")) {
		return true
	}
	if !bytes.Contains(b, []byte("1:t")) {
		return false
	}
	return bytes.Contains(b, []byte("1:y1:q")) ||
		bytes.Contains(b, []byte("1:y1:r")) ||
		bytes.Contains(b, []byte("1:y1:e"))
}

// SniffUDPTracker 识别 BEP 15 的 UDP Tracker connect 请求。
func SniffUDPTracker(b []byte) bool {
	if len(b) < 16 {
		return false
	}
	return binary.BigEndian.Uint64(b[:8]) == udpTrackerProtocolID &&
		binary.BigEndian.Uint32(b[8:12]) == 0
}

// SniffUTP 识别 BEP 29 的 uTP 连接首包（ST_SYN）。
//
// 关于时间戳：uTP 头里的 timestamp_microseconds 是**发送方本机的单调时钟**
// （libutp 的 UTP_GetMicroseconds，POSIX 上基于 CLOCK_MONOTONIC），
// 不是 Unix 纪元时间。所以拿它和 time.Now() 比大小在语义上就是错的——
// 上游 xray-core 正是栽在这里：
//
//	math.Abs(float64(time.Now().UnixMicro() - int64(timestamp))) > float64(24*time.Hour)
//
// 左边约 1.7e15，右边是纳秒计的 8.64e13，条件恒为真，
// 导致上游 SniffUTP 对合法 uTP 包也一律拒绝，等于从未生效。
// 换成 32 位回绕比较同样不对：基准不同，只会变成一个约 28% 通过率的随机闸门。
// 因此这里干脆不看时间戳（sing-box 的实现也不看），改为收紧结构性约束。
//
// 只认 ST_SYN 而不认 ST_DATA/ST_STATE，有两个原因：
// 一是 uTP 会话必然以 ST_SYN 开始，拦住首包整条会话就建立不起来；
// 二是逐包过滤时每个 UDP 包都会走到这里，放宽到其它包类型会与
// WireGuard 握手首包（版本位为 1、类型位为 0）和 QUIC 短包头
// （首字节可能正好是 0x41）产生结构性碰撞。
func SniffUTP(b []byte) bool {
	if len(b) < 20 {
		return false
	}
	// 首字节 = 类型(高 4 位) << 4 | 版本(低 4 位)；ST_SYN 为 4，版本恒为 1。
	if b[0] != 0x41 {
		return false
	}
	// 校验扩展链：extension 为 0 表示结束，否则后跟 1 字节长度 + 载荷。
	extension := b[1]
	pos := 20
	for extension != 0 {
		if extension > 0x04 || pos+2 > len(b) {
			return false
		}
		extension = b[pos]
		length := int(b[pos+1])
		pos += 2 + length
		if pos > len(b) {
			return false
		}
	}
	// connection_id 非 0。
	if binary.BigEndian.Uint16(b[2:4]) == 0 {
		return false
	}
	// wnd_size 合理：非 0 且不超过 16 MiB。
	if wnd := binary.BigEndian.Uint32(b[12:16]); wnd == 0 || wnd > 16<<20 {
		return false
	}
	// ST_SYN 的 seq_nr 非 0、ack_nr 必须为 0。
	return binary.BigEndian.Uint16(b[16:18]) != 0 &&
		binary.BigEndian.Uint16(b[18:20]) == 0
}
