# V2bX 安全与性能审计报告

> **本报告由 AI 多智能体审计工作流生成，所有结论均经独立对抗性验证。**

---

## 元数据

| 项目 | 值 |
|---|---|
| **审计日期** | 2026-06-03 |
| **审计基线** | [`648dcc9`](https://github.com/Shannon-x/V2bX/commit/648dcc9) — `build(deps): auto-update core dependencies (xray/sing-box/hysteria)` |
| **审计分支** | `dev_new` |
| **对照参考仓库** | [wyx2685/v2node](https://github.com/wyx2685/v2node) |
| **审计方法** | 多智能体工作流 (5 阶段 × 457 子智能体)：Map → Find → 3-lens 对抗验证 → 完备性 critic → 合成 |
| **验证方式** | 每条 finding 经 correctness / impact / repro 三视角独立审查，≥2/3 票通过才保留 |
| **修复计划** | `~/.claude/plans/cryptic-weaving-llama.md`（5 Wave PR 序列） |

### 审计覆盖范围

**深度审计的目录与文件**：
- [core/xray/](core/xray/) — dispatcher / inbound / ss / node / user
- [core/sing/](core/sing/) — hook / user / node / sing
- [core/hy2/](core/hy2/) — config / logger / hook / user / node / hy2（V2bX 独有最高风险面）
- [node/](node/) — controller / task / user / node / lego / cert
- [conf/](conf/) — node / watch / conf
- [common/](common/) — rate / task / crypt / json5
- [limiter/](limiter/) — CheckLimit / UpdateUser / OnlineDevice
- [api/panel/](api/panel/) — panel / node / user / utils
- [cmd/](cmd/) — server / x25519
- [core/xray/app/dispatcher/](core/xray/app/dispatcher/) — default / linkmanager / countreader

**仅浏览或未深入**：
- `core/tun/` 及其他非主线后端
- 单测与集成测试目录（未阅读以验证回归覆盖）
- 二进制依赖（xray-core / sing-box / hysteria2 fork）—— 仅审计 V2bX 内的桥接代码
- Docker / CI 配置
- 监控 metric 暴露（仅在 #58 涉及）

---

## 修复进度跟踪

| Wave | 范围 | 状态 | PR |
|---|---|---|---|
| **PR 0** | 写入本审计报告（baseline） | 🟢 已开 PR | [#3](https://github.com/Shannon-x/V2bX/pull/3) |
| **Wave 1** | 11 项零风险一行级修复（ACME 0600 / hy2 流控字段 / hy2 logger.Panic / SS2022 UUID / Transport Clone 等） | 🟢 已开 PR | [#4](https://github.com/Shannon-x/V2bX/pull/4) |
| **Wave 2** | 并发治理（bare map sync 化 + Counter LoadOrStore + atomic.Bool/Pointer + watcher 重构） | 🟢 已开 PR | [#5](https://github.com/Shannon-x/V2bX/pull/5) |
| **Wave 3** | 性能与上报正确性（rate 三连 / 流量回填 / ctx 化 / ManagedWriter atomic） | 🟢 已开 PR | [#6](https://github.com/Shannon-x/V2bX/pull/6) |
| **Wave 4** | 安全硬化（Include SSRF / json5 上限 / ObfsPassword Marshal / X25519 默认随机 / AES base64） | 🟢 已开 PR | [#7](https://github.com/Shannon-x/V2bX/pull/7) |
| **Wave 5** | 生命周期收尾（Start 回滚 / debounce 常量统一 / 共享缺陷标注） | 🟢 已开 PR | [#8](https://github.com/Shannon-x/V2bX/pull/8) |
| **Wave 6** | 审计残留闭环 + 性能优化（**#8 自定义出站可选硬化** / #3 hy2 cache / #31 sing per-tag 锁 / #50 json5 cap / B1 alloc / B3 dirty-set / B4 并行 close） | 🟢 已开 PR | [#9](https://github.com/Shannon-x/V2bX/pull/9) |

**修复完成度（Wave 1-6 全部开 PR 后）**：

- 审计 62 项：**全部 62 项已处理**
- 额外性能优化（B1 / B3 / B4）：3 项已实施
- **#8 自定义出站**：维护者决策保持"默认接受所有面板下发"（核心功能，不能破坏）；W6 提供 `CustomOutboundConfig` **可选**硬化选项（多租户场景使用），见下方 §3.3。
- 既往的部分项："W2.9 sing 锁粒度"、"W3.6 hy2 logger CheckLimit 缓存"、"W4.2 json5 后半" 全部在 Wave 6 闭环。

**升级兼容性**：Wave 6 **不引入任何行为变更**。面板下发的自定义出站继续按原样接受所有协议，**无需修改任何节点配置**。

> ℹ️ **2026-06-03 维护者决策更正**：早先版本的 Wave 6 曾把 #8 默认收紧为"白名单 freedom/blackhole 然后让用户 opt-in"。维护者指出这会破坏所有依赖 panel 下发 socks/http/vmess 出站的生产部署——这是 V2bX 核心功能而非例外用法。最终决定**保持默认允许全部**，把白名单做成可选加固选项。详见 §3.3。

如果你**主动想要**限制面板可下发的出站协议（典型场景：多租户节点，面板和节点不是同一方管理），可以在 `Options` 节点配置加：

```jsonc
{
  "CustomOutbound": {
    "AllowedProtocols": ["freedom","blackhole","socks"]  // 显式白名单
    // 或 "Enabled": false 完全禁用
  }
}
```

不配置 `CustomOutbound` → 接受所有面板下发出站（默认，等同 pre-W6 行为）。

---

## 1. 执行摘要

V2bX 在 `dev_new` 分支上对比 v2node 引入了多个本不存在的高危缺陷，主要集中在**新增的 Hysteria2 原生后端**、**Include URL 配置加载**、**ACME 证书写入**、**自定义出站路由热加载**和**速率限制器**等 V2bX 独有的代码路径。共确认 **62 个有效问题**（high 21 / medium 27 / low 14）。

**最高风险区域**：
- **Hysteria2 子系统**（[core/hy2/](core/hy2/)）：并发崩溃（map race / panic）、流控配置失效、限流器在数据路径上调用昂贵 CheckLimit、UserLimitInfo 字段无同步、`UpdateNodeReportMinTraffic` 为空实现。V2bX 独有代码，v2node 无可参考实现。
- **配置加载与监听**（[conf/](conf/)）：Include URL 的 SSRF 守卫存在 DNS rebinding 旁路、响应体无大小限制（OOM）、json5 prep 无上限缓存、watcher reload 在事件 goroutine 中执行重型重建并 race 共享 `vc`。
- **流量上报与并发 map**：多处 `nodeReportMinTrafficBytes` / `inboundOptions` / `Hy2nodes` 为裸 map 且写操作不加锁，典型 `fatal error: concurrent map read and map write` 触发场景。
- **速率限制**（[common/rate/conn.go](common/rate/conn.go)）：quantum=capacity 的 1 秒粗粒度 + WaitMaxDuration 静默放行超额 + I/O 后再扣减令牌——三者叠加导致按用户限速形同虚设。
- **认证/密钥**：ACME 私钥写为 0644、X25519 私钥可由 token 派生（预测性密钥）、SS 2022 UUID 切片 panic。

整体上，V2bX 的"新增功能"（Include URL / 自定义出站 / Hysteria2 原生 / 动态速率）在并发安全和输入验证两端均明显落后于 v2node 稳态代码。v2node 也存在部分共享缺陷（AesDecrypt、限速器结构），但缺乏 V2bX 独有的高危面。

---

## 2. 关键性能问题 (Critical Performance)

### 2.1 速率限制器三连缺陷 — 按用户限速完全失效

**热路径**：每个 TCP 用户连接的 Read / Write，每个 sing-box / xray MultiBuffer Write。

三个独立缺陷叠加：

| # | 文件 | 问题 |
|---|---|---|
| 19 | [common/rate/conn.go:20,31](common/rate/conn.go#L20) | `NewBucketWithQuantum(time.Second, rate, rate)` — quantum == capacity == 1 秒，1Hz 阶梯式补充 |
| 20 | [common/rate/conn.go:47-61](common/rate/conn.go#L47-L61) | `c.Conn.Read/Write` 在 `WaitMaxDuration` **之前**执行——令牌事后扣除，首次突发可全速 |
| 21 | [common/rate/conn.go:50,58](common/rate/conn.go#L50) | `WaitMaxDuration(..., 5s)` 在等待 > 5 秒时**静默放行**，bool 返回值被丢弃 |

**定量影响**：
- 10 Mbps 用户的短传输（≤ 2.5 MB）瞬时达到线速
- 多流并发用户累计欠债 > 5 秒 × rate 后，所有超额读写**免计费通过**
- 1 Mbps 用户单次 700 KB MultiBuffer（> 5 × 125 KB）一次性全速通过
- 即便长流也呈 1 Hz 抖动，TLS 握手、gRPC headers 出现 ~1 秒卡顿

**v2node 对照**：`/tmp/v2node/common/rate/conn.go:21-28` 使用 `limiter.Wait(int64(len(b)))` 在 I/O **之前**调用且无 maxWait——正确的令牌桶用法。

**修复（Wave 3）**：quantum 改 10ms 粒度（`NewBucketWithQuantum(10*time.Millisecond, rate, rate/100)`）+ `Wait` 移到 I/O 前 + 用 `Wait` 替代 `WaitMaxDuration`。

---

### 2.2 Hysteria2 InitialStreamReceiveWindow 字段写错 — 流控塌陷

**热路径**：所有 Hysteria2 入站连接的 QUIC 初始流接收窗口。

[core/hy2/config.go:84-90](core/hy2/config.go#L84-L90) 第 89 行 else 分支错误地把 `config.QUIC.InitConnectionReceiveWindow` 赋给 `quic.InitialConnectionReceiveWindow`（字段和源都错），导致 `quic.InitialStreamReceiveWindow` 永远为 0，quic-go 退化为内部默认 512 KiB（远小于配置意图的 8 MiB）。

**影响**：长 RTT 链路上单流吞吐被压到约 5 Mbps / 5ms RTT 倍数；运营商对 InitStreamReceiveWindow 的覆写完全无效。

**修复（Wave 1）**：第 89 行改为 `quic.InitialStreamReceiveWindow = config.QUIC.InitStreamReceiveWindow`。一行。

---

### 2.3 Hysteria2 logger 每条流 3× UserTag + 完整 CheckLimit

**热路径**：每条 Hysteria2 TCP/UDP 流量首包。

[core/hy2/logger.go:49-110](core/hy2/logger.go#L49-L110) `Connect/TCPRequest/UDPRequest` 每个回调里 `format.UserTag(l.Tag, uuid)` + 完整 `Limiter.CheckLimit`（含 `new(sync.Map)` 分配 + RLock）各调用 3 次，仅为翻转一个 bool `OverLimit`。

**修复（Wave 3）**：顶部计算一次 `tu := format.UserTag(...)`；CheckLimit 只在状态变化时更新；`OverLimit` 改 `atomic.Bool`。

---

### 2.4 限流器 CheckLimit 每条新连接 `new(sync.Map)` 然后丢弃

[limiter/limiter.go:211-235](limiter/limiter.go#L211-L235) `newipMap := new(sync.Map); newipMap.Store(ip, uid)` 无条件分配，然后 `LoadOrStore` 在稳态下立即丢弃。

**影响**：5k conns/s × 100k 用户 = 每秒 ~5k 个 sync.Map 头被分配后扔给 GC。

**修复（Wave 3）**：先 `Load`，miss 再分配。

---

### 2.5 ManagedWriter 数据热路径每次写都 RLock

[core/xray/app/dispatcher/linkmanager.go:25-33](core/xray/app/dispatcher/linkmanager.go#L25-L33) 10 Gbps 节点 ~830k 写/秒 × 2 atomics/RLock = 数 ms/秒 CPU 浪费。

**修复（Wave 3）**：`atomic.Pointer[buf.Writer]`，Load-only 热路径。

---

### 2.6 自定义 http.Transport 抹掉 Go 默认 — 失去 HTTP/2 与 Proxy

[api/panel/panel.go:44-47](api/panel/panel.go#L44-L47) `client.SetTransport(&http.Transport{MaxIdleConnsPerHost:10, IdleConnTimeout:90s})` **替换**（非扩展）默认 Transport，导致：
- Proxy=nil（HTTPS_PROXY 被忽略）
- ForceAttemptHTTP2=false（每次轮询新建 TLS）
- TLSHandshakeTimeout=0（慢握手挂满 30s）

**修复（Wave 1）**：`t := http.DefaultTransport.(*http.Transport).Clone(); t.MaxIdleConnsPerHost = 10; ...`

---

## 3. 安全问题 & 漏洞

### 3.1 ACME 私钥写为 0644 — 任意本地用户可读取 TLS 私钥

[node/lego.go:149-166](node/lego.go#L149-L166)（私钥行 162）`os.WriteFile(..., certificates.PrivateKey, 0644)` 写入 world-readable 私钥。对比 [node/cert.go:105](node/cert.go#L105) 自签密钥使用 0600，证实是疏忽。

**影响**：在多租户主机或同主机有任何非特权服务时私钥完全泄露；ACME 账户跨续期持续——直到手动轮换永久泄露。

**修复（Wave 1）**：0600，并 chmod 兜底 umask。

---

### 3.2 Include URL SSRF + DNS rebinding + 无大小限制

[conf/node.go:18-31, 61-89](conf/node.go#L18-L89) `safeIncludeTransport.DialContext` 中 `net.ParseIP(host)` 仅在 host 是字面 IP 时启用私网阻断；DNS 名称（如 `internal.attacker.example.com → 169.254.169.254`）直接绕过。同时：
- `io.ReadAll` 无 `MaxBytesReader`、`CheckRedirect` 未限制跨域跳转
- [common/json5/json5.go:49-55](common/json5/json5.go#L49-L55) `prep()` 用 `bytes.Buffer` + `io.Copy` 无上限缓存（峰值内存 ~2× 输入）
- Transport 缺 `ResponseHeaderTimeout`、`TLSHandshakeTimeout`，可被 slow-loris 拖满 30s

**影响**：AWS IMDS 169.254.169.254 IAM 凭证窃取、本地服务访问、单次 Include 即可 OOM 节点。

**修复（Wave 4）**：自行 `LookupIPAddr` 检查所有解析地址，pin 已校验的 IP；禁止跨主机重定向；`http.MaxBytesReader(nil, rsp.Body, 8<<20)`。

---

### 3.3 自定义出站 JSON 热加载到 xray — 完全 MITM 能力（**信任边界声明 + 可选硬化**）

[core/xray/node.go:94-141](core/xray/node.go#L94-L141) `AddNodeCustomOutbounds` 把面板返回的 `RawOutbound`/`RawDefaultOut` 直接 `json.Unmarshal` 到 `coreConf.OutboundDetourConfig` 然后 `ohm.AddHandler`，**无协议白名单、无 sendThrough 校验、无操作员审批**。

**潜在影响**：面板沦陷立即升级为**所有代理流量的运行时 MITM**——可路由任意域名到攻击者出站、嗅探非端到端 TLS 流量、跨内网横移。

> ⚠️ **维护者决策（2026-06-03 最终版）**：**保持默认允许全部**——面板下发自定义出站是 V2bX 核心功能而非例外，绝大多数生产部署依赖此功能进行路由配置。强制收紧会静默破坏所有这些部署。
>
> **本节作为信任边界声明**：
> **使用 V2bX 并允许面板下发自定义出站时，部署方必须将面板访问凭证（admin token / DB 凭证）视同节点 root 凭证保护。**面板侧任何沦陷都意味着所有出站流量可被运行时重路由。
>
> ✅ **Wave 6 提供了可选硬化机制（PR [#9](https://github.com/Shannon-x/V2bX/pull/9)）**：
> 新增 [conf/custom_outbound.go](conf/custom_outbound.go) 的 `CustomOutboundConfig`，**默认不启用**。需要更严格隔离的部署方可显式 opt-in：
>
> | 配置 | 效果 |
> |---|---|
> | （省略 / nil） | **接受所有**（默认，等同 pre-W6） |
> | `Enabled=false` | 拒绝所有面板下发出站 |
> | `AllowedProtocols=["freedom","socks"]` | 仅接受白名单协议 |
> | `AllowedProtocols=["*"]` | 显式表示接受所有 |
>
> 典型使用场景：多租户节点（面板和节点运维方不是同一人），或对接陌生面板时希望先收紧再观察。普通"自管面板 + 自管节点"部署**无需任何额外配置**。

---

### 3.4 Hysteria2 ObfsPassword 通过 fmt.Sprintf 拼入 JSON — 注入

[core/xray/inbound.go:464-476](core/xray/inbound.go#L464-L476) `rawobfsJSON := json.RawMessage(fmt.Sprintf(`{"password":"%s"}`, s.ObfsPassword))` — 面板返回的 ObfsPassword 含 `"` / `\` / `"}{"foo":...` 即可注入任意 JSON 字段或致 xray 解析失败 DoS。

**修复（Wave 4）**：`payload, _ := json.Marshal(map[string]string{"password": s.ObfsPassword})`。

---

### 3.5 X25519 私钥可由 (nodeID || nodeType || token) SHA256 派生

[cmd/x25519.go:27-66](cmd/x25519.go#L27-L66) 交互式提示默认 `Y` 为基于节点信息派生密钥。两节点 (node_id, type, token) 相同即共享私钥；token 一旦泄露，私钥 O(1) 可重建——直接破坏 Reality TLS 握手保密性。

**修复（Wave 4）**：交互式默认改为"随机"，派生模式保留但加大警告。

---

### 3.6 Hysteria2 logger 用 zap.Panic — 临时缺 limiter 即崩溃

[core/hy2/logger.go:49-118](core/hy2/logger.go#L49-L118) `Connect/TCPRequest/UDPRequest` 在 `limiter.GetLimiter(l.Tag)` 返回错误时调用 `l.logger.Panic(...)` — zap 引发 Go panic，贯穿 hysteria 流处理 goroutine（无 recover），**杀整个 V2bX 进程**。

**触发**：面板触发的 DeleteLimiter→AddLimiter 间隙内任意一条新连接。

**修复（Wave 1）**：`Warn` + 提前 return false。

---

### 3.7 Shadowsocks 2022 UUID 切片越界 panic

[core/xray/ss.go:35-46](core/xray/ss.go#L35-L46) 与 [core/sing/user.go:54-57](core/sing/user.go#L54-L57) `userInfo.Uuid[:keyLength]` 在 UUID 短于 16/32 字节时 panic，杀掉整批 AddUsers，inbound 半初始化。

**修复（Wave 1）**：`if len(userInfo.Uuid) < keyLength { log and skip }`。

---

### 3.8 其它

- **#46** [api/panel/node.go:505-543](api/panel/node.go#L505-L543) push_interval 无下限/上限 — 面板返回 `push_interval=0` 触发 busy-loop 自 DoS（Wave 1）
- **#58** [core/sing/hook.go:46](core/sing/hook.go#L46) UUID 含换行/ANSI 可伪造审计日志行（Wave 4）
- **#59** [api/panel/utils.go:14-16](api/panel/utils.go#L14-L16) `path.Join(c.APIHost + path)` 折叠 `https://` 为 `https:/`（Wave 1）

---

## 4. 正确性 Bug

### 4.1 进程崩溃级 — 并发 map 读写（典型 `fatal error: concurrent map read and map write`）

| # | 文件 | map |
|---|---|---|
| 5 | [core/hy2/hy2.go:13](core/hy2/hy2.go#L13), [core/hy2/node.go:64,87](core/hy2/node.go#L64), [core/hy2/user.go:39,61,64](core/hy2/user.go#L39) | `Hy2nodes` |
| 12/14/34 | [core/xray/xray.go:39](core/xray/xray.go#L39), [core/xray/node.go:31,91,182](core/xray/node.go#L31), [core/xray/user.go:85](core/xray/user.go#L85) | `nodeReportMinTrafficBytes` (xray) |
| 33 | [core/sing/sing.go:36](core/sing/sing.go#L36), [core/sing/node.go:401,429,437](core/sing/node.go#L401) | `nodeReportMinTrafficBytes` (sing) |
| 32 | [core/sing/sing.go:37](core/sing/sing.go#L37), [core/sing/node.go:407,440](core/sing/node.go#L407) | `inboundOptions` |
| 16 | [node/controller.go:21,24](node/controller.go#L16-L33), [node/task.go:95,182-208](node/task.go#L95) | `c.traffic`, `c.info` |

**修复（Wave 2）**：`sync.Map` 或 `sync.RWMutex`。

### 4.2 数据竞争 / sync.Map Load+Store 反模式

| # | 文件 | 表现 |
|---|---|---|
| 1/29/39 | [core/xray/app/dispatcher/default.go:195-218,379-401](core/xray/app/dispatcher/default.go#L195-L218) | LinkManagers/Counter:loser 注册到孤儿 LM，DelUsers 不可达；FD + pipe goroutine 永久泄漏 |
| 23/28/57 | [core/sing/hook.go:94-101,169-175](core/sing/hook.go#L94-L101) | sing HookServer.counter 同问题 |
| 22/42/48/56 | [core/hy2/hook.go:28-59](core/hy2/hook.go#L28-L59) | hy2 Counter Load+Store + UserLimitInfo.OverLimit 无原子读写 |
| 26 | [limiter/limiter.go:191-208](limiter/limiter.go#L191-L208) | UserLimitInfo 字段（DynamicSpeedLimit/ExpireTime）无锁写 |
| 30 | [conf/watch.go:41-66](conf/watch.go#L41-L66) | 重载 goroutine race 共享 `vc` 指针 |
| 35 | [core/xray/node.go:187-194](core/xray/node.go#L187-L194) | DelNode 与新连接 race 漏 CloseAll |
| 31 | [core/sing/user.go:113,135-150](core/sing/user.go#L113) | 全局 mapLock 下重建 inbound 阻塞 |

**统一修复（Wave 2）**：`LoadOrStore` 替代 `Load → Store`，关键字段 `atomic.Bool` / `atomic.Pointer`。

### 4.3 流量统计与上报

| # | 文件 | 问题 |
|---|---|---|
| 13 | [node/user.go:8-21](node/user.go#L8-L21) | `GetUserTrafficSlice(..., true)` **先**清零计数，POST 失败仅 log 不回填，每次失败 = 整周期流量永久丢失 |
| 41 | [core/hy2/hy2.go:64-66](core/hy2/hy2.go#L64-L66) | `Hysteria2.UpdateNodeReportMinTraffic` **空实现** — 面板阈值变更对 hy2 节点完全无效直到重启 |
| 43 | [api/panel/user.go:102-127](api/panel/user.go#L102-L127) | `GetUserAlive` 所有失败路径返回 nil error，调用方误清空 AliveList，设备限静默失效 |

### 4.4 其它单点正确性

| # | 文件 | 问题 |
|---|---|---|
| 17 | [common/crypt/aes.go:18-30](common/crypt/aes.go#L18-L30) | `de := make([]byte, len(data))` 用 base64 编码长度而非解码长度，返回值含 NUL 填充 |
| 38 | [core/xray/app/dispatcher/countreader.go:19-28](core/xray/app/dispatcher/countreader.go#L19-L28) | `ReadMultiBufferTimeout(time.Duration)` 参数未命名，固定传 `time.Second`，忽略调用者超时 |
| 40 | [core/xray/app/dispatcher/default.go:507-512](core/xray/app/dispatcher/default.go#L507-L512) | `routedDispatch` `sessionInbound.User != nil` 未先判空，nil 指针 panic 在无 recover goroutine |
| 60 | [conf/watch.go:26-73](conf/watch.go#L26-L73) | 10s 防抖 + 5s sleep 错配，6-10s 内连续编辑被丢弃 |
| 61 | [node/node.go:20-39](node/node.go#L20-L39) | Start 部分失败留下半初始化 controller，后续 Close 报 "del node error" |
| 25/44 | [common/task/task.go:80-103](common/task/task.go#L80-L103) | `Execute()` 不接 ctx，看门狗超时后内层 goroutine 仍持有 HTTP 响应体，慢面板下累积 OOM |
| 47/55 | [node/controller.go:111-133](node/controller.go#L111-L133) | `Controller.Close()` 未调用 `c.apiClient.Close()`，每次 reload 漏 10 个 idle TLS 连接 × 90s |

---

## 5. 完整问题清单（62 项，按严重度排序）

| # | severity | category | file:lines | title | 修复 Wave |
|---|---|---|---|---|---|
| 1 | high | concurrency | [core/xray/app/dispatcher/default.go:195-218](core/xray/app/dispatcher/default.go#L195-L218) | dispatcher.getLink Load+Store race — LinkManager/Counter 孤儿 | W2.5 |
| 2 | high | performance | [core/hy2/config.go:82-97](core/hy2/config.go#L82-L97) | InitialStreamReceiveWindow 字段写错,流控塌陷 | W1.2 |
| 3 | high | performance | [core/hy2/logger.go:49-110](core/hy2/logger.go#L49-L110) | Connect/TCPRequest/UDPRequest 每次 3× UserTag + 完整 CheckLimit | W2 (dedup) + W6 (cache, [#9](https://github.com/Shannon-x/V2bX/pull/9)) |
| 4 | high | concurrency | [node/controller.go:24,105](node/controller.go#L24) | `c.info` 指针无锁替换 | W2.4 |
| 5 | high | concurrency | [core/hy2/hy2.go:13](core/hy2/hy2.go#L13) | `Hy2nodes` map 无锁 → 并发读写 fatal | W2.1 |
| 6 | high | security | [node/lego.go:149-166](node/lego.go#L149-L166) | ACME 私钥写为 0644 | W1.1 |
| 7 | high | vulnerability | [core/xray/inbound.go:464-476](core/xray/inbound.go#L464-L476) | ObfsPassword JSON 注入 | W4.4 |
| 8 | high | vulnerability | [core/xray/node.go:94-141](core/xray/node.go#L94-L141) | 面板可控自定义出站热加载 | 信任边界声明 + W6 可选硬化（默认不启用，[#9](https://github.com/Shannon-x/V2bX/pull/9)）— 见 §3.3 |
| 9 | high | vulnerability | [conf/node.go:18-31,61-89](conf/node.go#L18-L89) | Include URL DNS rebinding SSRF + 无大小限制 | W4.1 |
| 10 | high | bug | [core/hy2/logger.go:49-118](core/hy2/logger.go#L49-L118) | zap.Panic 在请求路径致整进程崩溃 | W1.3 |
| 11 | high | bug | [core/hy2/config.go:84-90](core/hy2/config.go#L84-L90) | InitStreamReceiveWindow 永不生效（与 #2 同根） | W1.2 |
| 12 | high | concurrency | [core/xray/xray.go:39](core/xray/xray.go#L39) | xray nodeReportMinTrafficBytes 裸 map | W2.2 |
| 13 | high | bug | [node/user.go:8-21](node/user.go#L8-L21) | 流量上报失败不回填,周期内永久丢失 | W3.1 |
| 14 | high | concurrency | [core/xray/user.go:85](core/xray/user.go#L85) | GetUserTrafficSlice 读 map 时被并发写 | W2.2 |
| 15 | high | concurrency | [conf/watch.go:26-63](conf/watch.go#L26-L63) | watcher reload 在事件 goroutine 内重型重建 | W2 模式 B |
| 16 | high | concurrency | [node/controller.go:16-33](node/controller.go#L16-L33) | Controller.info / Controller.traffic 跨 goroutine 无同步 | W2.4 |
| 17 | high | bug | [common/crypt/aes.go:18-30](common/crypt/aes.go#L18-L30) | AesDecrypt 用 base64 长度分配输出缓冲 | W4.7 |
| 18 | high | vulnerability | [conf/node.go:61-73](conf/node.go#L61-L73) | Include URL 响应体无 MaxBytesReader | W4.1 |
| 19 | high | performance | [common/rate/conn.go:20,31](common/rate/conn.go#L20) | quantum=capacity 产生 1Hz 阶梯式整形 | W3.5 |
| 20 | high | performance | [common/rate/conn.go:47-61](common/rate/conn.go#L47-L61) | 限速器在 I/O 后才扣令牌 | W3.5 |
| 21 | high | bug | [common/rate/conn.go:50,58](common/rate/conn.go#L50) | WaitMaxDuration 静默放行超额 | W3.5 |
| 22 | medium | concurrency | [core/hy2/hook.go:28-59](core/hy2/hook.go#L28-L59) | hy2 LogTraffic OverLimit/Counter race | W2.5/W2.7 |
| 23 | medium | concurrency | [core/sing/hook.go:94-101](core/sing/hook.go#L94-L101) | sing HookServer Load+Store race | W2.5 |
| 24 | medium | performance | [limiter/limiter.go:211-235](limiter/limiter.go#L211-L235) | CheckLimit 每次 new(sync.Map) 丢弃 | W3.7 |
| 25 | medium | concurrency | [common/task/task.go:80-103](common/task/task.go#L80-L103) | Execute 不接 ctx,慢面板下 goroutine 堆积 | W3.2 |
| 26 | medium | concurrency | [limiter/limiter.go:191-208](limiter/limiter.go#L191-L208) | UserLimitInfo 字段无锁写 | W2.6 |
| 27 | medium | concurrency | [core/hy2/hook.go:38-44](core/hy2/hook.go#L38-L44) | OverLimit 无原子读写 | W2.7 |
| 28 | medium | concurrency | [core/xray/app/dispatcher/default.go:212-218](core/xray/app/dispatcher/default.go#L212-L218) | xray Counter Load+Store race | W2.5 |
| 29 | medium | concurrency | [core/xray/app/dispatcher/default.go:195-203](core/xray/app/dispatcher/default.go#L195-L203) | LinkManagers 同上（CloseAll 丢失） | W2.5 |
| 30 | medium | concurrency | [conf/watch.go:41-66](conf/watch.go#L41-L66) | watcher race 共享 `vc` | W2 模式 B |
| 31 | medium | concurrency | [core/sing/user.go:113](core/sing/user.go#L113) | sing rebuildInbound 在全局锁下做长 I/O | W6 per-tag 锁 ([#9](https://github.com/Shannon-x/V2bX/pull/9)) |
| 32 | medium | concurrency | [core/sing/sing.go:37,99](core/sing/sing.go#L37) | sing inboundOptions 无锁 | W2.3 |
| 33 | medium | concurrency | [core/sing/node.go:401,429,437](core/sing/node.go#L401) | sing nodeReportMinTrafficBytes 无锁 | W2.3 |
| 34 | medium | concurrency | [core/xray/xray.go:39,53](core/xray/xray.go#L39) | xray nodeReportMinTrafficBytes 无锁 | W2.2 |
| 35 | medium | concurrency | [core/xray/node.go:187-194](core/xray/node.go#L187-L194) | DelNode Range+Delete 与新连接 race | W2.8 |
| 36 | medium | bug | [core/xray/ss.go:35-46](core/xray/ss.go#L35-L46) | SS 2022 UUID 切片 panic | W1.4 |
| 37 | medium | vulnerability | [cmd/x25519.go:27-66](cmd/x25519.go#L27-L66) | X25519 私钥确定性派生 | W4.5 |
| 38 | medium | bug | [core/xray/app/dispatcher/countreader.go:19-28](core/xray/app/dispatcher/countreader.go#L19-L28) | ReadMultiBufferTimeout 忽略参数 | W1.9 |
| 39 | medium | concurrency | [core/xray/app/dispatcher/default.go:196-203,380-386](core/xray/app/dispatcher/default.go#L196-L203) | LinkManager 注册 race 致 writer 泄漏 | W2.5 |
| 40 | medium | bug | [core/xray/app/dispatcher/default.go:507-512](core/xray/app/dispatcher/default.go#L507-L512) | routedDispatch nil InboundFromContext panic | W1.10 |
| 41 | medium | bug | [core/hy2/hy2.go:64-66](core/hy2/hy2.go#L64-L66) | Hysteria2.UpdateNodeReportMinTraffic 空实现 | W1.7 |
| 42 | medium | concurrency | [core/hy2/hook.go:28-59](core/hy2/hook.go#L28-L59) | LogTraffic OverLimit + Counter 双 race | W2.5/W2.7 |
| 43 | medium | bug | [api/panel/user.go:102-127](api/panel/user.go#L102-L127) | GetUserAlive 所有失败返回 nil error | W1.8 |
| 44 | medium | concurrency | [node/task.go:58-88](node/task.go#L58-L88) | nodeInfoMonitor 三次串行 HTTP 无 ctx | W3.4 |
| 45 | medium | performance | [api/panel/panel.go:44-47](api/panel/panel.go#L44-L47) | 自定义 Transport 抹掉 HTTP/2/Proxy | W1.6 |
| 46 | medium | vulnerability | [api/panel/node.go:505-543](api/panel/node.go#L505-L543) | push_interval 无上下限,可自 DoS | W1.11 |
| 47 | medium | performance | [node/controller.go:111-133](node/controller.go#L111-L133) | Controller.Close 未关 apiClient | W3.3 |
| 48 | medium | concurrency | [core/hy2/hook.go:28-59](core/hy2/hook.go#L28-L59) | hy2 OverLimit 无同步 | W2.7 |
| 49 | medium | performance | [api/panel/panel.go:35-101](api/panel/panel.go#L35-L101) | Transport 缺 HTTP/2/Proxy/超时 | W1.6 |
| 50 | medium | vulnerability | [common/json5/json5.go:49-55](common/json5/json5.go#L49-L55) | prep() 无大小上限缓存 | W4 (caller-side) + W6 (prep-side, [#9](https://github.com/Shannon-x/V2bX/pull/9)) |
| 51 | medium | vulnerability | [conf/node.go:18-31,64-73](conf/node.go#L18-L73) | Include 缺 ResponseHeaderTimeout/Content-Length 检查 | W4.1 |
| 52 | medium | performance | [common/rate/conn.go:20,47-61](common/rate/conn.go#L20-L61) | 配置 Mbps 与实际吞吐定量偏差 | W3.5 |
| 53 | low | performance | [core/xray/app/dispatcher/linkmanager.go:25-33](core/xray/app/dispatcher/linkmanager.go#L25-L33) | ManagedWriter 每次写 RLock | W3.8 |
| 54 | low | concurrency | [core/sing/hook.go:94-100](core/sing/hook.go#L94-L100) | sing Load+Store race(低重) | W2.5 |
| 55 | low | concurrency | [node/controller.go:111-133](node/controller.go#L111-L133) | apiClient 在 Close 时泄漏 | W3.3 |
| 56 | low | concurrency | [core/hy2/hook.go:47-50](core/hy2/hook.go#L47-L50) | hy2 Counter Load+Store(低重) | W2.5 |
| 57 | low | concurrency | [core/sing/hook.go:94-100,169-175](core/sing/hook.go#L94-L100) | sing 首次流量丢失(低重) | W2.5 |
| 58 | low | vulnerability | [core/sing/hook.go:46](core/sing/hook.go#L46) | UUID 日志注入 | W4.6 |
| 59 | low | bug | [api/panel/utils.go:14-16](api/panel/utils.go#L14-L16) | assembleURL path.Join 折叠 scheme 双斜线 | W1.5 |
| 60 | low | bug | [conf/watch.go:26-73](conf/watch.go#L26-L73) | debounce 10s vs apply 5s 错配丢事件 | W5.2 |
| 61 | low | bug | [node/node.go:20-39](node/node.go#L20-L39) | Start 失败留半初始化 controller | W5.1 |
| 62 | low | bug | [core/hy2/hook.go:47-50](core/hy2/hook.go#L47-L50) | hy2 LoadOrStore 与 v2node 对照 | W2.5 |

---

## 6. 修复优先级建议（影响 × 难度加权）

按修复计划落地优先级排序：

### #1 ACME 私钥写为 0600（#6） · [node/lego.go:162](node/lego.go#L162) · **Wave 1**
即时可被本地用户盗取所有 TLS 私钥。**一个常量改动**。立刻补丁 + chmod 兜底 umask。

### #2 Hysteria2 InitialStreamReceiveWindow 字段修正（#2/#11） · [core/hy2/config.go:89](core/hy2/config.go#L89) · **Wave 1**
所有 hy2 节点运营商调优静默失效。**一行修改**。

### #3 Hy2 logger.Panic → Warn（#10） · [core/hy2/logger.go:52,73,98](core/hy2/logger.go#L52) · **Wave 1**
防止单条恶意/巧合连接杀进程。**3 处字面替换 + 提前 return**。

### #4 所有裸 map 改为 sync.Map / RWMutex（#5/#12/#14/#32/#33/#34） · **Wave 2**
消除随时可能的 fatal "concurrent map read and map write"。模板化重构，每处约 10 行。优先 `nodeReportMinTrafficBytes` × 3 处与 `Hy2nodes`，这两个最易触发。
- [core/xray/xray.go:39](core/xray/xray.go#L39), [core/sing/sing.go:36-37](core/sing/sing.go#L36-L37), [core/hy2/hy2.go:13](core/hy2/hy2.go#L13)

### #5 速率限制器三连修复（#19/#20/#21） · [common/rate/conn.go:20-61](common/rate/conn.go#L20-L61) · **Wave 3**
计费/QoS 公平性彻底失效，直接影响业务收入。约 20 行：quantum 调小 + Wait 前置 + 检查 bool。

**次优先**：
- **#13 流量上报失败回填**（Wave 3） · [node/user.go:8-21](node/user.go#L8-L21)，仿照 [core/xray/user.go:95-103](core/xray/user.go#L95-L103) 的 below-threshold 回写模式
- **#9 Include URL SSRF + #18 MaxBytesReader**（Wave 4） · [conf/node.go](conf/node.go)，配置加载攻击面
- **#8 自定义出站白名单**：**本期不修复**，见 §3.3 信任边界声明

---

## 7. v2node 借鉴清单

| 主题 | v2node | V2bX 现状 | 建议 Wave |
|---|---|---|---|
| Watcher 重载模式 | `conf/watch.go` 只 poke `reloadCh` + `cmd/server.go` 主循环驱动重建 | [conf/watch.go:26-66](conf/watch.go#L26-L66) 在事件 goroutine 内 race `vc` | W2 模式 B |
| Task 接受 ctx | `common/task/task.go`: `Execute func(ctx) error` | [common/task/task.go:80-103](common/task/task.go#L80-L103) goroutine 不可取消 | W3.2 |
| MinTraffic 阈值传参 | `core/user.go:86` 每次 GetUserTrafficSlice 传入 threshold | xray/sing 共享 map(裸,无锁) | W2.2/W2.3 |
| resty 默认 Transport | `api/v2board/panel.go` 用 resty 默认值（保留 HTTP/2、Proxy） | [api/panel/panel.go:44](api/panel/panel.go#L44) 替换整 Transport | W1.6 |
| limiter.Wait 无 maxWait | `common/rate/conn.go:21-28` Wait 在 I/O 前 | [common/rate/conn.go:47-61](common/rate/conn.go#L47-L61) WaitMaxDuration I/O 后 | W3.5 |
| 信号驱动重建而非 mutate `c.info` | `node/task.go:64-76` 发 ReloadCh | [node/task.go:95](node/task.go#L95) 直接 `c.info = newN` | W2.4 + 模式 B |

⚠️ **v2node 也带共享缺陷，不能直接照抄**：
- `common/crypt/aes.go` 同样的 base64 长度 bug
- 速率限制器也使用 quantum=capacity 的 1 秒桶（但因 Wait 前置，影响较小）
- LinkManager/Counter Load-then-Store race 仍然存在

---

## 8. Lens 三视角对抗验证

所有 62 项均经至少 2/3 lens 复核（correctness / impact / repro）。少数（#15、#16、#22、#26、#28、#29、#31、#42、#48、#52、#54、#57、#58、#62）在影响幅度或触发条件上做了下调，但根因均已确认。

---

## 9. 后续动作

- 修复实施按 `~/.claude/plans/cryptic-weaving-llama.md` 中定义的 5 个 Wave PR 执行
- 每个 Wave PR 合并后更新本文档顶部"修复进度跟踪"表对应行（⚪ → 🟡 → 🟢）
- 整体回归验证：`GOEXPERIMENT=jsonv2 go test -race -count=1 ./...` + Docker 构建 + `example/` 端到端
