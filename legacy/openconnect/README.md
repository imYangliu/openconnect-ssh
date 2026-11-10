# 外部 OpenConnect 备用方案

此目录保存原 OCH v0.1.0-alpha.2 的系统路由方案，独立于新的原生会话服务。旧文档和安装器仅用于旧版本，不适用于新的原生 YAML 或 CLI。现行命令见[原生使用指南](../../docs/usage.md)。

## 只将指定网段导入 VPN

自行安装 OpenConnect 和 vpn-slice，然后执行：

```bash
sudo bash legacy/openconnect/connect-split.sh https://vpn.example.com your_user 10.20.0.0/16
```

密码由 OpenConnect 交互读取，不写入脚本。该命令实际修改系统路由；Ctrl-C 正常断开后检查路由恢复。系统网段路由会影响所有应用，不只影响 SSH。域名、DNS、IPv6 和其他 TUN 工具需要独立验收。

原来的 macOS wrapper 在 `src/`，只追加网段，不阻止基础 vpnc-script 修改默认路由。`install-v0.sh` 仍是旧版安装器，会安装外部依赖，不要用于原生重写版本。

参考 [OpenConnect 的脚本说明](https://www.infradead.org/openconnect/vpnc-script.html)与 [vpn-slice](https://github.com/dlenski/vpn-slice)。本目录脚本未连接真实 VPN 验证。
