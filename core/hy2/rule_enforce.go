package hy2

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/InazumaV/V2bX/common/bittorrent"
	"github.com/InazumaV/V2bX/common/throttle"
	"github.com/InazumaV/V2bX/limiter"
	"github.com/apernet/hysteria/core/v2/server"
	"go.uber.org/zap"
)

// hysteria2 路径原本完全没有规则执行层：面板下发的 block_domain / block_ip /
// block_port / protocol 审计规则，在 xray 与 sing-box 上都会生效，唯独 hy2
// 一条都不走——core/hy2 里对 limiter 的引用只有限速和在线统计。
// 加上 hy2config.yaml 默认 ACL 只有三行（google 直连、cn 拒绝），
// 结果就是 hy2 节点对 BT 完全不设防。
//
// 这里用 hysteria 自己暴露的两个扩展点补上：
//
//	server.Outbound      —— 每次建连都会过 TCP()/UDP()，
//	                        而 UDP 侧的 CheckUDP() 是**逐包**调用的
//	                        （server/udp.go:129 checkAddr，带每会话 256 条地址缓存），
//	                        所以能按目的地址做到接近逐包的拦截粒度。
//	server.RequestHook   —— UDP(data, reqAddr) 能拿到**首包的原始载荷**，
//	                        是 hy2 上唯一能按报文特征识别 BT 的位置。
//	                        受 hysteria 架构限制只能看首包（见 core/v2/server/config.go 注释）。

var (
	errBlockedByRule = errors.New("blocked by V2bX rule")
	errBittorrent    = errors.New("bittorrent traffic rejected by V2bX rule")
)

// ruleOutbound 在真正的出站之前套一层面板规则判定。
type ruleOutbound struct {
	tag      string
	next     server.Outbound
	logger   *zap.Logger
	throttle *throttle.Gate
}

func newRuleOutbound(tag string, next server.Outbound, logger *zap.Logger) *ruleOutbound {
	return &ruleOutbound{
		tag:      tag,
		next:     next,
		logger:   logger,
		throttle: throttle.New(30 * time.Second),
	}
}

// check 对 "host:port" 形式的目标地址套用面板下发的域名/IP/端口黑名单。
// 拿不到 limiter 时一律放行，避免规则系统故障时把节点整个打死。
func (o *ruleOutbound) check(reqAddr string) error {
	l, err := limiter.GetLimiter(o.tag)
	if err != nil || l == nil {
		return nil
	}
	// 绝大多数节点没有配任何阻断规则。这一次原子读让热路径上
	// 连 net.SplitHostPort 的两次字符串切分都省掉。
	if !l.HasBlockRules() {
		return nil
	}
	host, portStr, err := net.SplitHostPort(reqAddr)
	if err != nil {
		// 地址形态不认识就不拦，交给下游去报错。
		return nil
	}
	if l.CheckDomainRule(host) {
		o.reject("domain", reqAddr)
		return errBlockedByRule
	}
	// host 是裸 IP 时才走 IP 黑名单；域名要等解析后才知道，
	// 那一步由 hysteria 的 resolver 链完成，这里不重复解析。
	if ip := net.ParseIP(host); ip != nil && l.CheckIPRule(host) {
		o.reject("ip", reqAddr)
		return errBlockedByRule
	}
	if port, err := strconv.Atoi(portStr); err == nil && l.CheckPortRule(port) {
		o.reject("port", reqAddr)
		return errBlockedByRule
	}
	return nil
}

func (o *ruleOutbound) reject(kind, reqAddr string) {
	if o.logger != nil && o.throttle.Allow(o.tag) {
		o.logger.Warn("request rejected by rule",
			zap.String("tag", o.tag),
			zap.String("rule", kind),
			zap.String("addr", reqAddr))
	}
}

func (o *ruleOutbound) TCP(reqAddr string) (net.Conn, error) {
	if err := o.check(reqAddr); err != nil {
		return nil, err
	}
	return o.next.TCP(reqAddr)
}

func (o *ruleOutbound) UDP(reqAddr string) (server.UDPConn, error) {
	if err := o.check(reqAddr); err != nil {
		return nil, err
	}
	return o.next.UDP(reqAddr)
}

// CheckUDP 由 hysteria 在每个 UDP 包发出前调用（结果按会话缓存），
// 是 hy2 上粒度最细的目的地址拦截点。
func (o *ruleOutbound) CheckUDP(reqAddr string) error {
	if err := o.check(reqAddr); err != nil {
		return err
	}
	return o.next.CheckUDP(reqAddr)
}

// btRequestHook 在 UDP 会话首包上做 BitTorrent 报文识别。
// inner 是原有的 sniff hook（hy2config.yaml 里 sniff.enable 打开时才有），
// 保持其行为不变，只在前面加一道 BT 判定。
type btRequestHook struct {
	tag      string
	inner    server.RequestHook
	logger   *zap.Logger
	throttle *throttle.Gate
}

func newBTRequestHook(tag string, inner server.RequestHook, logger *zap.Logger) *btRequestHook {
	return &btRequestHook{
		tag:      tag,
		inner:    inner,
		logger:   logger,
		throttle: throttle.New(30 * time.Second),
	}
}

func (h *btRequestHook) btEnabled() bool {
	l, err := limiter.GetLimiter(h.tag)
	return err == nil && l != nil && l.BlockBittorrentUDP()
}

// Check 决定是否要把这次请求交给 hook 处理。
//
// TCP 一侧刻意不接管：hysteria 在 Check 返回 true 时会先给客户端回一个成功响应
// 再去拨号（服务端 fast-open，见 core/v2/server/server.go:270-277），
// 接管会改变连接失败的可见性。TCP 的规则判定放在 ruleOutbound 里做，语义不变。
func (h *btRequestHook) Check(isUDP bool, reqAddr string) bool {
	if isUDP && h.btEnabled() {
		return true
	}
	if h.inner != nil {
		return h.inner.Check(isUDP, reqAddr)
	}
	return false
}

func (h *btRequestHook) TCP(stream server.HyStream, reqAddr *string) ([]byte, error) {
	if h.inner != nil {
		return h.inner.TCP(stream, reqAddr)
	}
	return nil, nil
}

func (h *btRequestHook) UDP(data []byte, reqAddr *string) error {
	if h.btEnabled() && bittorrent.SniffUDP(data) {
		if h.logger != nil && h.throttle.Allow(h.tag) {
			h.logger.Warn("bittorrent UDP session rejected",
				zap.String("tag", h.tag),
				zap.String("addr", *reqAddr))
		}
		return errBittorrent
	}
	// 只有原 hook 自己也想接管时才转交，避免我们因为 BT 判定返回 true
	// 而让 sniff hook 处理它本来不会碰的请求。
	if h.inner != nil && h.inner.Check(true, *reqAddr) {
		return h.inner.UDP(data, reqAddr)
	}
	return nil
}
