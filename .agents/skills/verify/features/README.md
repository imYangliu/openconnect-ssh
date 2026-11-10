# 原生 OCH Feature Map

此目录描述原生重写，替代旧版外部 OpenConnect 的验证地图。已实现不等于真实网关已验证。执行结果见[验证记录](../verification.md)，后续以新的 report.json 为准。

| 功能 | 范围 |
| --- | --- |
| [首次使用](onboarding.md) | 认证发现、配置向导、状态保存、服务恢复与可选 SSH 集成。 |
| [配置](configuration.md) | 原生 YAML、引用检查及配置拒绝。 |
| [SSH](proxy.md) | 固定去向、自动规则、IPC 数据流。 |
| [诊断](doctor.md) | 配置和后台快照、实时路由、VPN 与 TCP 分层检查、原生 SSH 和转发监听探测。 |
| [原生端口转发](forwarding.md) | 原生 SSH 正向、反向、共享会话与直接 VPN TCP 转发。 |
| [VPN 会话](vpn.md) | 并发共享与原生后端，真实认证单独验收。 |
| [混合代理](routing.md) | SOCKS5、CONNECT、普通 HTTP 与固定 profile。 |
| [UDP 与 ping](datagrams.md) | SOCKS5 UDP、用户态 UDP 和 ICMP、指定 VPN 的域名探测。 |
| [安装](installation.md) | 原生构建安装和旧方案归档。 |

从仓库根目录运行 `python3 .agents/skills/verify/scripts/verify.py`。它使用临时 HOME、Unix socket 和 loopback 服务，不修改系统网络或个人 SSH。记录动作、退出码、精确数据结果和副作用；未运行的入口保持未验证。每个功能文件保留四个固定 H2。
