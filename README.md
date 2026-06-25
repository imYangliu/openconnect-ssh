# OCH

> **English summary:** OCH is a lightweight OpenConnect + SSH helper for
> AnyConnect / OpenConnect networks. It ensures VPN reachability on demand
> before running `ssh`, manages extra macOS routes, and ships a native SwiftUI
> GUI on macOS (passwords stored in Keychain). Targets **macOS (GUI-first)** and
> **WSL (plain-Linux CLI)**. There is no background keepalive daemon —
> reconnection happens on demand when you run `och <host>` or `ssh` through its
> ProxyCommand. See the Chinese user guide at `docs/usage.md`.

OCH 是一个轻量的 OpenConnect + SSH 辅助工具，用来在 AnyConnect / OpenConnect 网络前自动处理 VPN 可达性、macOS 额外路由和 SSH 代理连接。

支持的平台是 **macOS（原生 GUI 为主）+ WSL（当作普通 Linux 的 CLI）**。macOS 提供一个 SwiftUI 原生 GUI，用 Keychain 保存 VPN 密码，并通过托管的 SSH Include 文件让指定 Host 自动走 `och --proxy-command`。

OCH 不维护后台保活守护进程：断线重连是**按需触发**的——执行 `och <host>` 或经其 ProxyCommand 的 `ssh xxx` 时会先探测 VPN，断开则自动尝试重连。开机自启请按需自行配置。

## 用户文档

完整用户用法见 [docs/usage.md](docs/usage.md)，配置文件字段和覆盖规则见 [docs/configuration.md](docs/configuration.md)。

## 功能亮点

- SwiftUI macOS GUI：管理一个 VPN profile、一个托管 SSH Host 和一组额外 CIDR 路由
- CLI 包装器：执行 `ssh` 前先检查 VPN，断开时自动尝试连接（按需，无后台守护进程）
- SSH 自动代理：只管理 `~/.ssh/och.config`，不接管全局 SSH 行为
- macOS 路由调节：默认复用 Homebrew OpenConnect 自带的 `vpnc-script`，可追加 `MACOS_EXTRA_ROUTES`
- 安全存储：GUI 把 VPN 密码保存到 Keychain，不写入 `.env`
- WSL/Linux 兼容：复用同一套 CLI 脚本，按 `ip route` 处理分流

## 仓库结构

```text
och                         CLI 入口
Makefile                    开发、构建、检查入口
Package.swift               SwiftPM 工程
Sources/OCHApp/             macOS SwiftUI GUI
src/                        shell 实现脚本
docs/usage.md               用户使用指南
docs/configuration.md       配置文件说明
examples/                   配置示例
install.sh                  安装脚本
.env.example                单文件配置模板
```

## 依赖

- macOS 或 WSL/Linux
- `bash`
- `ssh`
- `sudo`
- `openconnect`
- GUI（仅 macOS）：Swift 6、SwiftPM、SwiftUI
- macOS：系统自带 `route`、`nc`、Keychain，以及 Homebrew OpenConnect 的 `vpnc-script`
- WSL/Linux 分流模式：`ip`，以及 `vpn-slice` 或 `uvx`

## macOS 快速开始

### 1. 准备配置

GUI 会保存统一配置到 `~/.config/och/config.toml`。至少需要填好 VPN 网关、VPN 用户、托管 SSH Host、目标主机、SSH 用户和端口；字段名见 [docs/configuration.md](docs/configuration.md)。

`.env` 只作为可选覆盖文件使用；GUI 不会读取、写入或清理它。

macOS 默认使用 OpenConnect 自带的 `vpnc-script`，通常不需要 `VPN_ROUTES`。如果只想额外让某些网段走 VPN，配置 `MACOS_EXTRA_ROUTES`：

```bash
MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16"
```

### 2. 运行 GUI

```bash
make build
make run-gui
```

GUI 会读写 `~/.config/och/config.toml`。CLI 脚本也会读取这份 TOML 配置；VPN 密码保存到 macOS Keychain 的 `och` service 下，不会写入配置文件。

### 3. 运行 CLI

```bash
./och
./och --proxy
./och och-target
./och --proxy-command %h %p
```

直接控制 VPN：

```bash
och-vpn connect
och-vpn status
och-vpn verify
och-vpn disconnect
```

### 4. 安装命令

```bash
sudo make install
```

macOS 上如果存在 `/opt/homebrew/bin`，默认安装到 `/opt/homebrew/bin`，实现文件安装到 `/opt/homebrew/libexec/och`。其他系统默认安装到 `/usr/local`。

可以覆盖安装位置：

```bash
PREFIX=/path/to/prefix make install
```

## SSH 自动代理

GUI 只管理 `~/.ssh/och.config`，并检查 `~/.ssh/config` 是否包含：

```sshconfig
Include ~/.ssh/och.config
```

GUI 生成的托管 Host 类似：

```sshconfig
Host och-target
  HostName your-campus-host.example
  User your-ssh-user
  Port 22
  ProxyCommand /opt/homebrew/bin/och --proxy-command %h %p
```

这样只有被 OCH 管理的 Host 会自动确保 VPN 可达；不会影响其他 `ssh xxx`。

## macOS 路由调节

`MACOS_EXTRA_ROUTES` 支持把额外 CIDR 加到 OpenConnect tunnel：

```bash
MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16"
```

连接时，`macos-vpnc-route-wrapper.sh` 会先调用 OpenConnect 自带的 `/opt/homebrew/etc/vpnc/vpnc-script`，再把这些 CIDR 加到 `TUNDEV`。断开时会尝试删除这些额外路由。它不会覆盖或删除服务端下发的路由。

## 配置变量

OCH 的主配置是 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。密码等私密信息建议放在 `.env` / `ENV_FILE` 覆盖文件中，不写入 TOML。

完整字段表、TOML 示例和覆盖规则见 [docs/configuration.md](docs/configuration.md)。

## 开发入口

日常开发和验证都从 `Makefile` 进入：

```bash
make help
make check
make build
make smoke
```

常用目标：

- `make check`：运行全部检查
- `make check-shell`：检查 shell 脚本语法
- `make shellcheck`：有 `shellcheck` 时运行；未安装时跳过
- `make build`：构建 SwiftUI GUI
- `make run-gui`：构建并启动 GUI
- `make smoke`：运行轻量 smoke tests
- `make install`：安装 CLI 和运行时文件
- `make check-portable`：运行不依赖 Swift 的检查（适合 Linux/WSL）
- `make clean`：删除 SwiftPM 构建产物

## WSL / Linux 使用

WSL 当作普通 Linux 使用，复用同一套 CLI 脚本：

```bash
sudo make install        # 默认装到 /usr/local
och-vpn connect          # 起 VPN（也可直接 och <host>，会按需自动连接）
och och-target           # 经 VPN 连接目标
```

分流默认走 `ip route`，需要额外网段时设置 `VPN_ROUTES`（依赖 `vpn-slice` 或 `uvx`）。OCH 不提供后台保活/开机自启；如需开机自启或断线守护，请自行用 systemd / cron / 登录脚本等配置。

## 安全提示

- 不要提交真实的 `VPN_USER`、`VPN_PASSWORD`、主机地址或端口。
- GUI 使用 Keychain 保存 VPN 密码；管理员密码不保存，由 `sudo -A` 触发系统授权。
- `install.sh` 只安装示例配置，不覆盖真实配置文件。

## 许可证

MIT，见 `LICENSE`。
