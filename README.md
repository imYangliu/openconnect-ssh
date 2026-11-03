# OCH

OCH 让你用普通的 SSH 命令访问 VPN 内的主机。OpenSSH 调用 OCH 连接目标端口，必要时启动 OpenConnect，再把连接交给 SSH。支持 macOS 和 Debian/Linux。

[下载 Go 命令行版（macOS / Linux）](https://github.com/imYangliu/openconnect-ssh/releases/tag/v0.1.0-alpha.2)

## 快速开始

从当前源码构建并安装（需要 Go 1.24+），并确保系统有 OpenConnect、OpenSSH 和 sudo。

```bash
sudo make install
och setup
```

在终端填写 VPN 网关、用户和密码。需要 SSH 时输入目标地址或已有 Host 别名，保存后运行向导打印的命令，例如 `ssh och-target`。只使用 VPN 时跳过 SSH，运行 `och vpn connect`。使用 sudo 模式时，先在终端运行 `sudo -v` 完成授权。

## 常用操作

| 目的 | 命令 |
| --- | --- |
| 检查本机配置和依赖 | `och doctor` |
| 查看 VPN 进程 | `och vpn status` |
| 测试目标端口 | `och vpn verify` |
| 查看连接日志 | `och vpn logs` |
| 断开 VPN | `och vpn disconnect` |

安装、使用和排障见[使用指南](docs/usage.md)。路由、DNS 和凭据见[配置说明](docs/configuration.md)。开发时运行 `make build` 和 `make check`。
