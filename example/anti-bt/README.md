# V2bX 反 BT / DHT 配置包

针对机房 P2P 滥用投诉（典型措辞：「检测到 BitTorrent DHT 协议交互，捕获到
bencode/KRPC 报文」）的一整套配置与代码级修复。

---

## 一、为什么老规则挡不住

投诉里点名的是 **DHT**。DHT 是 bencode 编码的 KRPC 报文跑在 UDP 上（BEP 5），
和 BT 的 TCP 握手完全是两回事。逐条对照三个内核的实际能力：

| BT 流量类型 | xray 能识别？ | sing-box 能识别？ | hysteria2 能识别？ |
|---|---|---|---|
| TCP 明文握手 `\x13BitTorrent protocol` | 能 | 能 | 不能 |
| TCP 加密握手（MSE/PE） | 不能 | 不能 | 不能 |
| uTP（BEP 29） | **上游代码有 bug，永不命中** | 能 | 不能 |
| UDP Tracker（BEP 15） | 原本没有嗅探器 | 能 | 不能 |
| **DHT / KRPC（BEP 5）** | **没有嗅探器** | **没有嗅探器** | 不能 |

（上表是**打补丁前**的原生能力。本仓库的修复把 DHT / UDP Tracker / uTP
三类在 xray、sing-box、hysteria2 三条路径上全部补齐，见第二节。
唯一仍然无解的是 MSE/PE 加密的 TCP peer 连接——它在字节层面没有稳定特征。）

所以 `route.json` 里那条 `{"protocol":["bittorrent"]}`，在打补丁之前实际只能挡住
**明文 TCP 握手**这一种情况。而现代 BT 客户端默认开协议加密，DHT 更是从头到尾
不经过 TCP。规则写得再全，DHT 也一个包都拦不住。

### xray uTP 嗅探器的 bug

`xray-core` 的 `SniffUTP` 里有这么一行：

```go
if math.Abs(float64(time.Now().UnixMicro()-int64(timestamp))) > float64(24*time.Hour) {
    return nil, errNotBittorrent
}
```

两个问题叠在一起：

1. 左边是「64 位微秒时间戳」减「uTP 头里 32 位截断的时间戳」，量级约 `1.7e15`；
   右边 `24*time.Hour` 是纳秒计的 `Duration`，值 `8.64e13`。单位不同、量级差 20 倍，
   **这个条件对任何输入都恒为真**，合法 uTP 包也一律被判为「不是 BT」。
2. 更根本的是，uTP 头里的 `timestamp_microseconds` 是**发送方本机的单调时钟**
   （libutp 的 `UTP_GetMicroseconds`，POSIX 上基于 `CLOCK_MONOTONIC`），
   跟 Unix 纪元没有任何关系。所以拿它和 `time.Now()` 比大小，
   无论怎么修单位都是错的——改成 32 位回绕比较也只会变成一个约 28% 通过率的随机闸门。

本仓库的实现直接不看时间戳（sing-box 也不看），改为收紧结构性约束：
只认 ST_SYN、校验扩展链、connection_id 非 0、wnd_size 在合理区间、
seq_nr 非 0 且 ack_nr 为 0。对随机数据的误报率约 1e-11 量级。

### 还有一个更致命的结构性问题

xray 的 UDP 入站（shadowsocks / trojan / socks）对**每个客户端会话只建立一条 link**：

- `transport/internet/udp/dispatcher.go` 的 `getInboundRay` 完全忽略 `dest` 参数，
  命中缓存就直接复用；
- `proxy/shadowsocks/server.go` 在 cone 模式下还会把 `dest` 钉死在首包目标上。

于是「嗅探 + 路由 + limiter 判定」在一个 UDP 会话里**只发生一次**，判定对象是第一个包。
BT 客户端的首包几乎总是 DNS 查询或 QUIC 握手，会话被判成 `dns`/`quic` 之后，
成千上万个发往不同 peer 的 DHT 包沿着同一条 link 出站，
既不会再被嗅探，也不会再过任何一条路由规则。

**结论：纯 `route.json` 规则在架构上就管不住 UDP 逐包行为。**

### 关于 geosite 分类：一个需要澄清的点

发布件里的 `geosite.dat` 来自 **Loyalsoldier/v2ray-rules-dat**
（见 `.github/workflows/release.yml`），`category-public-tracker`、
`category-ipfs`、`category-antivirus` 这三个分类**都是有的**，
线上按发布件安装的节点引用它们不会有问题。

需要注意的只有一种情况：换成了自建或较老的 `geosite.dat`
（比如 v2fly 官方那份，或者仓库里 `example/geosite.dat` 这个过期样本）。
缺分类会让 xray 在 `RouterConfig.Build()` 阶段直接失败，
而 `core/xray/xray.go` 对这个错误是 `log.Panic` —— 整个 V2bX 起不来，
管理脚本随后静默回滚。运维看到「已替换禁止规则」的绿字，实际一直跑在旧规则上。

所以管理脚本改成了「落地前逐个校验分类是否存在，缺什么跳过什么」，
而不是让整份规则一起炸掉。这是防御性措施，不是本次投诉的原因。

## 二、修复分三层

### 第 1 层：代码（已合入本仓库）

| 文件 | 改了什么 |
|---|---|
| `core/xray/app/dispatcher/bittorrent.go` | 新增 DHT(KRPC)、UDP Tracker 嗅探器，并重写 uTP 嗅探（修掉上游那个恒为真的时间戳判断）。统一返回协议名 `bittorrent`，现有规则无需改动即可命中。 |
| `core/xray/app/dispatcher/bittorrent_filter.go` | **逐包**过滤出站 UDP 里的 BT 报文，绕开「一个 UDP 会话只判定一次」的结构性限制。命中只丢该包，不掐断整条会话，用户的 DNS 不受影响。 |
| `core/sing/hook.go` | UDP 路径原本把 `m.Destination.Network()`（恒返回字面量 `"socks"`）当协议名传给 `CheckProtocolRule`，面板的 protocol 审计规则在 UDP 上永远不可能命中。改为使用嗅探结果 `m.Protocol`。 |
| `core/sing/bittorrent_filter.go` | sing-box 的 bittorrent 嗅探器只注册了 TCP 握手 / uTP / UDP Tracker，**没有 DHT**。这里在入站 `PacketConn` 的读取侧逐包过滤。包装顺序必须在流量计数器之前——sing 的 `UnwrapCountPacketReader` 会剥掉计数器并收走它的 `CountFunc`，剥到本层因为不实现 `ReaderWithUpstream` 而停下，统计不受影响、过滤层也留在链路里。同时实现 `PacketReadWaitCreator` 以保住零拷贝快路径。 |
| `common/throttle/` | 按 key 限频的日志闸门。BT 丢包每秒可达上百次，逐条记日志会把丢包路径变成 I/O 瓶颈。 |
| `core/hy2/rule_enforce.go` | **hy2 原本完全不执行面板规则**——`core/hy2` 里对 limiter 的引用只有限速和在线统计，`block_domain` / `block_ip` / `block_port` 一条都不走。这里用 hysteria 自己的两个扩展点补上：`server.Outbound` 的 `CheckUDP()` 是**逐包**调用的（带每会话 256 条地址缓存），拿来做目的地址拦截；`server.RequestHook` 的 `UDP(data, reqAddr)` 能拿到**首包原始载荷**，是 hy2 上唯一能按报文特征识别 BT 的位置。 |
| `common/bittorrent/` | 与内核无关的 BT 报文识别（DHT/KRPC、UDP Tracker、uTP），xray 与 hy2 两条路径共用。 |
| `conf/limit.go` / `limiter/` | 新增开关 `LimitConfig.BlockBittorrentUDP`；面板下发的 protocol 规则含 `bittorrent` 时自动启用。 |

启用逐包过滤，在 `config.json` 的节点里加：

```json
"LimitConfig": {
    "BlockBittorrentUDP": true
}
```

（面板已经下发了含 `bittorrent` 的 protocol 审计规则的话，不加也会自动生效。）

这个开关同时作用于三个内核：xray 与 sing-box 的逐包 UDP 过滤、hy2 的 UDP 首包拒绝。
默认关闭，开销见下面的实测数据。
面板的 `block_domain` / `block_ip` / `block_port` 规则在 hy2 上现在无条件生效，
不需要这个开关——那本来就是这些规则应有的行为，此前只是没接线。

#### 延迟：反而是变好的

逐包过滤那 3.3 ns 是纯 CPU 计算，不涉及系统调用、锁、额外缓冲或批处理改变，
换算成延迟低于任何网络路径的测量噪声（同城 RTT 约 100 µs，差 3 万倍）。

真正会被用户感知的是**每条 UDP 流第一个包的嗅探耗时**，而这一项打完补丁是**变快**的。
原因在 `sniffer()` 的循环（`core/xray/app/dispatcher/default.go`）：
嗅探链返回 `common.ErrNoClue`（「说不准」）时会 `totalAttempt++` 然后**再读一次**，
最多阻塞到 200ms 的 `cacheDeadline` 用完。

上游 `bittorrent.SniffUTP` 对**长度不足 20 字节**的包返回 `ErrNoClue`，
而短 UDP 包在真实流量里很常见（DNS 响应、游戏心跳、各类小报文）。
也就是说打补丁前，这类流的首包最坏要白等 200ms。
本地实现对拿不准的一律给确定性错误，嗅探立刻结束。
`core/xray/app/dispatcher/sniffer_latency_test.go` 把这个差异锁住了。

#### 性能实测

数据来自仓库里的基准测试（Apple M 系列，`go test -bench -benchmem`），
可以自己重跑核对：

```
go test ./common/bittorrent/            -run XXX -bench SniffUDP  -benchmem
go test ./core/xray/app/dispatcher/     -run XXX -bench UDPRead   -benchmem
go test ./core/hy2/                     -run XXX -bench RuleOutbound -benchmem
```

| 场景 | 开销 | 内存分配 |
|---|---|---|
| 报文判定，正常流量（QUIC 1350B / DNS） | **2.7 ns/包** | **0** |
| 报文判定，最坏构造（`d` 开头 `e` 结尾的 1350B 垃圾） | 20 ns/包 | 0 |
| xray 逐包过滤层净增开销（4 包一批，45.2 → 58.3 ns） | **约 3.3 ns/包** | **0** |
| hy2 `CheckUDP`，节点未配任何阻断规则 | 8.5 ns/次 | 0 |
| hy2 `CheckUDP`，配了 2 条正则 + 2 个 CIDR + 2 个端口段 | 251 ns/次 | 0 |

换算一下：1 Gbps 的 1400 字节 UDP 约 89,000 包/秒，
逐包过滤的净开销是 89,000 × 3.3 ns ≈ **0.3 毫秒/秒，即单核的 0.03%**。
10 Gbps 也就 0.3%。**全部路径零堆分配，不会带来额外内存占用。**

hy2 的 `CheckUDP` 是按「会话 + 目的地址」缓存的（hysteria 每会话缓存 256 条），
不是每个包都算一遍；而且没配阻断规则时会在一次原子读之后立刻返回。

关于 sing-box：过滤层实现了 `N.PacketReadWaitCreator`，
把底层 conn 的零拷贝读等待器包一层原样交出去，
所以 `CopyPacket` 仍然走 `WaitReadPacket` 快路径，**不会退化成每包一次缓冲区分配**
（`core/sing/bittorrent_filter_test.go` 里有专门锁这一点的测试）。

被丢弃的 BT 包不计入用户流量——它们从未离开本机，不应计费。

### 第 1.5 层：默认配置本身的两个坑（已修）

**`example/route.json` 里有一条会打死所有域名访问的规则。**

```json
{ "type": "field", "outboundTag": "socks5-warp", "domain": [""] }
```

xray 的域名匹配对空字符串是**子串匹配**，`""` 是任何域名的子串——这条规则
匹配**所有域名**，把全部域名类流量导向 `socks5-warp`，
也就是 `127.0.0.1:40000`，而那个端口默认没有任何进程监听。

这不是纯样例问题：`.github/workflows/release.yml` 会把 `example/*.json`
打进发布包，`install.sh` 又在 `/etc/V2bX/route.json` 不存在时把它复制过去。
新装机器在跑配置生成器之前，用的就是这份。
已改成加固后的规则集，并加了回归测试
（`conf/script_defaults_test.go` 的 `TestNoEmptyDomainCatchAll`）。

**脚本生成的默认 route.json 缺 SMTP 和 BT 端口防护。**
原来只有一条 `regexp:(Subject|HELO|SMTP)` 的域名正则——域名正则拦不住 SMTP，
真正管用的是端口规则，而端口 25/465/587 一条都没有。BT 端口段同样没有。

现在 `V2bX.sh` 与 `initconfig.sh`（两份是复制粘贴关系，容易漂移）
生成的默认值统一为 13 条规则：

```
block-private          防 SSRF / 内网穿透（geoip:private）
block-private-cidr     显式内网段，兜住 AsIs 模式下的裸 IP
block-bt-protocol      BT 协议嗅探
block-bt-tcp-ports     6881-6999 / 51413
block-smtp             25 / 465 / 587
block-bt-dht-bootstrap DHT 引导节点域名
block-bt-pt-geosite    category-public-tracker / -pt / -ipfs
block-bt-tracker-domain 常见 tracker 与 BT 站点
block-ads              category-ads-all
block-antivirus        category-antivirus
block-competitor       category-vpnservices
block-abuse-regexp     原有的迅雷/临时邮箱/竞品/统计正则
final                  IPv4_out
```

hysteria2 的默认 ACL 也从 3 行换成了完整的 44 条。
`conf/script_defaults_test.go` 会校验两个脚本生成的默认值彼此一致、
与仓库模板一致，并且 ACL 能通过 hysteria 自己的解析器。

### 菜单 15 与菜单 19 现在同源

管理脚本有两个会写路由规则的入口：

| 菜单 | 做什么 | 落地方式 |
|---|---|---|
| 15 生成 V2bX 配置文件 | 整份重写 `route.json` | 禁止规则 + `final` 出站 |
| 19 更新路由禁止规则 | 只替换 `outboundTag == "block"` 的部分 | 保留运维自己加的分流规则 |

以前两边各写各的：15 是写死的 heredoc，19 是 jq 动态生成，规则集并不一样，
结果就是「生成的配置比更新后的弱」。现在两个入口都调用同一个
`build_block_rules`，静态 heredoc 已经删除。

`conf/script_defaults_test.go` 把这件事钉死——它会**真的把脚本里那段
shell 抽出来跑一遍**，然后断言：

- 两个脚本（`V2bX.sh` 与 `initconfig.sh`，复制粘贴关系）的权威规则段逐字节一致
- 两者生成的 `route.json` 完全一致，且与 `example/route.json` 一致
- **菜单 19 作用在菜单 15 的产出上是幂等的**——这就是「两个入口防护一致」的直接证明
- 菜单 19 换掉禁止规则的同时，保留 `warp` / 流媒体分流之类的自定义规则
- 无论 `geosite.dat` 新旧、甚至完全缺失，基线防护（private / BT 协议 / BT 端口 /
  SMTP / DHT 引导 / tracker 域名 / 滥用正则）都必须在
- 脚本里不允许再出现静态的 `route.json` heredoc
- hy2 的 ACL 在菜单 15 与菜单 19 之间一致，并且能通过 hysteria 自己的解析器

关于 geosite 分类的裁剪行为：

| geosite.dat 状态 | 产出 |
|---|---|
| 发布件自带（Loyalsoldier） | 12 条禁止规则，全分类可用 |
| 较老 / 自建，缺 3 个分类 | 11 条，逐条提示跳过了哪个分类 |
| 完全缺失 | 8 条，保留全部硬编码防护，并大声告警 |

### 域名规则实测：死规则和误伤都清掉了

`block-abuse-*` 那批**不走 geosite**，它们是 `domain:` / `regexp:` 域名匹配器。
但这些正则是从旧配置继承的，拿真实 router 逐条跑过之后发现两类问题：

**一、死规则。** xray 的 `regexp:` 匹配的是**域名**，不是 URL。
所以 `peer_id=`、`info_hash`、`get_peers`、`announce.php?passkey=`、`magnet:`
这些永远不可能出现在域名里；
`regexp:(^.@)(guerrillamail|...)` 更是要求域名里带 `@`——实测一个样本都命中不了，
纯粹给运维「已经挡住了」的错觉。

**二、误伤。** `(.*\.||)(duba)\.(com)` 这种写法前缀有两个空分支等于可有可无，
末尾也不锚定，而 xray 的 `regexp:` 是**非锚定**匹配。实测结果：

| 正则 | 被误伤的域名 |
|---|---|
| `(.*\.\|\|)(rising\|kingsoft\|duba\|...)` | `notduba.com`、`xduba.com`、`myrising.com`、`duba.com.legit-cdn.net` |
| `(.*\.\|\|)(miaozhen\|cnzz\|talkingdata\|umeng)` | `myumeng.com`、`nocnzz.com`、`umeng.com.evil.net` |
| `(.*\.\|\|)(netvigator\|torproject)` | `nottorproject.org`、`torproject.org.cdn.net` |
| `(.+\.\|^)(360\|so)\.(cn\|com)` | `so.com.evil.net` |

注意最后一列那种 `目标域名.攻击者域名` 的形式——伪造后缀就能骗过匹配。

**改法**：能精确列举的一律改用 `domain:`（命中自身与子域，不命中前缀相似和伪造后缀，
而且比正则快），真正需要模式的才留正则并加锚点：

```
block-abuse-domain    32 个 domain: 条目（迅雷/杀软/统计/竞品/临时邮箱）
block-abuse-regexp    2 条锚定正则：
                      ^(api|ps|sv|...)(\.map)?\.(baidu|n\.shifen)\.com$
                      (^|\.)[a-z0-9-]*(torrent|ed2k)[a-z0-9-]*(\.|$)
```

关键词表里刻意**去掉了 `thunder`**——它会误伤 `thunderbird.net` 这类正规服务，
迅雷相关域名改用精确的 `domain:xunlei.com` / `domain:sandai.net`。

`core/xray/route_template_test.go` 里三条测试锁住这些：
该拦的 26 个域名必须拦住、不该拦的 21 个（含各种前缀相似与伪造后缀）必须放行、
以及禁止正则里不允许再出现域名中不可能存在的字符。

### hy2 节点怎么加防护

**先确认一个致命缺口**：hy2 节点如果没有配 `Hysteria2ConfigPath`，
`core/hy2/node.go` 那段读配置的分支根本不会执行，`serverConfig` 保持零值，
于是 `core/hy2/config.go` 里 `hasACL` 为 false——**ACL 引擎压根不建立**。
这种节点对 BT 完全不设防，而且改 `/etc/V2bX/hy2config.yaml` 对它毫无作用。

菜单 19 现在会自动处理：

```
以下 hysteria2 节点没有配置 Hysteria2ConfigPath，当前处于完全无防护状态: 11
已为这些节点补上 Hysteria2ConfigPath: /etc/V2bX/hy2config.yaml（config.json 已备份到 ...）
/etc/V2bX/hy2config.yaml 不存在，按默认模板创建
hysteria2 ACL 已更新: /etc/V2bX/hy2-node12.yaml（备份 ...）
```

它会遍历**所有** hy2 节点引用到的配置文件（不再只处理第一个），
缺路径的补上、缺文件的按模板创建、已有文件的只替换 `acl:` 段，
`quic` / `ignoreClientBandwidth` / `masquerade` 等原有配置原样保留，
每份都留时间戳备份，重启失败会全部回滚。

hy2 的完整防护由三层叠加：

| 层 | 能挡什么 |
|---|---|
| ACL（配置） | BT 域名、BT 端口、SMTP、UDP 端口白名单 |
| `core/hy2/rule_enforce.go`（代码） | 面板的 block_domain / block_ip / block_port（此前一条都不生效）；`Outbound.CheckUDP` 是**逐包**调用的 |
| `RequestHook.UDP`（代码） | UDP 会话首包的 BT 报文识别，需要 `BlockBittorrentUDP` 开启 |

注意 hysteria 的 ACL 语法从语法层面就写不出「阻断 bittorrent」
（`Protocol` 只有 tcp/udp/both 三个传输层协议，写了会在编译期报
`invalid protocol/port` 并让节点起不来）——按协议识别 BT 的能力只能来自第 2、3 层。

### 第 2 层：配置（本目录的三个模板）

| 文件 | 用途 | 落地位置 |
|---|---|---|
| `route.json` | xray 路由规则，只引用真实存在的 geosite 分类 | `/etc/V2bX/route.json` |
| `hy2config.yaml` | hysteria2 ACL | `/etc/V2bX/hy2config.yaml` |
| `sing_origin.json` | sing-box 路由规则 | `/etc/V2bX/sing_origin.json` |

三个模板都有对应的自动化测试，会用各内核**自己的解析器**验证语法与语义
（`core/xray/route_template_test.go`、`core/hy2/acl_template_test.go`、
`core/sing/route_template_test.go`），语法错误不会等到节点起不来才发现。

**注意事项：**

- `route.json` 必须在 `config.json` 的 xray 内核里配了
  `"RouteConfigPath": "/etc/V2bX/route.json"` 才会被加载。没配的话这个文件形同虚设，
  而且 V2bX 不会有任何报错。
- `sing_origin.json` 是 V2bX 给 sing-box 注入路由规则的**唯一入口**，
  需要在 sing 内核里配 `"OriginalPath": "/etc/V2bX/sing_origin.json"`。
  不配的话 sing-box 出厂状态是**零路由规则**，所有流量直连出站。
- sing-box 1.12 起 inbound 级 `sniff` 已被移除，改成路由动作。模板第一条
  `{"action":"sniff","sniffer":["bittorrent",...]}` 不能删，删了后面所有
  `protocol` 规则永远不会命中。
- hysteria2 的 ACL 语法**从语法层面就写不出「阻断 bittorrent」**
  （`Protocol` 只有 tcp/udp/both 这三个传输层协议），
  写了会在编译期报 `invalid protocol/port` 并让节点起不来
  （`core/hy2/acl_template_test.go` 里有这条反向断言）。
  ACL 只能用「BT 域名黑名单 + BT 端口 + UDP 端口白名单」逼近；
  按协议识别 BT 的能力由上面第 1 层的 `core/hy2/rule_enforce.go` 提供。

### 第 3 层：主机防火墙（真正的兜底）

```bash
cp anti-bt-firewall.sh /etc/V2bX/
bash /etc/V2bX/anti-bt-firewall.sh install
```

三件事：

1. **出站 UDP 端口白名单**（默认 53/80/123/443/853/3478/5349/8443）。
   DHT、uTP、UDP Tracker 全跑在随机高位端口上，白名单一开这三类整体消失。
   **这是对本次投诉最直接有效的一条。**
2. 无视端口，按 bencode/KRPC 报文特征直接丢包，防止 DHT 藏在白名单端口上。
3. 丢弃到常见 BT 端口段的出站 TCP。

这一层与内核无关，xray / sing-box / hysteria2 节点一起覆盖，是唯一能保证
hysteria2 节点不再发 DHT 的手段。

**副作用**（白名单之外的 UDP 会被丢弃，按需自行放行）：

- 客户端经代理跑 WireGuard（默认 51820）
- 联机游戏的 P2P 直连（随机高位 UDP）
- 部分 WebRTC 实现的媒体流走随机高位端口，只放行 3478/5349 不够

调整方式：

```bash
UDP_ALLOW='53,80,123,443,853,3478,5349,8443,51820' \
  bash /etc/V2bX/anti-bt-firewall.sh install
```

裸载荷匹配（`@th,64,32`）需要 nftables >= 0.9，
Debian 11 / Ubuntu 20.04 及以后自带的版本都满足。
若 `nft -f` 报 `syntax error, unexpected @`，说明版本过老，
可以先只保留端口白名单那几条（去掉 KRPC 特征匹配），效果已经覆盖绝大部分场景。

规则只在内存里，重启会丢，需要持久化：

```bash
nft list ruleset > /etc/nftables.conf
systemctl enable --now nftables
```

---

## 三、现有节点怎么升级

分成两条路：**不换二进制**也能先止血，**换了二进制**才有完整防护。

### A. 只改配置（立刻可做，不用重新编译）

这一路只用现有版本的 V2bX 就能生效。

```bash
# 0. 先备份
cp -a /etc/V2bX /etc/V2bX.bak.$(date +%Y%m%d%H%M%S)

# 1. 主机防火墙——三个内核通吃，对本次投诉最直接有效，先上这个
cp example/anti-bt/anti-bt-firewall.sh /etc/V2bX/
bash /etc/V2bX/anti-bt-firewall.sh install
bash /etc/V2bX/anti-bt-firewall.sh status     # 看 counter 是否在涨

# 2. 路由规则。有新版管理脚本就直接：
V2bX routerule
#    没有的话手动替换（xray 内核）：
cp example/anti-bt/route.json /etc/V2bX/route.json
#    确认 config.json 的 xray 内核里配了 RouteConfigPath，没配等于没加载：
jq '.Cores[] | select(.Type=="xray") | .RouteConfigPath' /etc/V2bX/config.json

# 3. hysteria2 节点
cp example/anti-bt/hy2config.yaml /etc/V2bX/hy2config.yaml

# 4. sing-box 节点
cp example/anti-bt/sing_origin.json /etc/V2bX/sing_origin.json
#    并在 config.json 的 sing 内核里加上 OriginalPath，否则这个文件不会被读：
#      "OriginalPath": "/etc/V2bX/sing_origin.json"

systemctl restart V2bX && systemctl status V2bX
```

**必须确认重启后服务是活的。** 配置有问题时 V2bX 会 panic 起不来，
而不是降级运行：

```bash
journalctl -u V2bX -n 100 --no-pager | grep -i 'panic\|geosite\|routing\|acl'
```

### B. 换新二进制（完整防护）

DHT 的逐包识别在代码里，只改配置拿不到。编译并替换：

编译参数与 `.github/workflows/release.yml` 保持一致，尤其是 `-tags`——
少了它三个内核不会被编进去：

```bash
GOEXPERIMENT=jsonv2 go build -v -o V2bX \
  -tags "sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor" \
  -trimpath -ldflags "-s -w -buildid=" .

systemctl stop V2bX
cp V2bX "$(dirname "$(readlink -f /usr/local/V2bX/V2bX 2>/dev/null || echo /usr/local/V2bX/V2bX)")/V2bX"
systemctl start V2bX && systemctl status V2bX
```

更省事的做法是直接走你自己的 release 流程出一个新版本，再用
`V2bX update` 升级，避免手工替换二进制时路径搞错。

然后按需打开 BT 过滤。两种方式任选其一：

```bash
# 方式一：改 config.json，给每个节点的 LimitConfig 加开关
jq '(.Nodes[] | .LimitConfig) |= (. + {"BlockBittorrentUDP": true})' \
   /etc/V2bX/config.json > /tmp/c.json && mv /tmp/c.json /etc/V2bX/config.json
systemctl restart V2bX

# 方式二：在面板的审计规则里加一条 protocol = bittorrent，
#         V2bX 拉到规则后会自动开启，不用动 config.json，也不用重启
```

**灰度建议**：先在一台节点上开，跑 24 小时观察

```bash
# 有没有误伤：正常用户是否有 UDP 类业务报障（视频通话、游戏、HTTP/3 站点）
journalctl -u V2bX --since '24 hours ago' | grep -ci bittorrent
# 机器负载有没有变化
top -bn1 | head -5
```

确认无异常再推全量。有问题随时回退：把 `BlockBittorrentUDP` 改回 `false`
（或删掉面板那条 protocol 规则）重启即可，不需要换回旧二进制。

### 回滚

```bash
bash /etc/V2bX/anti-bt-firewall.sh uninstall      # 撤防火墙
rm -rf /etc/V2bX && mv /etc/V2bX.bak.<时间戳> /etc/V2bX && systemctl restart V2bX
```

管理脚本改配置前也会自己打时间戳备份（`route.json.bak.*` / `hy2config.yaml.bak.*`），
并在重启失败时自动回滚。

---

## 四、验证有没有真的挡住

```bash
# 1. 防火墙命中计数——packets 在涨说明确实拦到了
bash /etc/V2bX/anti-bt-firewall.sh status

# 2. 直接抓包确认服务器不再往外发 DHT。跑 10 分钟，应当零输出。
#    udp[8:4] 是 UDP 载荷的前 4 字节（UDP 头固定 8 字节），
#    0x64313a61 = "d1:a"（KRPC 查询），0x64313a72 = "d1:r"（KRPC 响应）
timeout 600 tcpdump -ni any -A 'udp and (udp[8:4] = 0x64313a61 or udp[8:4] = 0x64313a72)' 2>&1 | head -20

# 3. 确认没有到处握 BT 的 TCP 连接。0x13426974 = 0x13 "Bit"
timeout 600 tcpdump -ni any -A 'tcp[((tcp[12:1] & 0xf0) >> 2):4] = 0x13426974' 2>&1 | head -20

# 若上面的 BPF 表达式在老版 tcpdump 上不被接受，用这个粗一点的替代：
#    看有没有大量发往随机高位端口、目的地址高度分散的出站 UDP，
#    这正是 DHT 扇出的形态
timeout 60 tcpdump -ni any -c 200 'udp and not port 53 and not port 443 and not port 123 and not port 80' \
    | awk '{print $5}' | cut -d. -f1-4 | sort -u | wc -l

# 4. V2bX 侧的丢包日志（逐包过滤开启后，30 秒最多一条，防止刷屏）
journalctl -u V2bX --since '1 hour ago' | grep -i 'bittorrent'

# 5. 确认配置真的加载了，而不是启动失败后被回滚
systemctl status V2bX
journalctl -u V2bX -n 100 --no-pager | grep -i 'panic\|geosite\|routing'
```

---

## 五、各手段的实际效果排序

按对「DHT 大扇出」这一具体投诉的有效性：

1. **主机层出站 UDP 端口白名单** —— 决定性。DHT/uTP/UDP Tracker 一次性全灭，
   且三个内核一起覆盖。
2. **代码层 BT 过滤**（`BlockBittorrentUDP`）—— xray 与 sing-box 是逐包过滤，
   hy2 是 UDP 会话首包拒绝（hysteria 架构上只让 hook 看到首包）。
   三者都不影响正常 UDP 业务，误伤面比端口白名单小得多。
3. **nftables KRPC 特征丢包** —— 补白名单的漏，防止 DHT 藏在 443 之类的端口上。
4. **BT 端口段封禁** —— 有效但容易被绕（客户端可以改端口）。
5. **tracker / DHT bootstrap 域名黑名单** —— 只能拖慢冷启动。
   客户端本地有节点缓存，重启后靠缓存和 PEX 就能重新入网，不需要 bootstrap 域名。
6. **`protocol: bittorrent` 路由规则** —— 打补丁前只能挡明文 TCP 握手；
   打补丁后对 xray 的 UDP 首包有效，但受「一个会话只判定一次」限制，
   必须配合第 2 条才可靠。
7. **geosite tracker 分类** —— 本机 `geosite.dat` 里就没有 `category-public-tracker`，
   写了会让 xray 起不上来。

一句话：**能不能守住，取决于第 1 和第 2 条；其余都是补充。**
