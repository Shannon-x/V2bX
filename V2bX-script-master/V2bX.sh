#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

# check root
[[ $EUID -ne 0 ]] && echo -e "${red}错误: ${plain} 必须使用root用户运行此脚本！\n" && exit 1

# check os
if [[ -f /etc/redhat-release ]]; then
    release="centos"
elif cat /etc/issue | grep -Eqi "alpine"; then
    release="alpine"
elif cat /etc/issue | grep -Eqi "debian"; then
    release="debian"
elif cat /etc/issue | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /etc/issue | grep -Eqi "centos|red hat|redhat|rocky|alma|oracle linux"; then
    release="centos"
elif cat /proc/version | grep -Eqi "debian"; then
    release="debian"
elif cat /proc/version | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /proc/version | grep -Eqi "centos|red hat|redhat|rocky|alma|oracle linux"; then
    release="centos"
elif cat /proc/version | grep -Eqi "arch"; then
    release="arch"
else
    echo -e "${red}未检测到系统版本，请联系脚本作者！${plain}\n" && exit 1
fi

# os version
if [[ -f /etc/os-release ]]; then
    os_version=$(awk -F'[= ."]' '/VERSION_ID/{print $3}' /etc/os-release)
fi
if [[ -z "$os_version" && -f /etc/lsb-release ]]; then
    os_version=$(awk -F'[= ."]+' '/DISTRIB_RELEASE/{print $2}' /etc/lsb-release)
fi

if [[ x"${release}" == x"centos" ]]; then
    if [[ ${os_version} -le 6 ]]; then
        echo -e "${red}请使用 CentOS 7 或更高版本的系统！${plain}\n" && exit 1
    fi
    if [[ ${os_version} -eq 7 ]]; then
        echo -e "${red}注意： CentOS 7 无法使用hysteria1/2协议！${plain}\n"
    fi
elif [[ x"${release}" == x"ubuntu" ]]; then
    if [[ ${os_version} -lt 16 ]]; then
        echo -e "${red}请使用 Ubuntu 16 或更高版本的系统！${plain}\n" && exit 1
    fi
elif [[ x"${release}" == x"debian" ]]; then
    if [[ ${os_version} -lt 8 ]]; then
        echo -e "${red}请使用 Debian 8 或更高版本的系统！${plain}\n" && exit 1
    fi
fi

# 检查系统是否有 IPv6 地址
check_ipv6_support() {
    if ip -6 addr | grep -q "inet6"; then
        echo "1"
    else
        echo "0"
    fi
}

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -rp "$1 [默认$2]: " temp
        if [[ x"${temp}" == x"" ]]; then
            temp=$2
        fi
    else
        read -rp "$1 [y/n]: " temp
    fi
    if [[ x"${temp}" == x"y" || x"${temp}" == x"Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "是否重启V2bX" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}按回车返回主菜单: ${plain}" && read temp
    show_menu
}

install() {
    bash <(curl -Ls https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh)
    if [[ $? == 0 ]]; then
        if [[ $# == 0 ]]; then
            start
        else
            start 0
        fi
    fi
}

update() {
    if [[ $# == 0 ]]; then
        echo && echo -n -e "输入指定版本(默认最新版，输入 latest 使用滚动构建版): " && read version
    else
        version=$2
    fi
    bash <(curl -Ls https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh) $version
    if [[ $? == 0 ]]; then
        echo -e "${green}更新完成，已自动重启 V2bX，请使用 V2bX log 查看运行日志${plain}"
        exit
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

config() {
    echo "V2bX在修改配置后会自动尝试重启"
    vi /etc/V2bX/config.json
    sleep 2
    restart
    check_status
    case $? in
        0)
            echo -e "V2bX状态: ${green}已运行${plain}"
            ;;
        1)
            echo -e "检测到您未启动V2bX或V2bX自动重启失败，是否查看日志？[Y/n]" && echo
            read -e -rp "(默认: y):" yn
            [[ -z ${yn} ]] && yn="y"
            if [[ ${yn} == [Yy] ]]; then
               show_log
            fi
            ;;
        2)
            echo -e "V2bX状态: ${red}未安装${plain}"
    esac
}

uninstall() {
    confirm "确定要卸载 V2bX 吗?" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    if [[ x"${release}" == x"alpine" ]]; then
        service V2bX stop
        rc-update del V2bX
        rm /etc/init.d/V2bX -f
    else
        systemctl stop V2bX
        systemctl disable V2bX
        rm /etc/systemd/system/V2bX.service -f
        systemctl daemon-reload
        systemctl reset-failed
    fi
    rm /etc/V2bX/ -rf
    rm /usr/local/V2bX/ -rf

    echo ""
    echo -e "卸载成功，如果你想删除此脚本，则退出脚本后运行 ${green}rm /usr/bin/V2bX -f${plain} 进行删除"
    echo ""

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

start() {
    check_status
    if [[ $? == 0 ]]; then
        echo ""
        echo -e "${green}V2bX已运行，无需再次启动，如需重启请选择重启${plain}"
    else
        if [[ x"${release}" == x"alpine" ]]; then
            service V2bX start
        else
            systemctl start V2bX
        fi
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            echo -e "${green}V2bX 启动成功，请使用 V2bX log 查看运行日志${plain}"
        else
            echo -e "${red}V2bX可能启动失败，请稍后使用 V2bX log 查看日志信息${plain}"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    if [[ x"${release}" == x"alpine" ]]; then
        service V2bX stop
    else
        systemctl stop V2bX
    fi
    sleep 2
    check_status
    if [[ $? == 1 ]]; then
        echo -e "${green}V2bX 停止成功${plain}"
    else
        echo -e "${red}V2bX停止失败，可能是因为停止时间超过了两秒，请稍后查看日志信息${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart() {
    if [[ x"${release}" == x"alpine" ]]; then
        service V2bX restart
    else
        systemctl restart V2bX
    fi
    sleep 2
    check_status
    if [[ $? == 0 ]]; then
        echo -e "${green}V2bX 重启成功，请使用 V2bX log 查看运行日志${plain}"
    else
        echo -e "${red}V2bX可能启动失败，请稍后使用 V2bX log 查看日志信息${plain}"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    if [[ x"${release}" == x"alpine" ]]; then
        service V2bX status
    else
        systemctl status V2bX --no-pager -l
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    if [[ x"${release}" == x"alpine" ]]; then
        rc-update add V2bX
    else
        systemctl enable V2bX
    fi
    if [[ $? == 0 ]]; then
        echo -e "${green}V2bX 设置开机自启成功${plain}"
    else
        echo -e "${red}V2bX 设置开机自启失败${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    if [[ x"${release}" == x"alpine" ]]; then
        rc-update del V2bX
    else
        systemctl disable V2bX
    fi
    if [[ $? == 0 ]]; then
        echo -e "${green}V2bX 取消开机自启成功${plain}"
    else
        echo -e "${red}V2bX 取消开机自启失败${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    local access_log="/var/log/V2bX/access.log"
    echo -e "请选择要查看的日志:"
    echo -e "  ${green}1.${plain} 连接日志 - 实时跟随最新记录 (Ctrl+C 退出)"
    echo -e "  ${green}2.${plain} 连接日志 - 浏览历史, 从最新一条开始往回翻 (PageUp 上翻, q 退出)"
    echo -e "  ${green}3.${plain} 服务运行日志 (启动/报错等)"
    read -rp "请输入选择 [默认1]: " log_type
    case "$log_type" in
    3)
        if [[ x"${release}" == x"alpine" ]]; then
            if [[ -f /var/log/V2bX.log ]]; then
                tail -n 100 -f /var/log/V2bX.log
            else
                echo -e "${red}日志文件不存在，请先启动 V2bX${plain}"
            fi
        else
            journalctl -u V2bX.service -e --no-pager -f
        fi
        ;;
    2)
        if [[ -f ${access_log} ]]; then
            echo -e "${yellow}已定位到最新一条, PageUp/↑ 向前翻旧记录, q 退出${plain}"
            echo -e "${yellow}按日期检索历史: zgrep '2026/07/06' /var/log/V2bX/access*.log*${plain}"
            if command -v less >/dev/null 2>&1; then
                less +G ${access_log}
            else
                tail -n 200 ${access_log}
            fi
        else
            show_access_log_missing_hint
        fi
        ;;
    *)
        if [[ -f ${access_log} ]]; then
            echo -e "${yellow}最新日志在最下方实时滚动, Ctrl+C 退出${plain}"
            tail -n 50 -f ${access_log}
        else
            show_access_log_missing_hint
        fi
        ;;
    esac
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_access_log_missing_hint() {
    echo -e "${red}/var/log/V2bX/access.log 不存在${plain}"
    echo -e "${yellow}请检查:${plain}"
    echo -e "  1) V2bX 是否已更新到 v1.1.202607060806 及以上并重启 (v2bx update)"
    echo -e "  2) 配置中 Xray 内核的 Log.AccessPath 是否被设为 none/console"
    echo -e "  3) 服务运行日志中是否有 'Access log file not writable' 告警"
}

install_bbr() {
    bash <(curl -L -s https://github.com/ylx2016/Linux-NetSpeed/raw/master/tcpx.sh)
}

update_shell() {
    local shell_url="https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/V2bX.sh"
    if [[ x"${release}" == x"alpine" ]]; then
        curl -L -o /usr/bin/V2bX --retry 3 --retry-delay 2 "${shell_url}"
    else
        wget -O /usr/bin/V2bX -N --no-check-certificate "${shell_url}"
    fi
    if [[ $? != 0 ]]; then
        echo ""
        echo -e "${red}下载脚本失败，请检查本机能否连接 Github${plain}"
        before_show_menu
    else
        chmod +x /usr/bin/V2bX
        echo -e "${green}升级脚本成功，请重新运行脚本${plain}" && exit 0
    fi
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ ! -f /usr/local/V2bX/V2bX ]]; then
        return 2
    fi
    if [[ x"${release}" == x"alpine" ]]; then
        temp=$(service V2bX status | awk '{print $3}')
        if [[ x"${temp}" == x"started" ]]; then
            return 0
        else
            return 1
        fi
    else
        temp=$(systemctl status V2bX | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
        if [[ x"${temp}" == x"running" ]]; then
            return 0
        else
            return 1
        fi
    fi
}

check_enabled() {
    if [[ x"${release}" == x"alpine" ]]; then
        temp=$(rc-update show | grep V2bX)
        if [[ x"${temp}" == x"" ]]; then
            return 1
        else
            return 0
        fi
    else
        temp=$(systemctl is-enabled V2bX)
        if [[ x"${temp}" == x"enabled" ]]; then
            return 0
        else
            return 1;
        fi
    fi
}

check_uninstall() {
    check_status
    if [[ $? != 2 ]]; then
        echo ""
        echo -e "${red}V2bX已安装，请不要重复安装${plain}"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

check_install() {
    check_status
    if [[ $? == 2 ]]; then
        echo ""
        echo -e "${red}请先安装V2bX${plain}"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

show_status() {
    check_status
    case $? in
        0)
            echo -e "V2bX状态: ${green}已运行${plain}"
            show_enable_status
            ;;
        1)
            echo -e "V2bX状态: ${yellow}未运行${plain}"
            show_enable_status
            ;;
        2)
            echo -e "V2bX状态: ${red}未安装${plain}"
    esac
}

show_enable_status() {
    check_enabled
    if [[ $? == 0 ]]; then
        echo -e "是否开机自启: ${green}是${plain}"
    else
        echo -e "是否开机自启: ${red}否${plain}"
    fi
}

generate_x25519_key() {
    echo -n "正在生成 x25519 密钥："
    /usr/local/V2bX/V2bX x25519
    echo ""
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_V2bX_version() {
    echo -n "V2bX 版本："
    /usr/local/V2bX/V2bX version
    echo ""
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

# =====================================================
# 配置文件生成逻辑（内联版本，与 initconfig.sh 保持一致）
# =====================================================

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

    # 安全模式配置
    isreality=""
    istls=""
    enable_tfo=true
    certmode="none"
    certdomain="example.com"

    if [ "$NodeType" == "vless" ]; then
        echo -e "${yellow}请选择 VLESS 安全模式：${plain}"
        echo -e "${green}1. Reality${plain}（推荐，无需本地证书，由面板下发 Reality 参数）"
        echo -e "${green}2. TLS${plain}（需要本地配置 TLS 证书）"
        echo -e "${green}3. 无 TLS${plain}（适用于 VLESS Encryption 或纯 VLESS，加密由面板控制）"
        read -rp "请输入 [默认1]：" vless_security
        case "$vless_security" in
            2 )
                istls="y"
                ;;
            3 )
                # 无 TLS，CertMode 保持 none
                ;;
            * )
                isreality="y"
                ;;
        esac
    elif [ "$NodeType" == "vmess" ] || [ "$NodeType" == "shadowsocks" ]; then
        read -rp "是否配置 TLS 证书？(y/n)" istls
    elif [ "$NodeType" == "trojan" ]; then
        istls="y"
    elif [ "$NodeType" == "hysteria2" ]; then
        enable_tfo=false
        istls="y"
    fi

    if [[ "$istls" == "y" || "$istls" == "Y" ]]; then
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

    ipv6_support=$(check_ipv6_support)
    listen_ip="0.0.0.0"
    if [ "$ipv6_support" -eq 1 ]; then
        listen_ip="::"
    fi

    node_config=""
    if [ "$core_type" == "1" ]; then
        # Xray 节点配置
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

    # Xray 核心配置 - 带高性能连接参数
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

    # 创建 dns.json (Xray DNS)
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
                "regexp:(ed2k|\\.torrent|peer_id=|announce|info_hash|get_peers|find_node|BitTorrent|announce_peer|announce\\.php\\?passkey=|magnet:|xunlei|sandai|Thunder|XLLiveUD|bt_key)",
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
    restart 0
    before_show_menu
}

# =====================================================
# 增加节点（向现有 config.json 追加）
# =====================================================
add_single_node() {
    if [[ ! -f /etc/V2bX/config.json ]]; then
        echo -e "${red}未找到配置文件 /etc/V2bX/config.json，请先使用"生成配置文件"功能${plain}"
        before_show_menu
        return
    fi

    echo -e "${yellow}=== 向现有配置添加节点 ===${plain}"

    nodes_config=()
    core_xray=false
    core_hysteria2=false
    fixed_api_info=false
    fixed_api_version=""

    # 读取现有配置中的 ApiHost/ApiKey 作为默认值
    existing_apihost=$(grep -o '"ApiHost"[[:space:]]*:[[:space:]]*"[^"]*"' /etc/V2bX/config.json | head -1 | sed 's/.*"\([^"]*\)"$/\1/')
    existing_apikey=$(grep -o '"ApiKey"[[:space:]]*:[[:space:]]*"[^"]*"' /etc/V2bX/config.json | head -1 | sed 's/.*"\([^"]*\)"$/\1/')

    if [ -n "$existing_apihost" ]; then
        read -rp "请输入机场网址 [默认: ${existing_apihost}]：" ApiHost
        if [ -z "$ApiHost" ]; then
            ApiHost="$existing_apihost"
        fi
    else
        read -rp "请输入机场网址(https://example.com)：" ApiHost
    fi
    if [ -n "$existing_apikey" ]; then
        read -rp "请输入面板对接API Key [默认: ${existing_apikey}]：" ApiKey
        if [ -z "$ApiKey" ]; then
            ApiKey="$existing_apikey"
        fi
    else
        read -rp "请输入面板对接API Key：" ApiKey
    fi

    add_node_config

    if [ ${#nodes_config[@]} -eq 0 ]; then
        echo -e "${red}没有添加任何节点配置${plain}"
        before_show_menu
        return
    fi

    # 确认已有的核心配置
    need_add_core=""
    if [ "$core_xray" = true ]; then
        if ! grep -q '"Type"[[:space:]]*:[[:space:]]*"xray"' /etc/V2bX/config.json; then
            need_add_core="xray"
        fi
    fi

    if [ "$core_hysteria2" = true ]; then
        if ! grep -q '"Type"[[:space:]]*:[[:space:]]*"hysteria2"' /etc/V2bX/config.json; then
            need_add_core="${need_add_core} hysteria2"
        fi
    fi
    if [ -n "$need_add_core" ]; then
        echo -e "${yellow}警告：新节点使用的核心 (${need_add_core}) 未在现有配置中找到，请确认 Cores 配置中已包含对应核心${plain}"
    fi

    # 使用 python3/python 来安全地追加 JSON 节点
    local py_cmd=""
    if command -v python3 &>/dev/null; then
        py_cmd="python3"
    elif command -v python &>/dev/null; then
        py_cmd="python"
    fi

    if [ -n "$py_cmd" ]; then
        # 去除节点配置末尾的逗号
        local new_node="${nodes_config[0]}"
        new_node="${new_node%,}"

        cp /etc/V2bX/config.json /etc/V2bX/config.json.bak
        $py_cmd -c "
import json, sys
try:
    with open('/etc/V2bX/config.json', 'r') as f:
        config = json.load(f)
    new_node = json.loads('''${new_node}''')
    if 'Nodes' not in config:
        config['Nodes'] = []
    config['Nodes'].append(new_node)
    with open('/etc/V2bX/config.json', 'w') as f:
        json.dump(config, f, indent=4, ensure_ascii=False)
    print('OK')
except Exception as e:
    print(f'ERROR: {e}', file=sys.stderr)
    sys.exit(1)
"
        if [[ $? -eq 0 ]]; then
            echo -e "${green}节点添加成功！${plain}"
            echo -e "${yellow}正在重启 V2bX...${plain}"
            restart 0
        else
            echo -e "${red}节点添加失败，已恢复备份${plain}"
            mv /etc/V2bX/config.json.bak /etc/V2bX/config.json
        fi
    else
        echo -e "${red}未找到 python3/python，无法安全操作 JSON。请手动编辑 /etc/V2bX/config.json${plain}"
    fi
    before_show_menu
}

# =====================================================
# 删除节点
# =====================================================
delete_node() {
    if [[ ! -f /etc/V2bX/config.json ]]; then
        echo -e "${red}未找到配置文件 /etc/V2bX/config.json${plain}"
        before_show_menu
        return
    fi

    local py_cmd=""
    if command -v python3 &>/dev/null; then
        py_cmd="python3"
    elif command -v python &>/dev/null; then
        py_cmd="python"
    fi

    if [ -z "$py_cmd" ]; then
        echo -e "${red}未找到 python3/python，无法安全操作 JSON。请手动编辑 /etc/V2bX/config.json${plain}"
        before_show_menu
        return
    fi

    echo -e "${yellow}=== 当前已配置的节点 ===${plain}"
    $py_cmd -c "
import json
try:
    with open('/etc/V2bX/config.json', 'r') as f:
        config = json.load(f)
    nodes = config.get('Nodes', [])
    if not nodes:
        print('未找到任何节点配置')
    else:
        for i, node in enumerate(nodes):
            core = node.get('Core', '未知')
            node_type = node.get('NodeType', '未知')
            node_id = node.get('NodeID', '未知')
            api_host = node.get('ApiHost', '未知')
            print(f'  {i+1}. [{core}] {node_type} | NodeID: {node_id} | {api_host}')
        print(f'\n共 {len(nodes)} 个节点')
except Exception as e:
    print(f'读取配置失败: {e}')
"
    echo ""
    read -rp "请输入要删除的节点编号（输入 0 取消）：" del_index

    if [[ "$del_index" == "0" ]] || [[ -z "$del_index" ]]; then
        echo "已取消"
        before_show_menu
        return
    fi

    cp /etc/V2bX/config.json /etc/V2bX/config.json.bak
    $py_cmd -c "
import json, sys
try:
    with open('/etc/V2bX/config.json', 'r') as f:
        config = json.load(f)
    nodes = config.get('Nodes', [])
    idx = int('${del_index}') - 1
    if idx < 0 or idx >= len(nodes):
        print('错误：编号超出范围', file=sys.stderr)
        sys.exit(1)
    removed = nodes.pop(idx)
    config['Nodes'] = nodes
    with open('/etc/V2bX/config.json', 'w') as f:
        json.dump(config, f, indent=4, ensure_ascii=False)
    core = removed.get('Core', '未知')
    ntype = removed.get('NodeType', '未知')
    nid = removed.get('NodeID', '未知')
    print(f'已删除节点: [{core}] {ntype} NodeID:{nid}')
except Exception as e:
    print(f'ERROR: {e}', file=sys.stderr)
    sys.exit(1)
"
    if [[ $? -eq 0 ]]; then
        echo -e "${green}节点删除成功！${plain}"
        echo -e "${yellow}正在重启 V2bX...${plain}"
        restart 0
    else
        echo -e "${red}节点删除失败，已恢复备份${plain}"
        mv /etc/V2bX/config.json.bak /etc/V2bX/config.json
    fi
    before_show_menu
}

# 放开防火墙端口
open_ports() {
    systemctl stop firewalld.service 2>/dev/null
    systemctl disable firewalld.service 2>/dev/null
    setenforce 0 2>/dev/null
    ufw disable 2>/dev/null
    iptables -P INPUT ACCEPT 2>/dev/null
    iptables -P FORWARD ACCEPT 2>/dev/null
    iptables -P OUTPUT ACCEPT 2>/dev/null
    iptables -t nat -F 2>/dev/null
    iptables -t mangle -F 2>/dev/null
    iptables -F 2>/dev/null
    iptables -X 2>/dev/null
    netfilter-persistent save 2>/dev/null
    echo -e "${green}放开防火墙端口成功！${plain}"
}

show_usage() {
    echo "V2bX 管理脚本使用方法: "
    echo "------------------------------------------"
    echo "V2bX              - 显示管理菜单 (功能更多)"
    echo "V2bX start        - 启动 V2bX"
    echo "V2bX stop         - 停止 V2bX"
    echo "V2bX restart      - 重启 V2bX"
    echo "V2bX status       - 查看 V2bX 状态"
    echo "V2bX enable       - 设置 V2bX 开机自启"
    echo "V2bX disable      - 取消 V2bX 开机自启"
    echo "V2bX log          - 查看 V2bX 日志"
    echo "V2bX x25519       - 生成 x25519 密钥"
    echo "V2bX generate     - 生成 V2bX 配置文件"
    echo "V2bX update       - 更新 V2bX"
    echo "V2bX update x.x.x - 安装 V2bX 指定版本"
    echo "V2bX install      - 安装 V2bX"
    echo "V2bX uninstall    - 卸载 V2bX"
    echo "V2bX version      - 查看 V2bX 版本"
    echo "V2bX addnode      - 添加节点"
    echo "V2bX delnode      - 删除节点"
    echo "V2bX routerule    - 更新路由禁止规则"
    echo "------------------------------------------"
}

ensure_jq() {
    if command -v jq >/dev/null 2>&1; then
        return 0
    fi
    echo -e "${yellow}正在安装 jq (用于安全修改 JSON)...${plain}"
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

# 只替换 route.json 里的“禁止规则”这一块 (outboundTag==block)，
# 其它分流规则 (warp / 流媒体分流 / 自定义出站 / final / domainStrategy) 全部保留不动。
update_route_block_rules() {
    if ! ensure_jq; then
        echo -e "${red}jq 安装失败，为避免破坏配置已取消。请手动安装 jq 后重试${plain}"
        before_show_menu
        return
    fi

    # 从 config.json 读取实际的 RouteConfigPath，没有则用默认路径
    local route_file
    route_file=$(jq -r '(.Cores[]? | select(.Type=="xray") | .RouteConfigPath) // empty' /etc/V2bX/config.json 2>/dev/null | head -n1)
    [[ -z "${route_file}" ]] && route_file="/etc/V2bX/route.json"

    if [[ ! -f "${route_file}" ]]; then
        echo -e "${red}未找到路由文件 ${route_file}${plain}"
        before_show_menu
        return
    fi
    if ! jq empty "${route_file}" >/dev/null 2>&1; then
        echo -e "${red}${route_file} 不是合法 JSON，已取消（请先修复该文件）${plain}"
        before_show_menu
        return
    fi

    # 新的禁止规则块（严格禁 BT/PT + 25端口 + 内网 + 广告 + 杀软 + 竞品机场 + 反诈/滥用域名）
    local new_blocks
    new_blocks=$(cat <<'JSON'
[
  { "ruleTag": "block-bt-proto", "type": "field", "outboundTag": "block", "protocol": ["bittorrent"] },
  { "ruleTag": "block-bt-pt-tracker", "type": "field", "outboundTag": "block",
    "domain": ["geosite:category-public-tracker", "geosite:category-pt", "geosite:category-ipfs"] },
  { "ruleTag": "block-smtp", "type": "field", "outboundTag": "block", "port": 25 },
  { "ruleTag": "block-private", "type": "field", "outboundTag": "block", "ip": ["geoip:private"] },
  { "ruleTag": "block-ads", "type": "field", "outboundTag": "block", "domain": ["geosite:category-ads-all"] },
  { "ruleTag": "block-antivirus", "type": "field", "outboundTag": "block", "domain": ["geosite:category-antivirus"] },
  { "ruleTag": "block-competitor", "type": "field", "outboundTag": "block", "domain": ["geosite:category-vpnservices"] },
  { "ruleTag": "block-abuse", "type": "field", "outboundTag": "block",
    "domain": ["domain:xunlei.com", "domain:sandai.net",
      "regexp:(.*\\.||)(laomoe|jiyou|ssss|lolicp|vv1234|0z|4321q|868123|ksweb|mm126)\\.(com|cloud|fun|cn|gs|xyz|cc)",
      "regexp:(flows|miaoko)\\.(pages)\\.(dev)"] }
]
JSON
)

    local ts backup
    ts=$(date +%Y%m%d%H%M%S)
    backup="${route_file}.bak.${ts}"
    cp "${route_file}" "${backup}"

    # 合并：新禁止规则置顶 + 删除旧的 outboundTag==block 规则 + 保留其它一切
    if jq --argjson nb "${new_blocks}" \
        '.rules = ($nb + ((.rules // []) | map(select(.outboundTag != "block"))))' \
        "${backup}" > "${route_file}.tmp" 2>/dev/null && jq empty "${route_file}.tmp" >/dev/null 2>&1; then
        mv "${route_file}.tmp" "${route_file}"
    else
        rm -f "${route_file}.tmp"
        echo -e "${red}生成新规则失败，已保留原文件不变${plain}"
        before_show_menu
        return
    fi

    echo -e "${green}已替换禁止规则，其它分流规则保持不变。${plain}"
    echo -e "${yellow}备份文件: ${backup}${plain}"
    echo -e "${yellow}正在重启 V2bX 使其生效...${plain}"
    restart 0
    sleep 1
    check_status
    if [[ $? != 0 ]]; then
        echo -e "${red}V2bX 未能正常启动（可能 geosite.dat 缺少某分类），正在自动回滚...${plain}"
        cp "${backup}" "${route_file}"
        restart 0
        echo -e "${yellow}已回滚到修改前的 route.json，请更新 geosite.dat 后重试${plain}"
    else
        echo -e "${green}V2bX 运行正常，新禁止规则已生效${plain}"
    fi
    before_show_menu
}

show_menu() {
    echo -e "
  ${green}V2bX 后端管理脚本，${plain}${red}不适用于docker${plain}
--- https://github.com/Shannon-x/V2bX ---
  ${green}0.${plain} 修改配置
————————————————
  ${green}1.${plain} 安装 V2bX
  ${green}2.${plain} 更新 V2bX
  ${green}3.${plain} 卸载 V2bX
————————————————
  ${green}4.${plain} 启动 V2bX
  ${green}5.${plain} 停止 V2bX
  ${green}6.${plain} 重启 V2bX
  ${green}7.${plain} 查看 V2bX 状态
  ${green}8.${plain} 查看 V2bX 日志
————————————————
  ${green}9.${plain} 设置 V2bX 开机自启
  ${green}10.${plain} 取消 V2bX 开机自启
————————————————
  ${green}11.${plain} 一键安装 bbr (最新内核)
  ${green}12.${plain} 查看 V2bX 版本
  ${green}13.${plain} 生成 X25519 密钥
  ${green}14.${plain} 升级 V2bX 维护脚本
  ${green}15.${plain} 生成 V2bX 配置文件
  ${green}16.${plain} 放行 VPS 的所有网络端口
————————————————
  ${green}17.${plain} 添加节点
  ${green}18.${plain} 删除节点
————————————————
  ${green}19.${plain} 更新路由禁止规则 (BT/PT/广告/竞品/杀软)
————————————————
  ${green}20.${plain} 退出脚本
 "
    show_status
    echo && read -rp "请输入选择 [0-20]: " num

    case "${num}" in
        0) config ;;
        1) check_uninstall && install ;;
        2) check_install && update ;;
        3) check_install && uninstall ;;
        4) check_install && start ;;
        5) check_install && stop ;;
        6) check_install && restart ;;
        7) check_install && status ;;
        8) check_install && show_log ;;
        9) check_install && enable ;;
        10) check_install && disable ;;
        11) install_bbr ;;
        12) check_install && show_V2bX_version ;;
        13) check_install && generate_x25519_key ;;
        14) update_shell ;;
        15) generate_config_file ;;
        16) open_ports ;;
        17) check_install && add_single_node ;;
        18) check_install && delete_node ;;
        19) check_install && update_route_block_rules ;;
        20) exit ;;
        *) echo -e "${red}请输入正确的数字 [0-20]${plain}" ;;
    esac
}


if [[ $# > 0 ]]; then
    case $1 in
        "start") check_install 0 && start 0 ;;
        "stop") check_install 0 && stop 0 ;;
        "restart") check_install 0 && restart 0 ;;
        "status") check_install 0 && status 0 ;;
        "enable") check_install 0 && enable 0 ;;
        "disable") check_install 0 && disable 0 ;;
        "log") check_install 0 && show_log 0 ;;
        "update") check_install 0 && update 0 $2 ;;
        "config") config $* ;;
        "generate") generate_config_file ;;
        "install") check_uninstall 0 && install 0 ;;
        "uninstall") check_install 0 && uninstall 0 ;;
        "x25519") check_install 0 && generate_x25519_key 0 ;;
        "version") check_install 0 && show_V2bX_version 0 ;;
        "update_shell") update_shell ;;
        "addnode") check_install 0 && add_single_node ;;
        "delnode") check_install 0 && delete_node ;;
        "routerule") check_install 0 && update_route_block_rules ;;
        *) show_usage
    esac
else
    show_menu
fi
