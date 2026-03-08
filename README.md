# V2bX

<p align="center">
  <img src="https://img.shields.io/github/v/release/Shannon-x/V2bX?style=flat-square" alt="Release">
  <img src="https://img.shields.io/github/actions/workflow/status/Shannon-x/V2bX/release.yml?style=flat-square&label=Build" alt="Build Status">
  <img src="https://img.shields.io/github/license/Shannon-x/V2bX?style=flat-square" alt="License">
  <img src="https://img.shields.io/github/go-mod/go-version/Shannon-x/V2bX?style=flat-square" alt="Go Version">
</p>

**V2bX** 是一个基于多内核的 V2board 节点服务端程序，支持同时对接多个节点，轻量、高效、易部署。

> 本项目为修改版，基于 [InazumaV/V2bX](https://github.com/InazumaV/V2bX) 改进。

---

## ✨ 特性

- 🚀 **多协议支持** — Vmess / Vless / Trojan / Shadowsocks / Hysteria / Hysteria2 / TUIC / AnyTLS
- 🔧 **多内核引擎** — 同时支持 Xray、sing-box、Hysteria2 内核，按需选择
- 📡 **多节点对接** — 单实例同时对接多个面板节点，无需重复部署
- 🔒 **TLS 证书管理** — 自动申请 / 续签 TLS 证书（HTTP / DNS / 自签模式）
- 🛡️ **安全防护** — 内置审计规则、IP 限制、连接数限制、跨节点 IP 限制
- ⚡ **流量管控** — 用户级别限速 / 动态限速 / 端口级别限速
- 🐳 **Docker 支持** — 提供优化的 Dockerfile，支持 amd64 / arm64 多架构
- 📝 **配置简洁** — JSON 配置文件，支持修改后自动重启
- 🌐 **IPv6 就绪** — 自动检测并适配 IPv6 环境

---

## 📋 功能矩阵

| 功能 | Vmess/Vless | Trojan | Shadowsocks | Hysteria 1/2 | TUIC | AnyTLS |
|------|:-----------:|:------:|:-----------:|:------------:|:----:|:------:|
| 自动 TLS 证书 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 在线人数统计 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 审计规则 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 自定义 DNS | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| IP 数限制 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 连接数限制 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 跨节点 IP 限制 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 用户级别限速 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

---

## 🚀 快速部署

### 一键安装（推荐）

通过脚本一键安装 V2bX，支持 **Ubuntu / Debian / CentOS / Alpine / Arch** 系统：

```bash
wget -N https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh && bash install.sh
```

安装完成后，使用以下命令管理 V2bX：

```bash
V2bX              # 显示管理菜单
V2bX start        # 启动
V2bX stop         # 停止
V2bX restart      # 重启
V2bX status       # 查看状态
V2bX log          # 查看日志
V2bX update       # 更新到最新版
V2bX update x.x.x # 更新到指定版本
V2bX generate     # 交互式生成配置文件
V2bX x25519       # 生成 x25519 密钥
V2bX enable       # 设置开机自启
V2bX disable      # 取消开机自启
V2bX uninstall    # 卸载
V2bX version      # 查看版本
```

### Docker 部署

```bash
# 拉取镜像
docker pull ghcr.io/shannon-x/v2bx:latest

# 创建配置目录
mkdir -p /etc/V2bX

# 编辑配置文件（参考下方配置说明）
vi /etc/V2bX/config.json

# 启动容器
docker run -d \
  --name v2bx \
  --restart=always \
  --network=host \
  -v /etc/V2bX:/etc/V2bX \
  ghcr.io/shannon-x/v2bx:latest
```

### Docker Compose 部署

创建 `docker-compose.yml`：

```yaml
services:
  v2bx:
    image: ghcr.io/shannon-x/v2bx:latest
    container_name: v2bx
    restart: always
    network_mode: host
    volumes:
      - /etc/V2bX:/etc/V2bX
```

启动：

```bash
docker compose up -d
```

---

## ⚙️ 配置说明

配置文件路径：`/etc/V2bX/config.json`

### 基础配置示例（Xray 内核）

```json
{
    "Log": {
        "Level": "error",
        "Output": ""
    },
    "Cores": [
        {
            "Type": "xray",
            "Log": {
                "Level": "error",
                "ErrorPath": "/etc/V2bX/error.log"
            },
            "OutboundConfigPath": "/etc/V2bX/custom_outbound.json",
            "RouteConfigPath": "/etc/V2bX/route.json"
        }
    ],
    "Nodes": [
        {
            "Core": "xray",
            "ApiHost": "https://your-panel.com",
            "ApiKey": "your-api-key",
            "NodeID": 1,
            "NodeType": "vmess",
            "Timeout": 30,
            "ListenIP": "0.0.0.0",
            "SendIP": "0.0.0.0",
            "DeviceOnlineMinTraffic": 200,
            "EnableProxyProtocol": false,
            "EnableUot": true,
            "EnableTFO": true,
            "DNSType": "UseIPv4"
        }
    ]
}
```

### 基础配置示例（sing-box 内核）

```json
{
    "Log": {
        "Level": "error",
        "Output": ""
    },
    "Cores": [
        {
            "Type": "sing",
            "Log": {
                "Level": "error",
                "Timestamp": true
            },
            "NTP": {
                "Enable": false,
                "Server": "time.apple.com",
                "ServerPort": 0
            },
            "OriginalPath": "/etc/V2bX/sing_origin.json"
        }
    ],
    "Nodes": [
        {
            "Core": "sing",
            "ApiHost": "https://your-panel.com",
            "ApiKey": "your-api-key",
            "NodeID": 1,
            "NodeType": "vmess",
            "Timeout": 30,
            "ListenIP": "::",
            "SendIP": "0.0.0.0",
            "DeviceOnlineMinTraffic": 200,
            "TCPFastOpen": true,
            "SniffEnabled": true
        }
    ]
}
```

> 💡 **提示**：首次安装时选择「生成配置文件」可以交互式创建配置，无需手动编写。

---

## 🔨 手动编译

```bash
# 克隆仓库
git clone https://github.com/Shannon-x/V2bX.git
cd V2bX

# 编译（可通过 -tags 选择编译的内核：xray, sing, hysteria2）
GOEXPERIMENT=jsonv2 go build -v -o V2bX \
  -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" \
  -trimpath -ldflags "-s -w"
```

---

## 📁 项目结构

```
V2bX/
├── api/                    # 面板 API 对接
├── cmd/                    # CLI 命令定义
├── common/                 # 通用工具函数
├── conf/                   # 配置解析
├── core/                   # 多内核实现 (xray, sing-box, hysteria2)
├── limiter/                # 限速与限流
├── node/                   # 节点管理
├── V2bX-script-master/     # 安装脚本
├── Dockerfile              # Docker 构建
├── .github/workflows/      # CI/CD 工作流
└── example/                # 配置文件示例
```

---

## 📄 许可证

本项目基于 [MPL-2.0](LICENSE) 许可证开源。

---

## 🙏 致谢

- [XTLS/Xray-core](https://github.com/XTLS/)
- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [InazumaV/V2bX](https://github.com/InazumaV/V2bX)
- [apernet/hysteria](https://github.com/apernet/hysteria)
