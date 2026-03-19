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
