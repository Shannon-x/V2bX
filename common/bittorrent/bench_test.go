package bittorrent

import (
	"crypto/rand"
	"testing"
)

// 造几个贴近真实流量的载荷。代理节点上 UDP 的绝大多数是 QUIC/HTTP3 和 DNS。
func benchPayloads() map[string][]byte {
	quic := make([]byte, 1350)
	rand.Read(quic)
	quic[0] = 0xC3 // QUIC Initial 长包头
	quic[1], quic[2], quic[3], quic[4] = 0x00, 0x00, 0x00, 0x01

	quicShort := make([]byte, 1350)
	rand.Read(quicShort)
	quicShort[0] = 0x41 // QUIC 短包头，刻意撞 uTP 的首字节

	dns := []byte{
		0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x03, 'w', 'w', 'w', 0x06, 'g', 'o', 'o', 'g', 'l', 'e', 0x03, 'c', 'o', 'm', 0x00,
		0x00, 0x01, 0x00, 0x01,
	}
	// 最坏情况：以 'd' 开头、以 'e' 结尾但不是 KRPC，会走完整条慢路径的多次全量扫描
	worst := make([]byte, 1350)
	for i := range worst {
		worst[i] = 'x'
	}
	worst[0] = 'd'
	worst[len(worst)-1] = 'e'

	return map[string][]byte{
		"QUIC-Initial-1350B": quic,
		"QUIC-Short-1350B":   quicShort,
		"DNS-32B":            dns,
		"最坏情况-d开头e结尾-1350B":  worst,
		"DHT-命中-96B":         []byte("d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe"),
		"uTP-命中-20B":         buildUTPSyn(1234567),
	}
}

func BenchmarkSniffUDP(b *testing.B) {
	for name, payload := range benchPayloads() {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = SniffUDP(payload)
			}
		})
	}
}
