# V2bX

<p align="center">
  <img src="https://img.shields.io/github/v/release/Shannon-x/V2bX?style=flat-square" alt="Release">
  <img src="https://img.shields.io/github/actions/workflow/status/Shannon-x/V2bX/release.yml?style=flat-square&label=Build" alt="Build Status">
  <img src="https://img.shields.io/github/license/Shannon-x/V2bX?style=flat-square" alt="License">
  <img src="https://img.shields.io/github/go-mod/go-version/Shannon-x/V2bX?style=flat-square" alt="Go Version">
</p>

**V2bX** 是一个基于多内核的 V2board 节点服务端程序，支持同时对接多个节点，轻量、高效、易部署。

> 本项目为修改版，基于 [InazumaV/V2bX](https://github.com/InazumaV/V2bX) 改进。内核依赖每日自动同步上游最新版本（Xray / sing-box / Hysteria2）。

---

## 特性

- **多协议支持** — Vmess / Vless / Trojan / Shadowsocks / Hysteria / Hysteria2 / TUIC / AnyTLS
- **多内核引擎** — 同时支持 Xray、sing-box、Hysteria2 内核，按需选择
- **多节点对接** — 单实例同时对接多个面板节点，无需重复部署
- **TLS 证书管理** — 自动申请/续签 TLS 证书（HTTP / DNS / file 模式），多域名证书独立管理
- **安全防护** — 内置审计规则、IP 限制、连接数限制、跨节点 IP 限制
- **流量管控** — 用户级别限速 / 动态限速 / 端口级别限速
- **Docker 支持** — 提供优化的 Dockerfile，支持 amd64 / arm64 多架构
- **配置简洁** — JSON5 配置文件（支持注释），修改后自动热重载
- **IPv6 就绪** — 自动检测并适配 IPv6 环境
- **滚动发布** — 每次推送自动构建 `latest` release，版本始终最新
- **自动更新** — 每日自动检查上游内核更新（Xray / sing-box / Hysteria2），有更新则自动构建发布

---

## 功能矩阵

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

## 快速部署

### 一键安装（推荐）

支持 **Ubuntu / Debian / CentOS / Alpine / Arch**，支持 **amd64 / arm64 / armv7 / armv6 / armv5 / s390x / riscv64** 架构：

```bash
wget -N https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh && bash install.sh
```

安装指定版本：

```bash
wget -N https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh && bash install.sh v1.0.3
```

首次安装时脚本会询问是否自动生成配置文件，选择 `y` 即可进入交互式配置向导。

### 管理命令

安装完成后，在终端输入 `v2bx` 或 `V2bX` 即可使用管理脚本：

```bash
v2bx                # 显示交互式管理菜单
v2bx start           # 启动 V2bX
v2bx stop            # 停止 V2bX
v2bx restart         # 重启 V2bX
v2bx status          # 查看运行状态
v2bx log             # 查看实时日志
v2bx update          # 更新到最新版本
v2bx update v1.0.3   # 更新到指定版本
v2bx generate        # 交互式生成配置文件
v2bx x25519          # 生成 x25519 密钥对（Reality 节点用）
v2bx enable          # 设置开机自启
v2bx disable         # 取消开机自启
v2bx uninstall       # 卸载 V2bX
v2bx version         # 查看当前版本
```

### Docker 部署

```bash
# 创建配置目录并编辑配置文件
mkdir -p /etc/V2bX
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

```bash
docker compose up -d
```

---

## 配置说明

配置文件路径：`/etc/V2bX/config.json`（支持 JSON5 格式，可以写注释）

> **推荐**：首次安装时使用 `v2bx generate` 交互式生成配置，无需手动编写。

### 配置结构

```json
{
    "Log": { "Level": "error", "Output": "" },
    "Cores": [ /* 内核配置 */ ],
    "Nodes": [ /* 节点配置 */ ]
}
```

### 内核配置 (Cores)

一个实例可以同时运行多个内核，不同节点可以选择不同的内核。

#### Xray 内核

```json
{
    "Type": "xray",
    "Log": {
        "Level": "error",
        "ErrorPath": "/etc/V2bX/error.log"
    },
    "AssetPath": "/etc/V2bX/",
    "DnsConfigPath": "/etc/V2bX/dns.json",
    "OutboundConfigPath": "/etc/V2bX/custom_outbound.json",
    "RouteConfigPath": "/etc/V2bX/route.json",
    "XrayConnectionConfig": {
        "handshake": 4,
        "connIdle": 30,
        "uplinkOnly": 2,
        "downlinkOnly": 4,
        "bufferSize": 64
    }
}
```

| 字段 | 说明 |
|------|------|
| `AssetPath` | geoip.dat / geosite.dat 文件路径 |
| `DnsConfigPath` | Xray DNS 配置文件路径 |
| `OutboundConfigPath` | 自定义出站配置 |
| `RouteConfigPath` | 自定义路由规则 |
| `XrayConnectionConfig` | TCP 连接性能参数（握手超时、空闲超时、缓冲区等） |

#### sing-box 内核

```json
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
```

#### Hysteria2 内核

```json
{
    "Type": "hysteria2",
    "Log": {
        "Level": "error"
    }
}
```

### 节点配置 (Nodes)

每个节点代表面板上的一个节点，需要指定使用哪个内核。

#### 通用字段

| 字段 | 类型 | 必填 | 说明 |
|------|------|:----:|------|
| `Core` | string | 是 | 使用的内核：`xray` / `sing` / `hysteria2` |
| `ApiHost` | string | 是 | 面板地址，如 `https://panel.example.com` |
| `ApiKey` | string | 是 | 面板 API 对接密钥 |
| `NodeID` | int | 是 | 面板中的节点 ID |
| `NodeType` | string | 是 | 协议类型：`vmess` / `vless` / `trojan` / `shadowsocks` / `hysteria` / `hysteria2` / `tuic` / `anytls` |
| `Timeout` | int | 否 | API 请求超时（秒），默认 30 |
| `ApiVersion` | int | 否 | API 版本：`1` = V1 UniProxy（默认），`2` = V2 Flat API |
| `ListenIP` | string | 否 | 监听地址，默认 `0.0.0.0`，IPv6 用 `::` |
| `SendIP` | string | 否 | 出站源地址，默认 `0.0.0.0` |
| `DeviceOnlineMinTraffic` | int | 否 | 判定设备在线的最小流量（字节），默认 0 |
| `ReportMinTraffic` | int | 否 | 上报流量的最小阈值（KB），默认 0 |

#### Xray 节点示例

```json
{
    "Core": "xray",
    "ApiHost": "https://panel.example.com",
    "ApiKey": "your-api-key",
    "NodeID": 1,
    "NodeType": "vless",
    "Timeout": 30,
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
        "CertMode": "http",
        "CertDomain": "node1.example.com",
        "CertFile": "/etc/V2bX/node1.example.com.cert.pem",
        "KeyFile": "/etc/V2bX/node1.example.com.key.pem",
        "Email": "admin@example.com"
    }
}
```

**Xray 专属选项：**

| 字段 | 说明 |
|------|------|
| `EnableTFO` | 启用 TCP Fast Open |
| `EnableUot` | 启用 UDP over TCP |
| `EnableDNS` | 启用 Xray 内置 DNS |
| `DNSType` | DNS 策略：`AsIs` / `UseIP` / `UseIPv4` / `UseIPv6` |
| `EnableProxyProtocol` | 启用 Proxy Protocol（反代场景） |
| `DisableSniffing` | 禁用协议嗅探 |
| `EnableFallback` | 启用回落（Vless/Trojan） |

#### sing-box 节点示例

```json
{
    "Core": "sing",
    "ApiHost": "https://panel.example.com",
    "ApiKey": "your-api-key",
    "NodeID": 2,
    "NodeType": "shadowsocks",
    "Timeout": 30,
    "ListenIP": "::",
    "SendIP": "0.0.0.0",
    "DeviceOnlineMinTraffic": 200,
    "ReportMinTraffic": 0,
    "EnableTFO": true,
    "EnableSniff": true,
    "SniffOverrideDestination": true,
    "CertConfig": {
        "CertMode": "none"
    }
}
```

**sing-box 专属选项：**

| 字段 | 说明 |
|------|------|
| `EnableTFO` | 启用 TCP Fast Open |
| `EnableSniff` | 启用协议嗅探（默认 true） |
| `SniffOverrideDestination` | 嗅探结果覆盖目标地址（默认 true） |
| `EnableDNS` | 启用 DNS |
| `MultiplexConfig` | 多路复用配置（含 Brutal 支持） |

#### Hysteria2 节点示例

```json
{
    "Core": "hysteria2",
    "ApiHost": "https://panel.example.com",
    "ApiKey": "your-api-key",
    "NodeID": 3,
    "NodeType": "hysteria2",
    "Hysteria2ConfigPath": "/etc/V2bX/hy2config.yaml",
    "Timeout": 30,
    "ListenIP": "",
    "SendIP": "0.0.0.0",
    "DeviceOnlineMinTraffic": 200,
    "ReportMinTraffic": 0,
    "CertConfig": {
        "CertMode": "http",
        "CertDomain": "hy2.example.com",
        "CertFile": "/etc/V2bX/hy2.example.com.cert.pem",
        "KeyFile": "/etc/V2bX/hy2.example.com.key.pem",
        "Email": "admin@example.com"
    }
}
```

### TLS 证书配置

V2bX 支持多种证书模式，**不同域名的证书独立命名存储**，互不冲突：

| 模式 | `CertMode` | 说明 |
|------|:----------:|------|
| 无 TLS | `none` | 不使用 TLS（默认） |
| HTTP 自动申请 | `http` | 域名需已解析到本机，自动申请 Let's Encrypt 证书 |
| DNS 自动申请 | `dns` | 通过 DNS API 验证，需配置 `Provider` 和 `DNSEnv` |
| 手动指定 | `file` | 使用已有证书文件，需指定 `CertFile` 和 `KeyFile` 路径 |

```json
"CertConfig": {
    "CertMode": "dns",
    "CertDomain": "node1.example.com",
    "CertFile": "/etc/V2bX/node1.example.com.cert.pem",
    "KeyFile": "/etc/V2bX/node1.example.com.key.pem",
    "Email": "admin@example.com",
    "Provider": "cloudflare",
    "DNSEnv": {
        "CF_DNS_API_TOKEN": "your-cloudflare-api-token"
    }
}
```

支持的 DNS Provider：`alidns` / `cloudflare` / `gandi` / `godaddy` 等。

### Hysteria2 性能配置

Hysteria2 节点需要独立的 YAML 配置文件（默认 `/etc/V2bX/hy2config.yaml`），脚本会自动生成高性能优化配置：

```yaml
quic:
  initStreamReceiveWindow: 16777216    # 16MB - 流级别初始接收窗口
  maxStreamReceiveWindow: 16777216     # 16MB - 流级别最大接收窗口
  initConnReceiveWindow: 33554432      # 32MB - 连接级别初始接收窗口
  maxConnReceiveWindow: 33554432       # 32MB - 连接级别最大接收窗口
  maxIdleTimeout: 90s                  # 空闲超时
  maxIncomingStreams: 4096             # 最大并发流数
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
```

### 限速配置

在节点配置中添加 `LimitConfig`：

```json
{
    "LimitConfig": {
        "EnableRealtime": false,
        "SpeedLimit": 0,
        "DeviceLimit": 0,
        "ConnLimit": 0,
        "EnableDynamicSpeedLimit": false,
        "DynamicSpeedLimitConfig": {
            "Periodic": 60,
            "Traffic": 1073741824,
            "SpeedLimit": 10,
            "ExpireTime": 600
        }
    }
}
```

### 多节点多内核配置示例

同时运行 Xray + sing-box + Hysteria2，对接不同协议节点：

```json
{
    "Log": { "Level": "error", "Output": "" },
    "Cores": [
        {
            "Type": "xray",
            "Log": { "Level": "error", "ErrorPath": "/etc/V2bX/error.log" },
            "AssetPath": "/etc/V2bX/",
            "DnsConfigPath": "/etc/V2bX/dns.json",
            "OutboundConfigPath": "/etc/V2bX/custom_outbound.json",
            "RouteConfigPath": "/etc/V2bX/route.json",
            "XrayConnectionConfig": {
                "handshake": 4, "connIdle": 30,
                "uplinkOnly": 2, "downlinkOnly": 4, "bufferSize": 64
            }
        },
        {
            "Type": "sing",
            "Log": { "Level": "error", "Timestamp": true },
            "NTP": { "Enable": false, "Server": "time.apple.com", "ServerPort": 0 },
            "OriginalPath": "/etc/V2bX/sing_origin.json"
        },
        {
            "Type": "hysteria2",
            "Log": { "Level": "error" }
        }
    ],
    "Nodes": [
        {
            "Core": "xray",
            "ApiHost": "https://panel.example.com",
            "ApiKey": "your-key",
            "NodeID": 1,
            "NodeType": "vless",
            "EnableTFO": true,
            "EnableUot": true,
            "DNSType": "UseIPv4",
            "CertConfig": {
                "CertMode": "http",
                "CertDomain": "vless.example.com",
                "CertFile": "/etc/V2bX/vless.example.com.cert.pem",
                "KeyFile": "/etc/V2bX/vless.example.com.key.pem"
            }
        },
        {
            "Core": "sing",
            "ApiHost": "https://panel.example.com",
            "ApiKey": "your-key",
            "NodeID": 2,
            "NodeType": "trojan",
            "EnableTFO": true,
            "EnableSniff": true,
            "SniffOverrideDestination": true,
            "CertConfig": {
                "CertMode": "http",
                "CertDomain": "trojan.example.com",
                "CertFile": "/etc/V2bX/trojan.example.com.cert.pem",
                "KeyFile": "/etc/V2bX/trojan.example.com.key.pem"
            }
        },
        {
            "Core": "hysteria2",
            "ApiHost": "https://panel.example.com",
            "ApiKey": "your-key",
            "NodeID": 3,
            "NodeType": "hysteria2",
            "Hysteria2ConfigPath": "/etc/V2bX/hy2config.yaml",
            "CertConfig": {
                "CertMode": "http",
                "CertDomain": "hy2.example.com",
                "CertFile": "/etc/V2bX/hy2.example.com.cert.pem",
                "KeyFile": "/etc/V2bX/hy2.example.com.key.pem"
            }
        }
    ]
}
```

---

## 配置文件热重载

V2bX 默认启用配置文件监控（`-w` 参数），修改 `/etc/V2bX/config.json` 后会自动重载，无需手动重启。也支持通过环境变量监控 DNS 配置文件：

```bash
export XRAY_DNS_PATH=/etc/V2bX/dns.json
export SING_DNS_PATH=/etc/V2bX/sing_dns.json
```

---

## 手动编译

```bash
git clone https://github.com/Shannon-x/V2bX.git
cd V2bX && git checkout dev_new

GOEXPERIMENT=jsonv2 go build -v -o V2bX \
  -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" \
  -trimpath -ldflags "-s -w"
```

---

## 项目结构

```
V2bX/
├── api/                    # 面板 API 对接（支持 V1/V2 协议）
├── cmd/                    # CLI 命令（server/version/x25519/synctime 等）
├── common/                 # 通用工具函数
├── conf/                   # 配置解析与验证
├── core/                   # 多内核实现
│   ├── xray/               #   Xray 内核
│   ├── sing/               #   sing-box 内核
│   └── hy2/                #   Hysteria2 内核
├── limiter/                # 限速与限流
├── node/                   # 节点生命周期管理
├── V2bX-script-master/     # 安装脚本与管理脚本
│   ├── install.sh          #   一键安装脚本
│   ├── V2bX.sh             #   管理脚本（v2bx 命令）
│   └── initconfig.sh       #   配置生成脚本
├── example/                # 配置文件示例
├── Dockerfile              # Docker 构建文件
└── .github/workflows/      # CI/CD 工作流
    ├── release.yml          #   构建与发布
    ├── auto-update-core.yml #   内核自动更新
    └── Publish Docker image.yml  # Docker 镜像发布
```

---

## 许可证

本项目基于 [MPL-2.0](LICENSE) 许可证开源。

---

## 致谢

- [XTLS/Xray-core](https://github.com/XTLS/)
- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [InazumaV/V2bX](https://github.com/InazumaV/V2bX)
- [apernet/hysteria](https://github.com/apernet/hysteria)
- [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)
