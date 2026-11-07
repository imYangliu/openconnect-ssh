# 分流方式

原生 OCH 在应用层选择连接路径。`proxy-command --policy NAME` 按域名和 CIDR 分流，未命中直连；`--via NAME` 固定使用指定后端。配置见[配置说明](configuration.md)。混合代理监听固定绑定 VPN，由调用方决定分流。

旧版通过外部 OpenConnect 修改系统路由的方式保留在 [legacy/openconnect](../legacy/openconnect/README.md)。该方式会影响所有命中系统路由的应用，不限于 SSH；追加网段也不代表自动阻止默认路由被接管。

## sing-box 接入

将 OCH 混合监听配置为 sing-box 的 SOCKS5 或 HTTP 代理出口。保留目标域名，才能由 OCH 的固定 VPN 后端处理解析。上游已解析为 IP 时，OCH 按这个真实 IP 连接；不支持还原 Fake IP。

sing-box 的 [协议嗅探](https://sing-box.sagernet.org/configuration/route/sniff/)可以辅助上游规则，但不是 OCH 必需条件。HTTP/TLS 可能提供域名信息，SSH 嗅探不提供目标域名。

## 已核查的 ProxyCommand 能力

2026-11-07 核查 sing-box `testing` 提交 `9f1cd1ef75b7999bb118a9322a003f6edce2752d`，[`tools connect`](https://github.com/SagerNet/sing-box/blob/9f1cd1ef75b7999bb118a9322a003f6edce2752d/cmd/sing-box/cmd_tools_connect.go)支持 stdin/stdout 数据转发，具备直接接入 SSH ProxyCommand 的机制。它直接选择默认或指定 outbound，不能据此认为完整路由规则都被应用。此结论来自源码核查，没有在本项目验证其真实 VPN 连接。
