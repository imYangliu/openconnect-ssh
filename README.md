# OCH

> **English summary:** OCH is a lightweight OpenConnect + SSH helper for
> AnyConnect / OpenConnect networks. It ensures VPN reachability on demand
> when `ssh` reaches its ProxyCommand, manages macOS extra routes from strict TOML config, and
> ships a native SwiftUI GUI on macOS. Targets **macOS (GUI-first)** and
> **Debian/Linux CLI**. There is no background keepalive daemon.

OCH 是一个轻量的 OpenConnect + SSH 辅助工具，用来在 AnyConnect / OpenConnect 网络前自动处理 VPN 可达性、macOS 额外路由和 SSH 代理连接。

支持平台是 **macOS（原生 GUI 为主）+ Debian/Linux（CLI）**。OCH 不维护后台保活守护进程：断线重连是按需触发的。

## 用户文档

完整用户用法见 [docs/usage.md](docs/usage.md)，配置字段和严格解析规则见 [docs/configuration.md](docs/configuration.md)。

## 功能亮点

- SwiftUI macOS GUI：管理一个 VPN profile、一个托管 SSH Host 和一组额外 CIDR 路由。
- Rust 单二进制 CLI：提供 `och proxy-command` 作为 SSH ProxyCommand 后端，以及 `och vpn ...` 管理命令。
- SSH 自动代理：只管理 `~/.ssh/och.config`，不接管全局 SSH 行为。
- 严格配置：`config.toml` 是唯一非敏感配置来源，未知字段直接报错。
- Secret 分离：macOS 使用 Keychain；Linux 使用 `~/.config/och/secrets.env` 且必须 `0600`。
- 固定运行布局：GUI 使用 bundle 内 helper；CLI 使用安装时确定的 libexec helper。

## 仓库结构

```text
och                         开发入口包装器；安装后由 Rust 单二进制接管
Makefile                    开发、构建、检查入口
Package.swift               SwiftPM 工程
Sources/OCHApp/             macOS SwiftUI GUI
rust-cli/                   Rust 单二进制 CLI
src/                        setup、内部 VPN helper 和 macOS route wrapper
docs/usage.md               用户使用指南
docs/configuration.md       配置文件说明
examples/                   配置示例
install.sh                  安装脚本
```

## 依赖

- macOS 或 Debian/Linux
- `bash`
- `ssh`
- `sudo`
- `openconnect`
- Rust/Cargo（安装和构建 Rust CLI）
- GUI（仅 macOS）：Swift 6、SwiftPM、SwiftUI
- macOS：系统自带 `route`、`nc`、Keychain，以及 Homebrew OpenConnect 的 `vpnc-script`
- Debian/Linux：`ip`

## 快速开始

安装：

```bash
sudo make install
```

macOS 上如果存在 `/opt/homebrew/bin`，默认安装到 `/opt/homebrew/bin`，实现文件安装到 `/opt/homebrew/libexec/och`。其他系统默认安装到 `/usr/local`。

配置：

```bash
och setup
```

macOS GUI：

```bash
make build
make run-gui
```

常用命令：

```bash
och vpn connect
och vpn status
och vpn verify
och vpn disconnect
ssh och-target
```

## 配置

非敏感配置只读 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。普通配置不能用环境变量覆盖。

允许的 CLI 环境变量：

- `OCH_CONFIG_FILE`
- `OCH_SECRETS_FILE`
- `VPN_PASSWORD`
- `SUDO_ASKPASS`

Linux secret 文件：

```bash
mkdir -p ~/.config/och
printf 'VPN_PASSWORD="%s"\n' 'your-vpn-password' > ~/.config/och/secrets.env
chmod 600 ~/.config/och/secrets.env
```

## 路由

macOS 会消费 `[routes].extra`，通过内置 wrapper 把额外 CIDR 加到 OpenConnect tunnel。

Debian/Linux 不自动选择第三方分流脚本。需要额外路由时，请使用系统网络策略、服务端下发路由，或在部署层显式配置 OpenConnect 脚本。

## 开发入口

```bash
make help
make check
make build
make smoke
```

常用目标：

- `make check`：运行全部检查
- `make check-portable`：运行不依赖 Swift 的检查
- `make check-shell`：检查 shell 脚本语法
- `make shellcheck`：有 `shellcheck` 时运行；未安装时跳过
- `make build`：构建 SwiftUI GUI
- `make run-gui`：构建并启动 GUI
- `make smoke`：运行轻量 smoke tests
- `make install`：安装 CLI 和运行时文件
- `make clean`：删除 SwiftPM 构建产物

## 安全提示

- 不要提交真实的 VPN 用户名、VPN 密码、主机地址或端口。
- GUI 使用 Keychain 保存 VPN 密码；管理员密码不保存，由 `sudo -A` 触发系统授权。
- Linux secret 文件只允许保存 `VPN_PASSWORD`，且权限必须是 `0600`。

## 许可证

MIT，见 `LICENSE`。
