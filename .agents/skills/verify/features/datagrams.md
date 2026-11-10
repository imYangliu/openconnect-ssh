# UDP 与 VPN ping

SOCKS5 UDP 和 `och ping` 复用后台的原生 VPN 会话，不创建系统 TUN 或修改路由。

## Sub-features

- `vpn-udp` 使用连接式用户态 UDP socket，保留数据报边界。
- `vpn-icmp` 发送真实 Echo 并匹配来源、类型、序号和随机载荷。
- `ping-cli` 接受指定 VPN 与 IP 或域名，提供次数、间隔、超时、部分统计和退出状态。
- `atrust-resource-check` 对 IPv4 UDP 和 ICMP 使用现有 L3 资源匹配器。
- `tcping-cli` 通过指定 VPN 建立并立即关闭 TCP 连接，统计后端拨号耗时和连接成功次数。

## How to get to it (user POV)

运行 `och ping --via NAME IP_OR_DOMAIN`。默认尝试四次；参数须放在目标之前。UDP 客户端向现有 mixed 端口协商 SOCKS5 UDP ASSOCIATE，保持 TCP 控制连接打开。

运行 `och tcping --via NAME HOST PORT` 检查 TCP 端口，默认同样尝试四次。它不发送目标应用数据，也不使用 SSH 配置。

## Driving it with isolated tests

从仓库根目录运行 `python3 .agents/skills/verify/scripts/verify.py`。原生测试连接两个用户态数据包栈，验证 IPv4、IPv6、UDP 空数据报、真实 Echo、隧道 DNS、早到回包和取消。代理测试使用本地 UDP 服务；CLI 测试使用临时 HOME、编译产物和注入会话，不访问真实网关或个人 SSH。

## Gotchas

aTrust 当前的 UDP 和 ICMP 限 IPv4 且须匹配网关 IP 资源。SOCKS5 分片和 Fake IP 不受支持。ping 统计报告探测尝试，初始化失败不代表发出了 ICMP 包。域名每次探测可重新解析。测试通过不证明真实校园网关允许该目标或协议。
