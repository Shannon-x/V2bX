#!/usr/bin/env bash
# =============================================================================
# V2bX 出站 BT/DHT 兜底封锁（nftables）
#
# 为什么需要这一层：
#   代理内核的 protocol 嗅探只能识别「已知特征」的 BT 流量。DHT(KRPC)、
#   uTP、UDP Tracker 三类里，xray 上游只覆盖了 uTP 且实现有缺陷，
#   sing-box 不认 DHT，hysteria2 的 ACL 压根不支持按协议匹配。
#   而机房告警抓的恰恰是 DHT。所以必须在主机层补一道与内核无关的防线。
#
# 这个脚本做三件事：
#   1. 出站 UDP 端口白名单——DHT/uTP/UDP Tracker 都跑在随机高位端口上，
#      白名单一开，这三类流量整体消失。这是对投诉最直接有效的一条。
#   2. 无视端口，直接按 bencode/KRPC 报文特征丢包——防止 DHT 藏在白名单端口上。
#   3. 丢弃到常见 BT 端口段的出站 TCP。
#
# 独立使用 table inet v2bx_antibt，不会和 ufw / firewalld / docker 的规则冲突，
# 重复执行是幂等的。
#
#   安装：  bash anti-bt-firewall.sh install
#   查看：  bash anti-bt-firewall.sh status      # 带命中计数，用来验证是否真的拦到了
#   卸载：  bash anti-bt-firewall.sh uninstall
# =============================================================================
set -euo pipefail

TABLE="v2bx_antibt"

# 允许出站的 UDP 端口。删/加之前先读下面这段注释。
#   53   DNS            853  DNS over TLS/QUIC
#   80   HTTP/3         443  HTTP/3 + QUIC（绝大多数正常 UDP 流量在这里）
#   123  NTP            8443 部分自建服务的 QUIC 端口
#   3478/5349 STUN/TURN——WebRTC 通话（腾讯会议/Zoom/Discord 语音）需要
# 注意：白名单之外的 UDP 会被丢弃，以下场景会受影响，按需自行放行：
#   - 客户端通过代理跑 WireGuard（默认 51820）
#   - 联机游戏的 P2P 直连（随机高位 UDP）
#   - WebRTC 的媒体流在部分实现里走随机高位端口，仅放行 3478/5349 不够
UDP_ALLOW="${UDP_ALLOW:-53,80,123,443,853,3478,5349,8443}"

# 常见 BT 监听端口段的出站 TCP。
BT_TCP_PORTS="${BT_TCP_PORTS:-6881-6889,6969,51413}"

need_root() {
    if [[ ${EUID} -ne 0 ]]; then
        echo "请用 root 运行" >&2
        exit 1
    fi
}

need_nft() {
    if ! command -v nft >/dev/null 2>&1; then
        echo "未找到 nft 命令。Debian/Ubuntu: apt install -y nftables；RHEL 系: dnf install -y nftables" >&2
        exit 1
    fi
}

install_rules() {
    need_root
    need_nft
    nft list table inet "${TABLE}" >/dev/null 2>&1 && nft delete table inet "${TABLE}"

    nft -f - <<EOF
table inet ${TABLE} {
    set udp_allow {
        type inet_service
        flags interval
        elements = { ${UDP_ALLOW} }
    }

    set bt_tcp_ports {
        type inet_service
        flags interval
        elements = { ${BT_TCP_PORTS} }
    }

    # priority 10：排在常规 filter 链之后，只做「否决」，不接管放行决策。
    chain output {
        type filter hook output priority filter + 10; policy accept;

        # 本机与内网流量原样放行：面板回调、本地 DNS、容器网络都在这里。
        oifname "lo" accept
        ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 169.254.0.0/16 } accept
        ip6 daddr { ::1, fc00::/7, fe80::/10 } accept

        # --- 1. 按报文特征丢弃 DHT/KRPC，不看端口也不看连接状态 -----------
        # 这几条必须排在 ct established 之前：否则一条已建立的 UDP 流
        # 后续再夹带 KRPC 就会被 established 提前放行。
        #
        # @th 是传输层头起点，UDP 头固定 8 字节，所以 @th,64,N 即载荷的前 N bit。
        # 裸载荷匹配要配 meta l4proto 才能确定 @th 的基准，不能只写 udp。
        # KRPC 是 bencode 字典，首字节恒为 'd'，随后是排序后的首个键：
        #   d1:a  查询   d1:r  响应   d1:e  错误   d2:i  带 ip 键的响应
        meta l4proto udp @th,64,32 0x64313a61 counter drop comment "BT-DHT KRPC query"
        meta l4proto udp @th,64,32 0x64313a72 counter drop comment "BT-DHT KRPC response"
        meta l4proto udp @th,64,32 0x64313a65 counter drop comment "BT-DHT KRPC error"
        meta l4proto udp @th,64,32 0x64323a69 counter drop comment "BT-DHT KRPC ip-keyed"

        # BEP 15 UDP Tracker 的 connect 请求：前 8 字节是固定 magic。
        meta l4proto udp @th,64,64 0x0000041727101980 counter drop comment "BT UDP tracker connect"

        # --- 2. 已建立的会话放行 ------------------------------------------
        # hysteria2 / tuic 这类 UDP 入站节点的回程包走这里，
        # 不会被下面的出站白名单误伤。
        ct state established,related accept

        # --- 3. 出站 UDP 端口白名单 ---------------------------------------
        # 走到这里的都是新建的出站 UDP 流。白名单之外一律丢弃，
        # DHT / uTP / UDP Tracker 的随机高位端口在此整体消失。
        # 被 drop 的包不会建立 conntrack 表项，所以每个 DHT 包都会重新走到这里。
        udp dport @udp_allow accept
        meta l4proto udp counter drop comment "non-allowlisted outbound UDP"

        # --- 4. 常见 BT 端口的出站 TCP ------------------------------------
        tcp dport @bt_tcp_ports counter drop comment "BT well-known TCP ports"
    }
}
EOF
    echo "已安装 table inet ${TABLE}"
    echo "  UDP 出站白名单: ${UDP_ALLOW}"
    echo "  TCP 封禁端口段: ${BT_TCP_PORTS}"
    echo
    echo "规则只存在于内存，重启会丢。持久化："
    echo "  Debian/Ubuntu:  nft list ruleset > /etc/nftables.conf && systemctl enable --now nftables"
    echo "  RHEL 系:        nft list ruleset > /etc/sysconfig/nftables.conf && systemctl enable --now nftables"
}

status_rules() {
    need_nft
    if ! nft list table inet "${TABLE}" >/dev/null 2>&1; then
        echo "table inet ${TABLE} 未安装"
        exit 1
    fi
    echo "=== 规则与命中计数 ==="
    nft list table inet "${TABLE}"
    echo
    echo "counter 的 packets 持续增长 = 确实拦到了对应流量。"
    echo "若 KRPC 那几条一直是 0，而 non-allowlisted UDP 在涨，说明 DHT 已经被端口白名单挡在前面了。"
}

uninstall_rules() {
    need_root
    need_nft
    nft list table inet "${TABLE}" >/dev/null 2>&1 && nft delete table inet "${TABLE}"
    echo "已删除 table inet ${TABLE}"
}

case "${1:-}" in
    install)   install_rules ;;
    status)    status_rules ;;
    uninstall) uninstall_rules ;;
    *)
        echo "用法: $0 {install|status|uninstall}"
        echo "可用环境变量覆盖: UDP_ALLOW='53,443,...'  BT_TCP_PORTS='6881-6889,...'"
        exit 1
        ;;
esac
