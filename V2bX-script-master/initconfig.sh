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
        read -rp "请输入：" NodeType
        case "$NodeType" in
            1 ) NodeType="shadowsocks" ;;
            2 ) NodeType="vless" ;;
            3 ) NodeType="vmess" ;;
            4 ) NodeType="trojan" ;;
            5 ) NodeType="hysteria2" ;;
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

    # 创建 route.json 文件
    cat <<'EOF' > /etc/V2bX/route.json
{
    "domainStrategy": "AsIs",
    "rules": [
        {
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "geoip:private"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "domain": [
                "regexp:(api|ps|sv|offnavi|newvector|ulog\\.imap|newloc)(\\.map|)\\.(baidu|n\\.shifen)\\.com",
                "regexp:(.+\\.|^)(360|so)\\.(cn|com)",
                "regexp:(Subject|HELO|SMTP)",
                "regexp:(torrent|\\.torrent|peer_id=|info_hash|get_peers|find_node|BitTorrent|announce_peer|announce\\.php\\?passkey=)",
                "regexp:(^.@)(guerrillamail|guerrillamailblock|sharklasers|grr|pokemail|spam4|bccto|chacuo|027168)\\.(info|biz|com|de|net|org|me|la)",
                "regexp:(.?)(xunlei|sandai|Thunder|XLLiveUD)(.)",
                "regexp:(ed2k|\\.torrent|peer_id=|announce|info_hash|get_peers|find_node|BitTorrent|announce_peer|announce\\.php\\?passkey=|magnet:|xunlei|sandai|Thunder|XLLiveUD|bt_key)",
                "regexp:(.+\\.|^)(360)\\.(cn|com|net)",
                "regexp:(.*\\.||)(guanjia\\.qq\\.com|qqpcmgr|QQPCMGR)",
                "regexp:(.*\\.||)(rising|kingsoft|duba|xindubawukong|jinshanduba)\\.(com|net|org)",
                "regexp:(.*\\.||)(netvigator|torproject)\\.(com|cn|net|org)",
                "regexp:(.*\\.||)(miaozhen|cnzz|talkingdata|umeng)\\.(cn|com)",
                "regexp:(.*\\.||)(taobao)\\.(com)",
                "regexp:(.*\\.||)(laomoe|jiyou|ssss|lolicp|vv1234|0z|4321q|868123|ksweb|mm126)\\.(com|cloud|fun|cn|gs|xyz|cc)",
                "regexp:(flows|miaoko)\\.(pages)\\.(dev)"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "ip": [
                "127.0.0.1/32",
                "10.0.0.0/8",
                "fc00::/7",
                "fe80::/10",
                "172.16.0.0/12"
            ]
        },
        {
            "type": "field",
            "outboundTag": "block",
            "protocol": [
                "bittorrent"
            ]
        },
        {
            "type": "field",
            "outboundTag": "IPv4_out",
            "network": "udp,tcp"
        }
    ]
}
EOF

    # 创建 hy2config.yaml 文件
    cat <<'EOF' > /etc/V2bX/hy2config.yaml
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
acl:
  inline:
    - direct(geosite:google)
    - reject(geosite:cn)
    - reject(geoip:cn)
masquerade:
  type: 404
EOF
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
