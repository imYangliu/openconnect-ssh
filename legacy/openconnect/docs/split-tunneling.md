# SSH 与 VPN 分流指南

本文区分当前外部 OpenConnect 的系统路由方案与计划中的 OCH 内部连接方案。示例仅供配置参考，本项目没有对真实 VPN 或其他 TUN 工具的共存进行验证。

## 当前 OCH 的范围

OCH 以目标 TCP 端口是否可达决定是否启动 VPN，不检查目标是否命中 CIDR。macOS 的 `routes.extra` 只追加路由，不屏蔽服务端默认路由。配置见[配置说明](configuration.md#routes)。

系统路由按目标地址作用于机器上的流量，不限于触发连接的 SSH 进程。后续的内部连接方案才以 OCH 接收到的连接为分流对象。

## 用外部 OpenConnect 指定网段

OpenConnect 通过 `--script` 指定网络配置脚本。官方文档列出 [vpn-slice](https://www.infradead.org/openconnect/vpnc-script.html) 作为限定流量范围的替代脚本。它默认按指定主机或网段添加路由，而非复制全部服务端路由。

先按照 [vpn-slice 安装说明](https://github.com/dlenski/vpn-slice#installation)安装 OpenConnect 和 vpn-slice，并确认脚本的绝对路径。下面是独立运行 OpenConnect 的示例，需把网关、用户名、脚本路径及网段替换为实际值。

```bash
sudo openconnect --protocol=anyconnect \
  --user=your_vpn_username \
  --script '/absolute/path/to/vpn-slice 10.20.0.0/16' \
  https://vpn.example.com
```

在提示中输入凭据。此命令会实际创建 VPN 和修改系统网络，不属于 OCH 的隔离测试。保持进程在前台，退出时按 Ctrl-C，让断开脚本清理其配置。不要用强制终止代替正常断开。

这不是 OCH 当前的配置字段。OCH 尚未提供自定义 `--script` 的 TOML 选项。独立连接后，可以使用普通 SSH 访问目标；不要同时让 OCH 启动另一条 VPN。若要由 OCH 管理，需要另行实现符合 `connect/status/disconnect` 接口的 `[vpn].helper`。

选择实际需要的最小网段，避免把整个私有地址空间导入 VPN。不要加入 `0.0.0.0/0` 或 `::/0`。域名参数可能使 vpn-slice 查询 VPN DNS 并写入 `/etc/hosts`，断开时再清理。它不是完全不修改主机网络的用户态代理。具体行为见 [vpn-slice 使用说明](https://github.com/dlenski/vpn-slice#usage)。

## 检查分流结果

在连接前、连接后和断开后，分别检查目标地址与一个原本直连的地址。以下 IP 均为示例，需替换。macOS 使用：

```bash
route -n get 10.20.0.10
route -n get 1.1.1.1
netstat -rn
scutil --dns
```

Linux 使用：

```bash
ip route get 10.20.0.10
ip route get 1.1.1.1
ip rule show
ip route show table all
```

目标地址应指向 VPN 接口，非目标地址应保留连接前的路径，断开后应恢复。检查完整路由表，不能只检查 default 行；更具体的路由也能覆盖原路径。有 IPv6 时还需检查 IPv6 路由和实际使用的 AAAA 地址。

DNS 单独验收。先用目标 IP 检查 TCP 连通，再检查校内域名的解析和访问。Linux 的 DNS 管理方式随发行版变化，应通过实际使用的解析服务检查。

已有 TUN 工具可能通过更具体路由、策略路由或 DNS/Fake IP 改变结果。需同时确认 VPN 网关本身可达、校内目标走正确隧道，以及其他流量保持预期路径。本指南不承诺所有 TUN 工具自动共存。

## sing-box 与 ProxyCommand

2026-11-07 检查 sing-box `testing` 分支提交 `9f1cd1ef75b7999bb118a9322a003f6edce2752d`。其 [`tools connect`](https://github.com/SagerNet/sing-box/blob/9f1cd1ef75b7999bb118a9322a003f6edce2752d/cmd/sing-box/cmd_tools_connect.go) 连接目标，并双向复制 stdin/stdout。源码支持下面的 ProxyCommand 接法，不必先经过本地 SOCKS 监听端口：

```sshconfig
Host campus-example
  HostName 10.20.0.10
  User your_ssh_user
  ProxyCommand /absolute/path/to/sing-box -c /absolute/path/to/ssh-client.json tools connect %h:%p
```

此示例针对 IPv4 目标。配置文件必须事先提供所需的默认 outbound。可通过 `tools -o <tag> connect <address>` 指定 outbound。[`createDialer`](https://github.com/SagerNet/sing-box/blob/9f1cd1ef75b7999bb118a9322a003f6edce2752d/cmd/sing-box/cmd_tools.go) 直接选取 outbound，随后调用 DialContext，因此不能假定该命令会应用普通入站连接的全部 `route.rules`。每次调用创建自己的实例，不等于复用已运行的 sing-box 服务。

本机未安装 sing-box，本轮完成的是源码核查，没有运行上述 SSH 示例，也没有验证真实 VPN 认证。实际使用前应核对所装版本的 `sing-box tools connect --help`。

官方 [OpenConnect endpoint 文档](https://sing-box.sagernet.org/configuration/endpoint/openconnect/)标记该能力始于 1.14.0，关闭 `system` 时使用内部网络栈，`on_demand` 标记为 1.15.0 新增能力。这些版本标记不表示本机或某个稳定发行版已具备相同能力，也不证明 `tools connect` 与该 endpoint 的组合已在本项目验证。

## OCH 后续目标

OCH 计划由 ProxyCommand 接收目标，解析并匹配 CIDR，然后选择直连或内置 AnyConnect/aTrust 连接。SSH 数据通过内部网络栈传输，避免为单个 SSH 连接修改系统默认路由和全局 DNS。

需要分别实现认证、连接复用、并发会话、断线恢复、域名解析和 IPv6。即使不修改系统路由，访问 VPN 网关的底层连接仍受现有系统网络影响。aTrust 参考 [zju-connect](https://github.com/Mythologyli/zju-connect)。这些均为计划，不属于当前已验证能力。
