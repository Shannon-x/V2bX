# 性能优化与资源管理

本次优化参考了用户提供的 soga 2.17.2 二进制样本，SHA256 为
`dd1b309c55548ff98ae9294c15b99209271c10221a106f073d47eba64dc2899a`。
相关官方发布说明：[2.13.4](https://github.com/vaxilu/soga/releases/tag/2.13.4)、
[2.15.6](https://github.com/vaxilu/soga/releases/tag/2.15.6)、
[2.17.2](https://github.com/vaxilu/soga/releases/tag/2.17.2)。

静态检查发现，样本包含 Go/cgo 与 Rust 面板组件之间的接口，以及 Go Reality/QUIC
符号。可借鉴的方向是隔离面板适配层、保留成熟协议组件，并分别测量连接状态、缓冲区和
资源生命周期。我们没有还原其私有内存分配、调度或计费算法；下述实现均针对 V2bX。

## 无需调参即可生效的改动

- 流量更新使用每用户原子 dirty 标志。上报周期扫描用户表，只提取有更新的计数器。
  连接保留计数存储指针，避免每个数据块都写共享 map 和分配内存。原子读取并清零、
  低于阈值的流量累积、上报失败回填均保留。
- Xray 优先复用连接管理器，无阻断规则时跳过相关检查；统计 writer 使用上游类型，
  使 Linux splice 能识别其计数器。与 EOF 一起返回的数据也会保留并计数。
- 定时任务不重叠执行。停止时取消 context，关闭节点和限速器前等待回调结束。
  周期变更会安全地重新调度，不在回调内部关闭自身任务。不支持 context 的旧回调
  必须执行完毕，关闭操作才会返回。
- sing-box 一次应用完整用户快照，成功更新只重建一次监听器。候选配置独立复制，
  更新失败时保留原用户状态并尝试恢复监听器，计数键包含节点标识。
  成功返回空用户数组会移除用户；HTTP 304 则保留原快照。
- 面板用户列表按流解析并限制用户数量，完整校验后才接受 ETag。无效、截断、null
  或缺失的用户数组不会被当成空快照。HTTP 响应体在自动 gzip 解压后受大小限制，
  包括 Resty 在内存中缓冲的响应。
- 原生 Hy2 缓存解析后的 TLS 证书，握手不再读取 PEM 文件。后台每 30 秒检查一次；
  续期错误保留最后有效的证书与私钥，成功替换后约 30 秒内生效。
  Xray 已有独立证书缓存。
- 原生 Hy2 在构造失败或关闭时释放 UDP socket、刷新任务和 masquerade HTTP(S)
  监听器，同时关闭被接管的 HTTP 连接。辅助监听器绑定失败返回错误，不再终止进程。
- 配置监听支持原子替换文件，合并连续事件，并在关闭时等待正在执行的重载。
  符号链接及软链接目录额外每秒检查一次目标，弥补部分系统遗漏替换事件的问题。
  完整重载先校验，再停止旧运行实例；构造或启动失败则恢复此前配置。
  完整重载可能中断连接，不属于无中断监听器切换。运行时回滚不会还原被就地修改的外部文件。

## 迁移现有 Xray 配置

新安装使用与 Go 配置一致的默认值：`handshake=4`、`connIdle=120`、`bufferSize=128`。
仅更新二进制不会覆盖 `/etc/V2bX/config.json` 中已有的参数。

预览迁移：

```sh
V2bX tune -c /etc/V2bX/config.json --profile balanced
```

核对列出的字段后应用：

```sh
V2bX tune -c /etc/V2bX/config.json --profile balanced --apply
```

`balanced` 将 Xray 管道 `bufferSize` 设为 128 KiB、`connIdle` 设为 120 秒；
`low-memory` 使用 64 KiB 缓冲区，空闲超时相同。这些是主动选择的调参方案，不保证提升吞吐。
握手参数、日志、路由、其他核心、节点、Includes 和未知 JSON 属性均保留，不会拉取 Includes。
应用时会重写为格式化 JSON 并去掉注释，同时生成带时间戳、限制访问权限的备份，完整保留
原文件字节。命令拒绝符号链接，采用原子替换，不输出凭据；重复应用相同方案不产生改动。

服务的配置监听器会检测变更并执行完整重载。若以 `--watch=false` 启动，需手动重启。
回滚时，将命令输出的备份恢复到原配置路径，再重载或重启。节点通过下述检查前不要删除备份。

管道容量是队列阈值，并非每连接固定分配量，也不是总内存硬上限。缩小队列可减少下游读取
缓慢时滞留的数据，但也可能降低吞吐；不能用该参数乘以用户数估算 RSS。

## Xray Hysteria2 接收窗口

原生 Hy2 的 YAML 不控制 Xray Hysteria2 传输层。Xray 节点现在可在 `EnableTFO`
等节点选项旁配置 `Hysteria2QUIC`：

```json
{
  "Core": "xray",
  "Hysteria2QUIC": {
    "InitStreamReceiveWindow": 1048576,
    "MaxStreamReceiveWindow": 8388608,
    "InitConnectionReceiveWindow": 4194304,
    "MaxConnectionReceiveWindow": 20971520,
    "MaxIncomingStreams": 512,
    "MaxIdleTimeout": 30
  }
}
```

以上仅为实验起点，不会自动成为默认配置。省略字段或设为零会保留当前锁定核心的对应默认值：
流初始/最大窗口均为 8 MiB，连接初始/最大窗口均为 20 MiB，入站流数为 1024，空闲超时为
30 秒。初始窗口不能超过实际生效的最大窗口；窗口单位为字节，流数为数量，超时单位为秒。
本地窗口设置保留面板下发的带宽、拥塞控制和混淆参数。测试会检查最终核心 protobuf，
不只检查 JSON 是否能解析。

窗口应依据带宽时延积和真实协议压测选择。低于所需在途数据量的窗口会限制高 RTT 链路吞吐。
这些流控上限并不代表启动时预分配相应内存。

不需要域名覆盖或识别的 Xray 节点仍可设置 `DisableSniffing=true`。由于路由可能依赖嗅探，
本次不会自动关闭它。`EnableTFO` 现已传递到 TCP 和 WebSocket 的 socket 配置；实际效果
仍取决于客户端与操作系统支持。

## 面板响应上限

`MaxResponseBodyBytes` 和 `MaxUsers` 是可选 API 配置字段，可放在扁平节点字段中，
也可放在嵌套配置的 `ApiConfig` 内。零值分别采用每次 HTTP 响应 64 MiB、每份用户快照
500,000 人的默认上限，负数会被拒绝。合法面板数据确实超过上限时再主动提高。
这些上限约束解析过程的临时开销，不是整个进程的 RSS 上限。

## 测量与验收

诊断默认关闭，按需启用：

```sh
V2bX server -c /etc/V2bX/config.json --pprof-listen 127.0.0.1:6060
curl -fsS http://127.0.0.1:6060/debug/runtime
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
go tool pprof 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'
```

监听地址只接受明确的回环 IP，不应通过代理暴露到公网。`/debug/runtime` 提供堆内存、
累计分配、栈、goroutine 和 GC 计数；内核 socket 内存与 RSS 需另行在主机测量。

本地组件基准环境为 macOS arm64、Apple M5、Go 1.26.4、`GOEXPERIMENT=jsonv2`：

| 路径 | 优化前 | 优化后 |
| --- | --- | --- |
| 流量 dirty 标记 | 64 B/op，2 allocs/op | 0 B/op，0 allocs/op |
| 原生 Hy2 证书查询 | 约 8.7 KB/op，117 allocs/op | 0 B/op，0 allocs/op |

本地微基准中，扫描 10,000 个计数器、其中 100 个用户活跃，每次上报约需 70–78 µs。
这些测量证明组件的内存分配减少，不能据此推算 Linux 内存节省比例或整体 TLS/吞吐提升。

2026-11-04 的最终本地验证通过了全量 `go test ./...`、所有改动包的 `-race`、
Linux amd64 全核心及 Xray 单核心交叉构建，以及迁移命令的预览、应用、原样备份、
大整数保留和重复执行检查。原有未跟踪文件 `limiter/zz_perf_probe_test.go:217` 的
自赋值会使标准 `go vet ./...` 失败；保留该文件，通过临时 overlay 隔离此探针后 vet 通过。

部署验收应在相同服务器、客户端、路由、协议、连接数、流量和 RTT 下对比改动前后：

1. 分别测试 Reality/Vision、VLESS WS 和 Hy2，以 1,000、5,000、10,000 条实际连接
   覆盖空闲、繁忙、慢速读取和短连接场景。
2. 记录 RSS/PSS、存活堆、分配速率、GC CPU、内核 socket 内存、FD/goroutine/线程数、
   吞吐量，以及连接建立耗时的 p95/p99。
3. 核对流量总量、上报失败与回填、限额、用户删除、证书续期、无效配置回滚和反复重载。
4. 连接反复建立与关闭后资源数量应稳定；完成 24 小时运行验证后，再广泛采用更小的缓冲区
   或窗口。如果吞吐或延迟退化，回退调参方案。

上游 Linux splice 在 `ReadFrom` 结束时才累计本次复制字节。writer 兼容修复保留这部分最终
计数，但没有新增持续 splice 连接的周期采样。仍需 Linux 协议级验证，macOS 单元测试不能
证明该运行时行为。

专用节点打包时可以只构建所需核心，例如：

```sh
GOEXPERIMENT=jsonv2 go build -tags xray -trimpath -o /tmp/V2bX-xray .
docker build --build-arg V2BX_BUILD_TAGS=xray -t v2bx:xray .
```

默认 Docker 构建仍包含所有受支持的核心。二进制变小不意味着高并发 RSS 会同比降低；
ACME provider 仍被包含在构建中。
