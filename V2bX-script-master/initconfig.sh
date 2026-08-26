#!/bin/bash
# V2bX 配置文件生成脚本
# 修正 JSON 字段名以匹配 Go struct tags
# 添加 Xray 性能优化配置 (XrayConnectionConfig)
# 添加 ApiVersion 选择支持

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

# 检查系统是否有 IPv6 地址
check_ipv6_support() {
    if ip -6 addr | grep -q "inet6"; then
        echo "1"  # 支持 IPv6
    else
        echo "0"  # 不支持 IPv6
    fi
}

ensure_jq() {
    if command -v jq >/dev/null 2>&1; then
        return 0
    fi
    echo -e "${yellow}正在安装 jq (用于安全生成 JSON)...${plain}"
    if [[ x"${release}" == x"alpine" ]]; then
        apk add --no-cache jq >/dev/null 2>&1
    elif command -v apt >/dev/null 2>&1; then
        apt-get update >/dev/null 2>&1
        apt-get install -y jq >/dev/null 2>&1
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y jq >/dev/null 2>&1
    elif command -v yum >/dev/null 2>&1; then
        yum install -y jq >/dev/null 2>&1
    fi
    command -v jq >/dev/null 2>&1
}

# =====================================================
# 禁止规则的唯一权威来源
#
# 菜单 15「生成配置文件」和菜单 19「更新路由禁止规则」都从这里取规则，
# 保证两个入口产出的防护强度完全一致。改规则只需要改这一处。
#
# 两者的区别只在于落地方式：
#   15  整份重写 route.json = 禁止规则 + final 出站
#   19  只替换 outboundTag=="block" 的部分，保留运维自己加的分流规则
# =====================================================

# 判断 geosite.dat 里是否存在某个分类。分类名在 dat 里以大写存储。
geosite_has_category() {
    local dat="$1" name
    name=$(printf '%s' "$2" | tr '[:lower:]' '[:upper:]')
    [[ -f "${dat}" ]] || return 1
    LC_ALL=C grep -ao '[A-Z0-9@!_-]\{2,\}' "${dat}" 2>/dev/null | grep -qx -- "${name}"
}

# 找到 xray 内核实际使用的 geosite.dat
resolve_geosite_path() {
    local asset=""
    if [[ -f /etc/V2bX/config.json ]] && command -v jq >/dev/null 2>&1; then
        asset=$(jq -r '(.Cores[]? | select(.Type=="xray") | .AssetPath) // empty' /etc/V2bX/config.json 2>/dev/null | head -n1)
    fi
    [[ -z "${asset}" ]] && asset="/etc/V2bX/"
    echo "${asset%/}/geosite.dat"
}

# 输出禁止规则数组（JSON）到 stdout，诊断信息一律走 stderr。
#
# geosite 分类会按本机 geosite.dat 的实际内容裁剪：发布件用的
# Loyalsoldier/v2ray-rules-dat 三个分类都有；换成自建或较老的 dat 时
# 缺分类会让 xray 在 RouterConfig.Build() 阶段 panic、节点直接起不来，
# 所以这里缺什么跳过什么，而不是让整份规则一起炸掉。
build_block_rules() {
    local geosite_dat c
    local bt_cats=() ads_cats=() av_cats=() vpn_cats=() abuse_cats=()
    geosite_dat=$(resolve_geosite_path)

    if [[ ! -f "${geosite_dat}" ]]; then
        echo -e "${yellow}未找到 ${geosite_dat}，本次跳过全部 geosite 类规则${plain}" >&2
        echo -e "${yellow}（广告 / 竞品 / 杀软 / tracker 分类将不会生效，请补上 geosite.dat 后重跑本功能）${plain}" >&2
    else
        echo -e "${yellow}使用 geosite 数据库: ${geosite_dat}${plain}" >&2
        # 逐站分类比手写单个域名更好：上游维护镜像域名。
        # 实测 1337x 分类挡 5/5 个镜像，手写 domain:1337x.to 只挡 1/5；
        # qihoo360 挡 6/6，手写 domain:360.cn 只挡 1/6。
        for c in category-public-tracker category-pt category-ipfs \
                 piratebay 1337x nyaa rutracker btdig; do
            if geosite_has_category "${geosite_dat}" "${c}"; then bt_cats+=("geosite:${c}")
            else echo -e "${yellow}  跳过不存在的分类: ${c}${plain}" >&2; fi
        done
        for c in category-ads-all; do
            if geosite_has_category "${geosite_dat}" "${c}"; then ads_cats+=("geosite:${c}")
            else echo -e "${yellow}  跳过不存在的分类: ${c}${plain}" >&2; fi
        done
        for c in category-antivirus; do
            if geosite_has_category "${geosite_dat}" "${c}"; then av_cats+=("geosite:${c}")
            else echo -e "${yellow}  跳过不存在的分类: ${c}${plain}" >&2; fi
        done
        for c in category-vpnservices; do
            if geosite_has_category "${geosite_dat}" "${c}"; then vpn_cats+=("geosite:${c}")
            else echo -e "${yellow}  跳过不存在的分类: ${c}${plain}" >&2; fi
        done
        # 迅雷/杀软/统计/Tor 的逐站分类，替代原先手写的一堆域名
        for c in xunlei qihoo360 kingsoft torproject umeng; do
            if geosite_has_category "${geosite_dat}" "${c}"; then abuse_cats+=("geosite:${c}")
            else echo -e "${yellow}  跳过不存在的分类: ${c}${plain}" >&2; fi
        done
    fi

    local bt_json ads_json av_json vpn_json abuse_json
    bt_json=$(printf '%s\n' "${bt_cats[@]:-}"  | jq -R . | jq -sc 'map(select(length>0))')
    ads_json=$(printf '%s\n' "${ads_cats[@]:-}" | jq -R . | jq -sc 'map(select(length>0))')
    av_json=$(printf '%s\n' "${av_cats[@]:-}"  | jq -R . | jq -sc 'map(select(length>0))')
    vpn_json=$(printf '%s\n' "${vpn_cats[@]:-}" | jq -R . | jq -sc 'map(select(length>0))')
    abuse_json=$(printf '%s\n' "${abuse_cats[@]:-}" | jq -R . | jq -sc 'map(select(length>0))')

    jq -nc \
        --argjson bt "${bt_json}" --argjson ads "${ads_json}" \
        --argjson av "${av_json}" --argjson vpn "${vpn_json}" \
        --argjson abuse "${abuse_json}" '
    [
      { ruleTag:"block-private", type:"field", outboundTag:"block", ip:["geoip:private"] },
      { ruleTag:"block-private-cidr", type:"field", outboundTag:"block", ip:[
          "127.0.0.1/32","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16",
          "fc00::/7","fe80::/10","::1/128" ] },
      { ruleTag:"block-bt-protocol", type:"field", outboundTag:"block", protocol:["bittorrent"] },
      { ruleTag:"block-bt-tcp-ports", type:"field", outboundTag:"block", network:"tcp", port:"6881-6999,51413" },
      { ruleTag:"block-smtp", type:"field", outboundTag:"block", port:"25,465,587" },
      { ruleTag:"block-bt-dht-bootstrap", type:"field", outboundTag:"block", domain:[
          "full:router.bittorrent.com","full:dht.transmissionbt.com","full:router.utorrent.com",
          "full:dht.libtorrent.org","full:router.bitcomet.com","full:dht.aelitis.com" ] }
    ]
    + (if ($bt | length) > 0 then
        [{ ruleTag:"block-bt-pt-geosite", type:"field", outboundTag:"block", domain:$bt }] else [] end)
    + [
      { ruleTag:"block-bt-tracker-domain", type:"field", outboundTag:"block", domain:[
          "domain:leechers-paradise.org","domain:internetwarriors.net","domain:torrentz2.eu",
          "domain:yts.mx","domain:eztv.re","domain:bt4g.com","domain:torrentgalaxy.to" ] }
    ]
    + (if ($ads | length) > 0 then
        [{ ruleTag:"block-ads", type:"field", outboundTag:"block", domain:$ads }] else [] end)
    + (if ($av | length) > 0 then
        [{ ruleTag:"block-antivirus", type:"field", outboundTag:"block", domain:$av }] else [] end)
    + (if ($vpn | length) > 0 then
        [{ ruleTag:"block-competitor", type:"field", outboundTag:"block", domain:$vpn }] else [] end)
    + (if ($abuse | length) > 0 then
        [{ ruleTag:"block-abuse-geosite", type:"field", outboundTag:"block", domain:$abuse }] else [] end)
    + [
      { ruleTag:"block-abuse-domain", type:"field", outboundTag:"block", domain:[
          "domain:xlpan.com","domain:qqpcmgr.com","domain:guanjia.qq.com",
          "domain:rising.com.cn","domain:jinshanduba.com","domain:xindubawukong.com",
          "domain:netvigator.com","domain:talkingdata.cn",
          "domain:guerrillamail.com","domain:guerrillamailblock.com","domain:sharklasers.com",
          "domain:pokemail.net","domain:spam4.me","domain:bccto.me","domain:chacuo.net",
          "domain:laomoe.com","domain:jiyou.cloud","domain:lolicp.com","domain:ksweb.com",
          "domain:flows.pages.dev","domain:miaoko.pages.dev" ] },
      { ruleTag:"block-abuse-regexp", type:"field", outboundTag:"block", domain:[
          "regexp:^(api|ps|sv|offnavi|newvector|ulog\\.imap|newloc)(\\.map)?\\.(baidu|n\\.shifen)\\.com$",
          "regexp:(^|\\.)[a-z0-9-]*(torrent|ed2k)[a-z0-9-]*(\\.|$)" ] }
    ]'
}

# route.json 的静态兜底。
#
# 装不上 jq 的机器（离线、精简镜像、冷门发行版）也必须能拿到防护规则：
# 生成失败留下一份缺失或过期的 route.json，xray 内核会在
# RouterConfig.Build() 阶段 panic，整个 V2bX 起不来。
#
# 内容与 jq 路径的产出逐字节一致（conf/script_defaults_test.go 会校验），
# 唯一差别是不做 geosite 分类裁剪 —— 发布包自带的 geosite.dat
# 四个分类都有，所以对按发布件安装的机器没有影响。
route_json_static() {
    cat <<'ROUTEEOF'
{
    "domainStrategy": "AsIs",
    "rules": [
        {
            "ruleTag": "block-private",
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "geoip:private"
            ]
        },
        {
            "ruleTag": "block-private-cidr",
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "127.0.0.1/32",
                "10.0.0.0/8",
                "172.16.0.0/12",
                "192.168.0.0/16",
                "169.254.0.0/16",
                "fc00::/7",
                "fe80::/10",
                "::1/128"
            ]
        },
        {
            "ruleTag": "block-bt-protocol",
            "type": "field",
            "outboundTag": "block",
            "protocol": [
                "bittorrent"
            ]
        },
        {
            "ruleTag": "block-bt-tcp-ports",
            "type": "field",
            "outboundTag": "block",
            "network": "tcp",
            "port": "6881-6999,51413"
        },
        {
            "ruleTag": "block-smtp",
            "type": "field",
            "outboundTag": "block",
            "port": "25,465,587"
        },
        {
            "ruleTag": "block-bt-dht-bootstrap",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "full:router.bittorrent.com",
                "full:dht.transmissionbt.com",
                "full:router.utorrent.com",
                "full:dht.libtorrent.org",
                "full:router.bitcomet.com",
                "full:dht.aelitis.com"
            ]
        },
        {
            "ruleTag": "block-bt-pt-geosite",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "geosite:category-public-tracker",
                "geosite:category-pt",
                "geosite:category-ipfs",
                "geosite:piratebay",
                "geosite:1337x",
                "geosite:nyaa",
                "geosite:rutracker",
                "geosite:btdig"
            ]
        },
        {
            "ruleTag": "block-bt-tracker-domain",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "domain:leechers-paradise.org",
                "domain:internetwarriors.net",
                "domain:torrentz2.eu",
                "domain:yts.mx",
                "domain:eztv.re",
                "domain:bt4g.com",
                "domain:torrentgalaxy.to"
            ]
        },
        {
            "ruleTag": "block-ads",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "geosite:category-ads-all"
            ]
        },
        {
            "ruleTag": "block-antivirus",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "geosite:category-antivirus"
            ]
        },
        {
            "ruleTag": "block-competitor",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "geosite:category-vpnservices"
            ]
        },
        {
            "ruleTag": "block-abuse-geosite",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "geosite:xunlei",
                "geosite:qihoo360",
                "geosite:kingsoft",
                "geosite:torproject",
                "geosite:umeng"
            ]
        },
        {
            "ruleTag": "block-abuse-domain",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "domain:xlpan.com",
                "domain:qqpcmgr.com",
                "domain:guanjia.qq.com",
                "domain:rising.com.cn",
                "domain:jinshanduba.com",
                "domain:xindubawukong.com",
                "domain:netvigator.com",
                "domain:talkingdata.cn",
                "domain:guerrillamail.com",
                "domain:guerrillamailblock.com",
                "domain:sharklasers.com",
                "domain:pokemail.net",
                "domain:spam4.me",
                "domain:bccto.me",
                "domain:chacuo.net",
                "domain:laomoe.com",
                "domain:jiyou.cloud",
                "domain:lolicp.com",
                "domain:ksweb.com",
                "domain:flows.pages.dev",
                "domain:miaoko.pages.dev"
            ]
        },
        {
            "ruleTag": "block-abuse-regexp",
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "regexp:^(api|ps|sv|offnavi|newvector|ulog\\.imap|newloc)(\\.map)?\\.(baidu|n\\.shifen)\\.com$",
                "regexp:(^|\\.)[a-z0-9-]*(torrent|ed2k)[a-z0-9-]*(\\.|$)"
            ]
        },
        {
            "ruleTag": "final",
            "type": "field",
            "outboundTag": "IPv4_out",
            "network": "udp,tcp"
        }
    ]
}
ROUTEEOF
}

# 生成一份完整的 route.json（禁止规则 + final 出站）。菜单 15 用这个。
write_default_route_json() {
    local target="${1:-/etc/V2bX/route.json}" blocks
    if ! command -v jq >/dev/null 2>&1; then
        echo -e "${yellow}未找到 jq，改用内置静态规则集（跳过 geosite 分类裁剪）${plain}" >&2
        route_json_static > "${target}"
        return $?
    fi
    blocks=$(build_block_rules) || return 1
    jq -n --argjson b "${blocks}" '{
        domainStrategy: "AsIs",
        rules: ($b + [{ ruleTag:"final", type:"field", outboundTag:"IPv4_out", network:"udp,tcp" }])
    }' > "${target}"
}

# ---- hysteria2 侧的唯一权威 ACL ----
#
# hysteria 的 ACL 语法只有 outbound(address, proto/port) 三段，
# Protocol 只有 tcp/udp/both 这三个传输层协议，
# 从语法层面就写不出「阻断 bittorrent」——写了会在编译期报
# invalid protocol/port 并让节点起不来。所以只能用
# 「BT 域名黑名单 + BT 端口 + UDP 端口白名单」逼近。
# 首条命中即返回，顺序不能调换。
#
# 按协议识别 BT 的能力由 V2bX 自己的 core/hy2/rule_enforce.go 提供。
hy2_acl_block() {
    cat <<'ACLEOF'
acl:
  inline:
    # ---- 1. 原有的域名分流，保持最高优先级 ----
    - direct(geosite:google)
    - reject(geosite:cn)
    - reject(geoip:cn)

    # ---- 2. BT / PT / DHT 相关域名 ----
    - reject(suffix:router.bittorrent.com)
    - reject(suffix:dht.transmissionbt.com)
    - reject(suffix:router.utorrent.com)
    - reject(suffix:dht.libtorrent.org)
    - reject(suffix:router.bitcomet.com)
    - reject(suffix:opentrackr.org)
    - reject(suffix:openbittorrent.com)
    - reject(suffix:demonii.com)
    - reject(suffix:torrent.eu.org)
    - reject(suffix:explodie.org)
    - reject(suffix:leechers-paradise.org)
    - reject(suffix:internetwarriors.net)
    - reject(suffix:tracker.dler.org)
    - reject(suffix:thepiratebay.org)
    - reject(suffix:1337x.to)
    - reject(suffix:nyaa.si)
    - reject(suffix:rutracker.org)
    - reject(suffix:torrentz2.eu)
    - reject(suffix:yts.mx)
    - reject(suffix:eztv.re)
    - reject(suffix:bt4g.com)
    - reject(suffix:btdig.com)
    - reject(suffix:torrentgalaxy.to)
    - reject(suffix:xunlei.com)
    - reject(suffix:sandai.net)

    # ---- 3. 常见 BT 端口 ----
    - reject(all, tcp/6881-6999)
    - reject(all, tcp/51413)
    - reject(all, udp/6881-6999)
    - reject(all, udp/51413)

    # ---- 4. SMTP，防垃圾邮件投诉 ----
    - reject(all, tcp/25)
    - reject(all, tcp/465)
    - reject(all, tcp/587)

    # ---- 5. UDP 端口白名单（真正杀死 DHT 的一条）----
    # 白名单必须写在总封禁之前，否则会被 reject(all, udp/*) 抢先命中。
    #   53   DNS         853  DNS over TLS/QUIC
    #   80   HTTP/3      443  QUIC / HTTP3，绝大多数正常 UDP 在这里
    #   123  NTP         3478-3479 / 5349  STUN/TURN，WebRTC 语音视频需要
    - direct(all, udp/53)
    - direct(all, udp/80)
    - direct(all, udp/123)
    - direct(all, udp/443)
    - direct(all, udp/853)
    - direct(all, udp/3478-3479)
    - direct(all, udp/5349)
    - direct(all, udp/8443)
    # 其余 UDP 一律拒绝：DHT(KRPC)、uTP、UDP Tracker 都在这里被挡下。
    # 代价：经代理跑 WireGuard、联机游戏 P2P 直连、部分 WebRTC 媒体流会不可用。
    - reject(all, udp/*)
ACLEOF
}

# 写出一份完整的默认 hy2config.yaml。菜单 15 用这个。
write_default_hy2config() {
    local target="${1:-/etc/V2bX/hy2config.yaml}"
    {
        cat <<'HYPRE'
quic:
  initStreamReceiveWindow: 16777216
  maxStreamReceiveWindow: 16777216
  initConnReceiveWindow: 33554432
  maxConnReceiveWindow: 33554432
  maxIdleTimeout: 90s
  maxIncomingStreams: 4096
  disablePathMTUDiscovery: false
ignoreClientBandwidth: false
disableUDP: false
udpIdleTimeout: 120s
resolver:
  type: system
HYPRE
        hy2_acl_block
        cat <<'HYPOST'
masquerade:
  type: 404
HYPOST
    } > "${target}"
}

add_node_config() {
    echo -e "${green}请选择节点核心类型：${plain}"
    echo -e "${green}1. xray${plain}"
    echo -e "${green}2. hysteria2${plain}"
    echo -e "${green}3. sing-box${plain}"
    read -rp "请输入：" core_type
    if [ "$core_type" == "1" ]; then
        core="xray"
        core_xray=true
    elif [ "$core_type" == "2" ]; then
        core="hysteria2"
        core_hysteria2=true
    elif [ "$core_type" == "3" ]; then
        core="sing"
        core_sing=true
    else
        echo "无效的选择。请选择 1、2 或 3。"
        return 1
    fi
    while true; do
        read -rp "请输入节点Node ID：" NodeID
        if [[ "$NodeID" =~ ^[0-9]+$ ]]; then
            break
        else
            echo "错误：请输入正确的数字作为Node ID。"
        fi
    done

    # API 版本选择
    api_version=1
    if [ "$fixed_api_version" != "" ]; then
        api_version=$fixed_api_version
    else
        echo -e "${yellow}请选择面板 API 版本：${plain}"
        echo -e "${green}1. V1 UniProxy (默认，兼容大部分面板)${plain}"
        echo -e "${green}2. V2 Flat API (适用于 Shannon-x/v2board)${plain}"
        read -rp "请输入 [默认1]：" api_ver_input
        if [ "$api_ver_input" == "2" ]; then
            api_version=2
        fi
        if [ "$fixed_api_info" = true ]; then
            fixed_api_version=$api_version
        fi
    fi

    if [ "$core_hysteria2" = true ] && [ "$core_xray" != true ]; then
        NodeType="hysteria2"
    else
        echo -e "${yellow}请选择节点传输协议：${plain}"
        echo -e "${green}1. Shadowsocks${plain}"
        echo -e "${green}2. Vless${plain}"
        echo -e "${green}3. Vmess${plain}"
        echo -e "${green}4. Trojan${plain}"
        if [[ "$core_hysteria2" == true || "$core_xray" == true ]]; then
            echo -e "${green}5. Hysteria2${plain}"
        fi
        if [[ "$core_sing" == true ]]; then
            echo -e "${green}6. AnyTLS${plain}（sing-box 内核，强制 TLS）"
        fi
        read -rp "请输入：" NodeType
        case "$NodeType" in
            1 ) NodeType="shadowsocks" ;;
            2 ) NodeType="vless" ;;
            3 ) NodeType="vmess" ;;
            4 ) NodeType="trojan" ;;
            5 ) NodeType="hysteria2" ;;
            6 ) NodeType="anytls" ;;
            * ) NodeType="shadowsocks" ;;
        esac
    fi

    # TLS/Reality 配置
    isreality=""
    istls=""
    enable_tfo=true
    if [ "$NodeType" == "vless" ]; then
        read -rp "请选择是否为reality节点？(y/n)" isreality
    elif [ "$NodeType" == "hysteria2" ]; then
        enable_tfo=false
        istls="y"
    elif [ "$NodeType" == "anytls" ]; then
        # AnyTLS 协议建立在 TLS 之上，必须配置证书，这里强制走 TLS 流程
        istls="y"
    fi

    if [[ "$isreality" != "y" && "$isreality" != "Y" && "$istls" != "y" ]]; then
        read -rp "请选择是否进行TLS配置？(y/n)" istls
    fi

    certmode="none"
    certdomain="example.com"
    if [[ "$isreality" != "y" && "$isreality" != "Y" && ( "$istls" == "y" || "$istls" == "Y" ) ]]; then
        echo -e "${yellow}请选择证书申请模式：${plain}"
        echo -e "${green}1. http模式自动申请，节点域名已正确解析${plain}"
        echo -e "${green}2. dns模式自动申请，需填入正确域名服务商API参数${plain}"
        echo -e "${green}3. file模式，自签证书或提供已有证书文件${plain}"
        read -rp "请输入：" certmode
        case "$certmode" in
            1 ) certmode="http" ;;
            2 ) certmode="dns" ;;
            3 ) certmode="file" ;;
        esac
        read -rp "请输入节点证书域名(example.com)：" certdomain
        if [ "$certmode" == "dns" ]; then
            echo -e "${red}请在配置生成后手动修改 DNSEnv 参数，然后重启V2bX！${plain}"
        fi
    fi

    node_config=""
    if [ "$core_type" == "1" ]; then
        # Xray 节点配置 - 使用正确的 JSON tag 字段名
        node_config=$(cat <<EOF
{
            "Core": "$core",
            "ApiHost": "$ApiHost",
            "ApiKey": "$ApiKey",
            "NodeID": $NodeID,
            "NodeType": "$NodeType",
            "Timeout": 30,
            "ApiVersion": $api_version,
            "ListenIP": "0.0.0.0",
            "SendIP": "0.0.0.0",
            "DeviceOnlineMinTraffic": 200,
            "ReportMinTraffic": 0,
            "EnableProxyProtocol": false,
            "EnableUot": true,
            "EnableTFO": true,
            "DNSType": "UseIPv4",
            "DisableSniffing": false,
            "CertConfig": {
                "CertMode": "$certmode",
                "RejectUnknownSni": false,
                "CertDomain": "$certdomain",
                "CertFile": "/etc/V2bX/${certdomain}.cert.pem",
                "KeyFile": "/etc/V2bX/${certdomain}.key.pem",
                "Email": "v2bx@github.com",
                "Provider": "cloudflare",
                "DNSEnv": {
                    "EnvName": "env1"
                }
            }
        },
EOF
)
    elif [ "$core_type" == "2" ]; then
        # Hysteria2 节点配置
        node_config=$(cat <<EOF
{
            "Core": "$core",
            "ApiHost": "$ApiHost",
            "ApiKey": "$ApiKey",
            "NodeID": $NodeID,
            "NodeType": "$NodeType",
            "Hysteria2ConfigPath": "/etc/V2bX/hy2config.yaml",
            "Timeout": 30,
            "ApiVersion": $api_version,
            "ListenIP": "",
            "SendIP": "0.0.0.0",
            "DeviceOnlineMinTraffic": 200,
            "ReportMinTraffic": 0,
            "CertConfig": {
                "CertMode": "$certmode",
                "RejectUnknownSni": false,
                "CertDomain": "$certdomain",
                "CertFile": "/etc/V2bX/${certdomain}.cert.pem",
                "KeyFile": "/etc/V2bX/${certdomain}.key.pem",
                "Email": "v2bx@github.com",
                "Provider": "cloudflare",
                "DNSEnv": {
                    "EnvName": "env1"
                }
            }
        },
EOF
)
    elif [ "$core_type" == "3" ]; then
        # Sing-box 节点配置
        node_config=$(cat <<EOF
{
            "Core": "$core",
            "ApiHost": "$ApiHost",
            "ApiKey": "$ApiKey",
            "NodeID": $NodeID,
            "NodeType": "$NodeType",
            "Timeout": 30,
            "ApiVersion": $api_version,
            "ListenIP": "0.0.0.0",
            "SendIP": "0.0.0.0",
            "DeviceOnlineMinTraffic": 200,
            "ReportMinTraffic": 0,
            "EnableTFO": false,
            "EnableSniff": true,
            "SniffOverrideDestination": true,
            "CertConfig": {
                "CertMode": "$certmode",
                "RejectUnknownSni": false,
                "CertDomain": "$certdomain",
                "CertFile": "/etc/V2bX/${certdomain}.cert.pem",
                "KeyFile": "/etc/V2bX/${certdomain}.key.pem",
                "Email": "v2bx@github.com",
                "Provider": "cloudflare",
                "DNSEnv": {
                    "EnvName": "env1"
                }
            }
        },
EOF
)
    fi
    nodes_config+=("$node_config")
}

generate_config_file() {
    echo -e "${yellow}V2bX 配置文件生成向导${plain}"
    echo -e "${red}请阅读以下注意事项：${plain}"
    echo -e "${red}1. 生成的配置文件会保存到 /etc/V2bX/config.json${plain}"
    echo -e "${red}2. 原来的配置文件会保存到 /etc/V2bX/config.json.bak${plain}"
    echo -e "${red}3. 支持 Xray / Hysteria2 / Sing-box 核心${plain}"
    echo -e "${red}4. Xray 核心已内置高性能连接参数优化${plain}"
    echo -e "${red}5. 使用此功能生成的配置文件会自带审计规则，确定继续？(y/n)${plain}"
    read -rp "请输入：" continue_prompt
    if [[ "$continue_prompt" =~ ^[Nn][Oo]? ]]; then
        return 0
    fi

    # jq 是生成路由规则用的。V2bX.sh 那边已经有这一步，
    # initconfig.sh 当初漏了，导致全新安装（install.sh source 本文件）时
    # 机器上没有 jq 就会一路报 "jq: command not found"，route.json 生成失败。
    # 装不上也不致命：write_default_route_json 会回落到内置静态规则集。
    if ! ensure_jq; then
        echo -e "${yellow}jq 安装失败，路由规则将使用内置静态规则集（不做 geosite 分类裁剪）${plain}"
    fi

    nodes_config=()
    first_node=true
    core_xray=false
    core_hysteria2=false
    fixed_api_info=false
    fixed_api_version=""

    while true; do
        if [ "$first_node" = true ]; then
            read -rp "请输入机场网址(https://example.com)：" ApiHost
            read -rp "请输入面板对接API Key：" ApiKey
            read -rp "是否设置固定的机场网址和API Key？(y/n)" fixed_api
            if [ "$fixed_api" = "y" ] || [ "$fixed_api" = "Y" ]; then
                fixed_api_info=true
                echo -e "${green}成功固定地址${plain}"
            fi
            first_node=false
            add_node_config
        else
            read -rp "是否继续添加节点配置？(回车继续，输入n或no退出)" continue_adding_node
            if [[ "$continue_adding_node" =~ ^[Nn][Oo]? ]]; then
                break
            elif [ "$fixed_api_info" = false ]; then
                read -rp "请输入机场网址(https://example.com)：" ApiHost
                read -rp "请输入面板对接API Key：" ApiKey
            fi
            add_node_config
        fi
    done

    # 初始化核心配置数组
    cores_config="["

    # Xray 核心配置 - 带高性能连接参数优化
    if [ "$core_xray" = true ]; then
        cores_config+="
    {
        \"Type\": \"xray\",
        \"Log\": {
            \"Level\": \"error\",
            \"ErrorPath\": \"/etc/V2bX/error.log\"
        },
        \"AssetPath\": \"/etc/V2bX/\",
        \"DnsConfigPath\": \"/etc/V2bX/dns.json\",
        \"OutboundConfigPath\": \"/etc/V2bX/custom_outbound.json\",
        \"RouteConfigPath\": \"/etc/V2bX/route.json\",
        \"XrayConnectionConfig\": {
            \"handshake\": 10,
            \"connIdle\": 300,
            \"uplinkOnly\": 2,
            \"downlinkOnly\": 4,
            \"bufferSize\": 256
        }
    },"
    fi

    # Hysteria2 核心配置
    if [ "$core_hysteria2" = true ]; then
        cores_config+="
    {
        \"Type\": \"hysteria2\",
        \"Log\": {
            \"Level\": \"error\"
        }
    },"
    fi

    # Sing-box 核心配置
    if [ "$core_sing" = true ]; then
        cores_config+="
    {
        \"Type\": \"sing\",
        \"Log\": {
            \"Disable\": false,
            \"Level\": \"error\",
            \"Timestamp\": true
        },
        \"NTP\": {
            \"Enable\": false,
            \"Server\": \"time.apple.com\",
            \"ServerPort\": 0
        }
    },"
    fi

    # 移除最后一个逗号并关闭数组
    cores_config+="]"
    cores_config=$(echo "$cores_config" | sed 's/},]$/}]/')

    # 切换到配置文件目录
    cd /etc/V2bX

    # 备份旧的配置文件
    if [ -f config.json ]; then
        mv config.json config.json.bak
    fi
    nodes_config_str="${nodes_config[*]}"
    formatted_nodes_config="${nodes_config_str%,}"

    # 创建 config.json 文件
    cat <<EOF > /etc/V2bX/config.json
{
    "Log": {
        "Level": "error",
        "Output": ""
    },
    "Cores": $cores_config,
    "Nodes": [$formatted_nodes_config]
}
EOF

    # 创建 dns.json 文件 (Xray DNS)
    cat <<'EOF' > /etc/V2bX/dns.json
{
    "servers": [
        "1.1.1.1",
        "8.8.8.8",
        "localhost"
    ],
    "tag": "dns_inbound"
}
EOF

    # 创建 custom_outbound.json 文件
    cat <<'EOF' > /etc/V2bX/custom_outbound.json
[
    {
        "tag": "IPv4_out",
        "protocol": "freedom",
        "settings": {
            "domainStrategy": "UseIPv4v6"
        }
    },
    {
        "tag": "IPv6_out",
        "protocol": "freedom",
        "settings": {
            "domainStrategy": "UseIPv6"
        }
    },
    {
        "protocol": "blackhole",
        "tag": "block"
    }
]
EOF

    # 生成 route.json。和菜单「更新路由禁止规则」共用 build_block_rules，
    # 两个入口的防护强度完全一致，不会再出现「生成的配置比更新后的弱」。
    if ! write_default_route_json /etc/V2bX/route.json; then
        echo -e "${red}生成 route.json 失败！xray 内核会因为读不到路由文件而无法启动。${plain}"
        echo -e "${red}请手动安装 jq 后执行 V2bX routerule 重新生成：${plain}"
        echo -e "${red}  Debian/Ubuntu: apt-get install -y jq   RHEL 系: dnf install -y jq   Alpine: apk add jq${plain}"
    fi

    # 生成 hy2config.yaml。ACL 与菜单「更新路由禁止规则」同源。
    write_default_hy2config /etc/V2bX/hy2config.yaml
    echo -e "${green}V2bX 配置文件生成完成，正在重新启动 V2bX 服务${plain}"
    # 判断是否有 restart 函数（从 V2bX.sh 调用时有，从 install.sh 调用时没有）
    if type restart >/dev/null 2>&1; then
        restart 0
    else
        # 从 install.sh source 调用，直接用系统命令重启
        if [[ -f /etc/init.d/V2bX ]]; then
            service V2bX restart
        else
            systemctl restart V2bX
        fi
        sleep 2
        echo -e "${green}V2bX 重启完成${plain}"
    fi
    if type before_show_menu >/dev/null 2>&1; then
        before_show_menu
    fi
}
