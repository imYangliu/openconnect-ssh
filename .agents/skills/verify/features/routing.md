# 混合代理与分流边界

一个回环端口接受 SOCKS5、HTTP CONNECT 和普通 HTTP，固定绑定 VPN。

## Sub-features

- `mixed-socks` 已实现 TCP CONNECT。
- `mixed-socks-udp` 已实现 UDP ASSOCIATE，固定 VPN、来源绑定、有界多目标与 TCP/VPN 生命周期清理。
- `mixed-http-connect` 已实现隧道转发。
- `mixed-http` 已实现普通 HTTP 代理。
- `system-routes` 原生路径不修改系统路由；旧方案移至 legacy。
- `fakeip` 未实现。

## How to get to it (user POV)

配置 listeners.NAME，再用 SOCKS5 或 HTTP 客户端连接。

## Driving it with isolated tests

Preconditions:

在仓库根目录使用 `python3 .agents/skills/verify/scripts/verify.py`，具备 SKILL.md 所列工具。

统一入口运行真实本地代理协议测试，session 工厂替代外部 VPN。验收固定 profile、预读数据、失败响应、HTTP 头处理与半关闭。

`go test ./internal/proxy` 使用真实回环 TCP 控制连接和 UDP 服务验证 UDP ASSOCIATE、多目标、域名头、空数据报、分片丢弃、来源固定、上限、超时和清理。原生 VPN 数据包覆盖见[UDP 与 ping](datagrams.md)。

## Gotchas

不要把代理端口称为自动全局代理。它不会重新应用 SSH 规则；上游负责分流。其他本机进程也能访问回环端口。
