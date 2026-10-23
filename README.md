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
  -v /var/log/V2bX:/var/log/V2bX \
  ghcr.io/shannon-x/v2bx:latest
```

> `-v /var/log/V2bX:/var/log/V2bX` 用于持久化连接（access）日志；不挂载的话日志会留在容器可写层，
> 容器重建时丢失。如果希望连接日志继续输出到 `docker logs`，把 Xray 内核的 `Log.AccessPath` 设为 `"console"`。

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
      - /var/log/V2bX:/var/log/V2bX
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
    "Runtime": { "GCPercent": 50, "MemoryLimitPercent": 70 },  /* Go 运行时调优，可省略 */
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
        "AccessPath": "",
        "ErrorPath": "/etc/V2bX/error.log",
        "MaxSize": 100,
        "MaxBackups": 0,
        "MaxDays": 90,
        "Compress": true
    },
    "AssetPath": "/etc/V2bX/",
    "DnsConfigPath": "/etc/V2bX/dns.json",
    "OutboundConfigPath": "/etc/V2bX/custom_outbound.json",
    "RouteConfigPath": "/etc/V2bX/route.json",
    "XrayConnectionConfig": {
        "handshake": 4,
        "connIdle": 120,
        "uplinkOnly": 2,
        "downlinkOnly": 4,
        "bufferSize": 64
    }
}
```

| 字段 | 说明 |
|------|------|
| `Log.AccessPath` | 连接（access）日志路径。留空 = 默认写入 `/var/log/V2bX/access.log`（自动轮转）；`"console"` = 输出到 stdout/journald（旧行为）；`"none"` = 完全关闭 |
| `Log.ErrorPath` | 错误日志路径，写文件时同样自动轮转 |
| `Log.MaxSize` | 单个日志文件大小上限（MB），超过即轮转，默认 100 |
| `Log.MaxBackups` | 保留的轮转文件个数，0 = 不限个数（仅按天数清理），默认 0 |
| `Log.MaxDays` | 轮转日志保留天数，默认 90，0 = 永久保留 |
| `Log.Compress` | 轮转后是否 gzip 压缩旧日志，默认 true |
| `AssetPath` | geoip.dat / geosite.dat 文件路径 |
| `DnsConfigPath` | Xray DNS 配置文件路径 |
| `OutboundConfigPath` | 自定义出站配置 |
| `RouteConfigPath` | 自定义路由规则 |
| `XrayConnectionConfig` | 连接策略：握手超时、空闲超时、单向关闭宽限、每连接缓冲上限（见下文及「内存与性能调优」） |

> 连接日志默认不再刷进 systemd journal（此前空 `AccessPath` 会把每条连接写到 stdout，导致 journald 无限增长）。
> 更新后无需改配置：连接日志自动落到 `/var/log/V2bX/access.log`，每天零点切分、超过 100MB 也会切分，
> 旧文件 gzip 压缩、保留 90 天，可用 `zgrep '2026/07/06' /var/log/V2bX/access*.log*` 这类命令按日期检索。

`XrayConnectionConfig` 的每一个值都是**按连接**付费的，是节点内存占用最大的开关：

| 字段 | 默认 | 说明 |
|------|------|------|
| `handshake` | 4 | 连接建立后多少秒内必须完成代理握手，超时即断开。半开/探测连接会在这段时间里占着 TLS 状态和协程栈 |
| `connIdle` | 120 | 双向都无流量多少秒后断开。客户端会把空闲长连接一直挂着直到服务端断开，所以这个值直接决定"空闲用户群"折算成多少并发连接（每条 REALITY 连接在服务端大约 60–80 KB） |
| `uplinkOnly` / `downlinkOnly` | 2 / 4 | 一侧关闭后另一侧的宽限秒数 |
| `bufferSize` | 64 | 每条连接每个方向内部管道最多排队多少 KB。吞吐由内核 socket 缓冲决定，这个管道只需要覆盖协程调度延迟；调大只会让每个慢客户端多占这么多内存（Xray 在 arm64 上的默认值只有 4 KB）。`0` 表示最多排队一次读取，`-1` 不限 |

默认值与 v2node 相同（握手 4s / 空闲 120s），`bufferSize` 比 v2node 的 128 再减半。
旧版脚本生成的 `10/300/2/4/256` 在三万并发连接规模下会比默认值多占 1 GB 以上内存，
升级后建议按上表改回。带宽大、内存充裕且连接数不多的机器可以放宽到 `4/300/2/4/128`；
内存极小的机器可继续压到 `4/60/1/2/16`。

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
| `DisableCDNRealIP` | 关闭 CDN 回源真实 IP（见下文，默认开启） |
| `Hysteria2QuicParams` | Xray 内核 hysteria2 节点的 QUIC 参数（接收窗口 / 空闲超时 / 并发流上限），见「内存与性能调优」 |

**Cloudflare CDN 回源真实 IP（默认开启，无需配置）**

vless/vmess/trojan 走 CF CDN（ws / httpupgrade / xhttp / grpc）时，源站原本看到的连接来源是 CF 边缘
节点 IP，设备数限制和面板在线 IP 上报的都是 CF IP。**从本版本起，这类 HTTP 传输默认自动读取 CF 请求头
里的真实客户端 IP**——更新 V2bX 后无需改任何配置，面板/设备限制/连接日志就会显示用户真实 IP。

原理：正常代理客户端（v2rayN、sing-box 等）不会自己发 `X-Forwarded-For`，所以 CF 填进去的就是真实
访客 IP，直接取用即可。不影响 tcp/reality 等非 HTTP 传输。

如需关闭（例如你不经过 CDN 或另有需求），在节点配置加：

```json
"DisableCDNRealIP": true
```

如果用的是别的 CDN（回源真实 IP 头不是 `CF-Connecting-IP`），可显式指定要信任的请求头：

```json
"TrustedXForwardedFor": ["X-Real-IP"]
```

> ⚠️ **关于设备数限制的加固（可选）**：程序取 `X-Forwarded-For` 的最左项作为真实 IP。正常用户不会
> 发这个头，所以显示的就是真实 IP；但**蓄意作弊的用户**可以自己构造该头来伪造上报 IP、绕过设备数限制。
> 若你依赖设备数限制做严格管控，建议：① 防火墙只放行 [Cloudflare 官方 IP 段](https://www.cloudflare.com/ips/)
> 访问源站端口；② 在 Cloudflare 加一条 Transform Rule 把 `X-Forwarded-For` 设为 `cf.connecting_ip`
> （免费版支持，会用真实 IP 覆盖客户端自带值）。仅显示真实 IP、不做严格限制的话，默认即可，无需这两步。

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

> 跑在 **Xray 内核**上的 hysteria2 节点不读取这个 YAML，QUIC 参数通过节点的
> `Hysteria2QuicParams` 配置，见「内存与性能调优」。

独立 hysteria2 内核的节点需要独立的 YAML 配置文件（默认 `/etc/V2bX/hy2config.yaml`），脚本会自动生成高性能优化配置
（16/32 MB 的窗口是为单条大流量链路准备的，每条流最多可缓冲这么多未读数据，用户多、内存小的机器建议减半）：

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

### 面板用户设备数限制

V2bX 使用面板下发的用户 `device_limit` 作为上限，并使用 Xboard-sh
`/alivelist` 返回的 Redis 实时数量进行跨节点判定。数据库中的
`online_count` 只是后台展示快照，不参与连接拒绝。

- 同一上报周期内的新 IP 会先在本节点计数，达到上限后立即拒绝，
  不会等待下一次面板同步。
- 节点每个上报周期都会发送完整设备快照，包括空快照；设备数下降后，
  面板会返回最新计数，节点立即解除不再需要的限制。
- Xboard-sh `device_limit_mode=0` 会把“节点 × IP”分别计数；如果希望同一
  公网 IP 同时测速多个节点仍只算一台设备，请使用 `device_limit_mode=1`。

跨物理节点的数量依然是周期同步的最终一致状态；V2bX 不会在每个新连接的
热路径上向面板发起同步 HTTP 请求，以避免面板网络抖动直接影响节点
连接速度和可用性。

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
                "handshake": 4, "connIdle": 120,
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

## 内存与性能调优

一个代理节点的内存几乎全部是"按连接付费"的：每条 VLESS/REALITY 连接在服务端要维持一份
TLS 状态（约 30–50 KB）、5–6 个协程栈（各 8 KB 起）和一对内部管道。三万并发连接光这部分就
是 2 GB 量级；而 Go 的垃圾回收器默认（`GOGC=100`）还会放任堆涨到存活数据的两倍再回收。
所以降内存的思路是两条：**少留连接**、**少留垃圾**。

### Runtime：Go 运行时调优

`config.json` 顶层的 `Runtime` 段，所有字段可省略：

```json
"Runtime": {
    "GCPercent": 50,            // GOGC。默认 50：堆上限 ≈ 存活数据 × 1.5（Go 默认 100 是 × 2）。0 = 保持 Go 默认；最小 10
    "MemoryLimitPercent": 70,   // 软内存上限 = 可用内存（物理内存与 cgroup 限制取小）的 70%。逼近时 GC 加速回收，0 = 关闭
    "MemoryLimit": "",          // 绝对值上限，如 "1536MiB" / "2GiB"，优先于百分比；非 Linux 平台只能用这个
    "StatsInterval": 300,       // 每隔多少秒打印一行资源摘要并做瓶颈告警（见下文「怎么判断这台机器跑满了」），0 = 关闭
    "PprofPort": 0              // 非 0 时在 127.0.0.1:<端口> 提供 Go pprof，用于抓堆/协程/CPU 剖析
}
```

* 环境变量 `GOGC` / `GOMEMLIMIT` 一旦设置就优先于配置（方便在 systemd `Environment=` 里临时覆盖）。
* `GCPercent` 越小，RSS 越贴近真实存活数据，但 GC 更频繁（每次 GC 的开销与存活数据成正比，
  50 相比 100 大约多 3–5% CPU）。CPU 吃紧的机器可以回到 100，内存吃紧可以压到 30。
* 软上限是安全网不是硬限制，**不会拒绝连接、不会限速**：堆接近上限时 GC 会把更多 CPU 用来回收
  （最多 50%），真要超过也只是超过，不会崩溃。它把"内核 OOM 杀掉进程、三万用户瞬间掉线"换成
  "CPU 升高 + 日志告警"。不要设成接近 100%，内核为三万条 TCP 连接维护的 socket 缓冲（几百 MB）
  不在 Go 堆里；独占的机器可以放宽到 80。
* 热重载 `config.json` 时 `GCPercent` / `MemoryLimit*` 会立即重新生效；`PprofPort` 需重启。
* 启动日志会打印实际生效的值：`Go runtime tuned GOGC="50 (config)" memory_limit="2.68GiB (70% of 3.83GiB)"`。

排查内存时打开 `PprofPort`（例如 6060），然后：

```bash
go tool pprof -top http://127.0.0.1:6060/debug/pprof/heap        # 谁占着内存
curl -s http://127.0.0.1:6060/debug/pprof/goroutine?debug=1 | head # 协程数 ≈ 连接数 × 5
```

### 怎么判断这台机器跑满了、什么时候该加机器

`StatsInterval`（默认 300 秒）会在日志里打印一行：

```
Runtime stats mem_used=1.52GiB heap_live=1.01GiB mem_limit=2.68GiB goroutines=152310 gc_cpu=3.2% load1=1.98/4
```

| 字段 | 含义 | 跑满的信号 |
|------|------|-----------|
| `mem_used` / `mem_limit` | Go 运行时占用 vs 软上限 | 持续 ≥ 90% |
| `heap_live` | 连接真正需要的存活数据（其余是待回收的垃圾） | 接近 `mem_limit` 说明不是 GC 的问题，是连接太多 |
| `goroutines` | Xray 每条连接约 5 个协程，`goroutines / 5` ≈ 并发连接数 | 配合每连接 70–100 KB 估算容量 |
| `gc_cpu` | 这段时间 GC 占用的 CPU 比例 | 持续 ≥ 15%：内存被压到上限，GC 在硬扛 |
| `load1` | 1 分钟负载 / 核数 | ≥ 0.9 × 核数：CPU 到顶 |

命中阈值时会额外打 **WARN**（`内存已接近这台机器的承载上限…` / `CPU 负载已接近核数…`），
用 `journalctl -u V2bX | grep -E 'Runtime stats|承载上限|接近核数'` 就能回看趋势。
这几行不受 `Log.Level` 影响：节点按常见配置写 `"Level": "error"` 也照样会打印，不想看就把 `StatsInterval` 设为 0。
内存告警先确认 `connIdle` / `bufferSize` 已按本文收紧，仍持续出现就说明连接数到顶，该加机器了；
CPU 告警则是带宽/连接数到顶（REALITY 握手和 AEAD 加解密都吃 CPU）。

**加机器怎么分流**：在面板里按同样参数再建一个节点（REALITY 的 dest / 私钥 / shortId 可以完全一样），
把用户或订阅分到两个节点；或者两台机器用同一个节点配置、域名做多条 A 记录（DNS 轮询），客户端自然分散。
不建议前置一台 HAProxy 之类做四层转发——它自己会先成为瓶颈，而且 Proxy Protocol 还要额外配置。

### 连接策略：XrayConnectionConfig

见上文「内核配置 → Xray 内核」的字段表。要点：

* `connIdle` 决定空闲连接留多久，是并发连接数的直接来源。120 s 与 v2node 一致；
  如果面板里看到 TCP 连接数远高于实际活跃用户，优先检查这个值是否还是旧脚本的 300。
* `bufferSize` 是每条慢连接的额外内存，不是吞吐开关。64 KB 已经足够，256 KB 只会让内存翻几倍。
* 速率限制（`SpeedLimit`）会让连接退出 splice 零拷贝路径，走限速的连接在 Go 侧多一份缓冲，
  不需要限速的节点就不要配。
* `AccessPath: "none"` 可以关掉连接日志，高连接数节点每秒上千条日志的格式化开销可观。

### hysteria2（Xray 内核）QUIC 参数

Xray 内核的 hysteria2 节点默认使用 8 MiB 流窗口 / 20 MiB 连接窗口 / 每连接 1024 条流，
这是为单条大流量链路准备的。每条流最多可以缓冲窗口大小的未读上行数据，用户多、内存小的机器
可以在节点的 `XrayOptions` 里收紧（单位字节，窗口最小 16384；超时单位秒，`maxIdleTimeout`
4–120，`keepAlivePeriod` 2–60；`maxIncomingStreams` 最小 8，0 表示保持 Xray 默认）：

```json
"Hysteria2QuicParams": {
    "initStreamReceiveWindow": 2097152,
    "maxStreamReceiveWindow": 4194304,
    "initConnectionReceiveWindow": 5242880,
    "maxConnectionReceiveWindow": 10485760,
    "maxIdleTimeout": 20,
    "maxIncomingStreams": 256
}
```

面板下发的 brutal 带宽（`up_mbps` / `down_mbps`）不受影响。

### 内核侧

面板显示的内存是整机数字，包含内核为 TCP 连接维护的 socket 缓冲。三万条连接时这部分
通常有几百 MB，与 V2bX 进程无关。可以适当调小自动调优的上限：

```bash
sysctl -w net.ipv4.tcp_rmem="4096 87380 4194304"
sysctl -w net.ipv4.tcp_wmem="4096 65536 4194304"
sysctl -w net.ipv4.tcp_fin_timeout=30
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

需要 Go 1.27.1 或更新版本，构建环境与 `go.mod` 保持一致。

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
