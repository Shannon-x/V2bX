package bittorrent

import (
	"encoding/binary"
	"testing"
	"time"

	xraybt "github.com/xtls/xray-core/common/protocol/bittorrent"
)

// buildUTPSyn 构造一个合法的 uTP ST_SYN 包（BEP 29）。
// ts 是发送方本机单调时钟的微秒数，与 Unix 纪元无关。
func buildUTPSyn(ts uint32) []byte {
	b := make([]byte, 20)
	b[0] = 0x41 // type=ST_SYN(4), version=1
	b[1] = 0x00 // 无扩展
	binary.BigEndian.PutUint16(b[2:4], 0x1234)
	binary.BigEndian.PutUint32(b[4:8], ts)
	binary.BigEndian.PutUint32(b[8:12], 0)
	binary.BigEndian.PutUint32(b[12:16], 1048576)
	binary.BigEndian.PutUint16(b[16:18], 1)
	binary.BigEndian.PutUint16(b[18:20], 0)
	return b
}

func buildUDPTrackerConnect() []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[:8], udpTrackerProtocolID)
	binary.BigEndian.PutUint32(b[8:12], 0) // action = connect
	binary.BigEndian.PutUint32(b[12:16], 0x5f3a1b2c)
	return b
}

func TestSniffUDPHits(t *testing.T) {
	cases := map[string][]byte{
		"DHT get_peers 查询":     []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe"),
		"DHT ping 查询":          []byte("d1:ad2:id20:abcdefghij01234567890e1:q4:ping1:t2:aa1:y1:qe"),
		"DHT find_node 查询":     []byte("d1:ad2:id20:abcdefghij01234567896:target20:mnopqrstuvwxyz123456e1:q9:find_node1:t2:aa1:y1:qe"),
		"DHT announce_peer 查询": []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz1234564:porti6881e5:token8:aoeusnthe1:q13:announce_peer1:t2:aa1:y1:qe"),
		"DHT 响应":               []byte("d1:rd2:id20:mnopqrstuvwxyz1234565:nodes9:def456...e1:t2:aa1:y1:re"),
		"DHT 带 ip 键的响应":        []byte("d2:ip6:abcdef1:rd2:id20:mnopqrstuvwxyz123456e1:t2:aa1:y1:re"),
		"DHT 错误":               []byte("d1:eli201e23:A Generic Error Ocurrede1:t2:aa1:y1:ee"),
		"UDP Tracker connect":  buildUDPTrackerConnect(),
		// 三个时间戳都必须命中：uTP 的时间戳是发送方单调时钟，
		// 任何拿它跟 Unix 时间比对的判据都会在这里翻车。
		"uTP ST_SYN(刚开机的小时间戳)": buildUTPSyn(1234567),
		"uTP ST_SYN(零时间戳)":     buildUTPSyn(0),
		"uTP ST_SYN(接近回绕上限)":   buildUTPSyn(4294967000),
	}
	for name, payload := range cases {
		if !SniffUDP(payload) {
			t.Errorf("%s: 期望命中 bittorrent，实际未命中", name)
		}
	}
}

func TestSniffUDPMisses(t *testing.T) {
	quicInitial := append([]byte{0xC3, 0x00, 0x00, 0x00, 0x01}, make([]byte, 40)...)
	dnsQuery := []byte{
		0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x03, 'w', 'w', 'w', 0x06, 'g', 'o', 'o', 'g', 'l', 'e', 0x03, 'c', 'o', 'm', 0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	// WireGuard handshake initiation：type=1、3 字节保留位为 0，随后是随机 sender_index。
	// 它的版本位/类型位组合与 uTP 的 ST_DATA 完全同形，是收紧判定前的真实误报来源。
	wgZero := append([]byte{0x01, 0x00, 0x00, 0x00}, make([]byte, 144)...)
	wgRand := append([]byte{0x01, 0x00, 0x00, 0x00}, make([]byte, 144)...)
	binary.BigEndian.PutUint32(wgRand[4:8], uint32(time.Now().UnixMicro()))
	// QUIC 短包头首字节可能正好是 0x41（固定位置位、包号长度 1）
	quicShort := append([]byte{0x41}, make([]byte, 40)...)
	stun := append([]byte{0x00, 0x01, 0x00, 0x00, 0x21, 0x12, 0xA4, 0x42}, make([]byte, 12)...)
	// ST_DATA 不是连接首包，只认 ST_SYN，应当拒掉
	utpData := buildUTPSyn(1234567)
	utpData[0] = 0x01
	// ack_nr 非 0 的「ST_SYN」不合法
	utpBadAck := buildUTPSyn(1234567)
	binary.BigEndian.PutUint16(utpBadAck[18:20], 7)
	// wnd_size 大得离谱，不是真实客户端会发的值
	utpBadWnd := buildUTPSyn(1234567)
	binary.BigEndian.PutUint32(utpBadWnd[12:16], 0xFFFFFFFF)

	cases := map[string][]byte{
		"空包":                 {},
		"过短":                 []byte("d1:ae"),
		"QUIC Initial":       quicInitial,
		"QUIC 短包头 0x41":      quicShort,
		"DNS 查询":             dnsQuery,
		"WireGuard 握手(零索引)":  wgZero,
		"WireGuard 握手(随机索引)": wgRand,
		"STUN 绑定请求":          stun,
		"uTP ST_DATA(非首包)":   utpData,
		"uTP ack_nr 非 0":     utpBadAck,
		"uTP wnd_size 越界":    utpBadWnd,
		"普通 bencode 非 KRPC":  []byte("d8:announce30:http://example.com/announce4:infod4:name4:teseee"),
		"以 d 开头的随机文本":        []byte("dear world, this is definitely not a krpc message at all"),
	}
	for name, payload := range cases {
		if SniffUDP(payload) {
			t.Errorf("%s: 误报为 bittorrent", name)
		}
	}
}

// 回归测试：上游 xray-core 的 SniffUTP 因单位不一致的时间戳比较而恒不命中。
// 这条测试同时锁住「本地实现必须能认出合法 uTP」和「上游确实还没修」两件事。
func TestLocalUTPFixesUpstreamRegression(t *testing.T) {
	for _, ts := range []uint32{0, 1234567, 4294967000, uint32(time.Now().UnixMicro())} {
		pkt := buildUTPSyn(ts)
		if !SniffUTP(pkt) {
			t.Errorf("本地 SniffUTP 未能识别合法 uTP ST_SYN 包 (timestamp=%d)", ts)
		}
		if _, err := xraybt.SniffUTP(pkt); err == nil {
			t.Logf("上游 SniffUTP 对 timestamp=%d 命中了，可考虑退回上游实现", ts)
		}
	}
}
