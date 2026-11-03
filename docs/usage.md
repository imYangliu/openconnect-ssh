# 使用 OCH

从源码使用 Go 1.24 或更高版本构建。运行 `make build`，再运行 `sudo make install` 安装到 macOS 的 `/opt/homebrew` 或 Linux 的 `/usr/local`。安装到用户目录可用 `make install PREFIX="$HOME/.local"`。发布包使用项目的 install.sh，预编译版本支持 macOS arm64 和 Linux x86_64。

## 第一次配置

运行 `och setup`，填写 VPN 网关、账号和密码。密码保存到权限为 0600 的 secrets.env。需要 SSH 时填写目标地址或已有的 SSH 别名。向导调用 OpenSSH 解析已有别名的主机、用户、端口，为新别名写入 ProxyCommand，并在主 SSH 配置开头引入托管文件。输入 `-` 可仅配置 VPN。额外路由和 DNS 通过 [config.toml](configuration.md) 设置。

## 日常连接

用向导打印的命令，例如 `ssh och-target`。OCH 直接尝试目标 TCP 端口，成功后把这条连接交给 OpenSSH。失败时检查本用户的 VPN 进程，必要时通过 sudo 启动 OpenConnect，再等待目标恢复。SSH 认证仍由 OpenSSH 处理。新建或断开 VPN 需要管理员权限，可先在终端运行 `sudo -v`。也可以配置自己的 SUDO_ASKPASS。

仅使用 VPN 时可运行 `och vpn connect` 和 `och vpn disconnect`。VPN 在 SSH 会话结束后仍可复用。

## 排障

- `och doctor` 检查本机依赖、基本配置、secret 文件权限和 SSH 别名的 ProxyCommand。
- `och vpn status` 查看 OCH 管理的本机 OpenConnect 进程。
- `och vpn verify` 单独测试配置的目标端口。端口失败可能来自路由、远端主机或 SSH 服务。
- `och vpn logs` 显示最近 80 行 OpenConnect 日志，默认位于 `~/.config/och/run/openconnect.log`。
