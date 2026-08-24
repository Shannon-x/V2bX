package dispatcher

import (
	"errors"

	"github.com/InazumaV/V2bX/common/bittorrent"
)

// btSniffHeader 把与内核无关的 BT 识别结果适配成 xray 的 SniffResult。
//
// 协议名沿用上游的 "bittorrent"，所以现有的 route.json 规则
// {"protocol":["bittorrent"]} 和面板侧 protocol 审计规则无需任何改动即可命中。
type btSniffHeader struct{}

func (h *btSniffHeader) Protocol() string { return "bittorrent" }

// Domain 返回空串：BT 流量没有可用于目标改写的域名，
// 这样 shouldOverride 会直接返回 false，只把协议名写进 content.Protocol 供路由匹配。
func (h *btSniffHeader) Domain() string { return "" }

var errNotBittorrent = errors.New("not bittorrent header")

// SniffBittorrentUDP 检测 UDP 侧的 BitTorrent 流量（DHT/KRPC、UDP Tracker、uTP）。
// 用它替换上游的 bittorrent.SniffUTP：上游版本既没有 DHT 与 UDP Tracker 识别，
// uTP 那部分也因为拿单调时钟时间戳去比对 Unix 时间而恒不命中。
func SniffBittorrentUDP(b []byte) (*btSniffHeader, error) {
	if bittorrent.SniffUDP(b) {
		return &btSniffHeader{}, nil
	}
	return nil, errNotBittorrent
}
