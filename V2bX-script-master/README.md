# V2bX 安装脚本

V2bX 节点服务端一键安装与管理脚本，适用于 Ubuntu / Debian / CentOS / Alpine / Arch 系统。

## 一键安装

```bash
wget -N https://raw.githubusercontent.com/Shannon-x/V2bX/dev_new/V2bX-script-master/install.sh && bash install.sh
```

## 文件说明

| 文件 | 说明 |
|------|------|
| `install.sh` | 一键安装脚本，下载并部署 V2bX |
| `V2bX.sh` | 管理脚本，安装后可通过 `V2bX` 命令调用 |
| `initconfig.sh` | 交互式配置文件生成脚本 |
| `V2bX.service` | systemd 服务文件 |

## 更新旧节点的路由规则

默认路由不再启用 `block-ads` / `geosite:category-ads-all`，避免封禁
`ads.tiktok.com` 等正常业务网站。

已安装的节点先使用菜单 **14「升级 V2bX 维护脚本」** 获取新版脚本，
然后使用菜单 **19「更新路由禁止规则」**，或执行：

```bash
V2bX routerule
```

更新会备份 Xray 当前的路由文件，以新规则替换旧的 `block` 出站规则，
清除其中的广告分类封禁（也支持没有 `ruleTag` 的旧配置），并保留非 `block`
的自定义分流和默认出站。重复执行不会恢复广告封禁。
脚本会重启 V2bX 使配置生效；启动失败时沿用原有的自动回滚流程。

该更新处理的是脚本管理的 Xray 路由规则，面板下发或自行配置在其他出站上的
广告拦截规则需在对应位置调整。
