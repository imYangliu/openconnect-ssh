# OCH

> **English summary:** OCH is a lightweight OpenConnect + SSH helper for
> AnyConnect / OpenConnect networks. It ensures VPN reachability on demand
> when `ssh` reaches its ProxyCommand, manages macOS extra routes from strict TOML config, and
> ships a native SwiftUI GUI on macOS 26+. Targets **macOS (GUI-first)** and
> **Debian/Linux CLI**. The signed macOS app can register a privileged XPC
> helper so VPN connect/disconnect no longer prompts for sudo every time.

OCH 是一个轻量的 OpenConnect + SSH 辅助工具，用来在 AnyConnect / OpenConnect 网络前自动处理 VPN 可达性、macOS 额外路由和 SSH 代理连接。

支持平台是 **macOS 26+（原生 GUI 为主）+ Debian/Linux（CLI）**。默认断线重连是按需触发的；签名的 macOS App 可以注册本机 privileged XPC helper，让连接和断开由 root 后台服务执行。

## 用户文档

完整用户用法见 [docs/usage.md](docs/usage.md)，配置字段和严格解析规则见 [docs/configuration.md](docs/configuration.md)。

## 功能亮点

- SwiftUI macOS GUI：管理一个 VPN profile、一个托管 SSH Host 和一组额外 CIDR 路由。
- Rust 单二进制 CLI：提供 Ratatui/Crossterm `och tui` 控制台、`och setup` 向导、`och doctor` 健康检查、`och proxy-command` SSH ProxyCommand 后端，以及 `och vpn ...` 管理命令。
- macOS 26+ 服务模式：签名 App 通过 `SMAppService` 注册 root XPC helper，GUI 的 connect/disconnect/status/logs 通过 helper 执行。
- SSH 自动代理：只管理 `~/.ssh/och.config`，写入前先用 OpenSSH 校验，不接管全局 SSH 行为。
- 严格配置：`config.toml` 是唯一非敏感配置来源，未知字段直接报错。
- Secret 分离：CLI 默认使用 `~/.config/och/secrets.env` 且必须 `0600`；macOS GUI 可选 Keychain 保存。
- 固定运行布局：GUI 使用 bundle 内 helper；CLI 使用安装时确定的 libexec helper。

## 仓库结构

```text
och                         开发入口包装器；安装后由 Rust 单二进制接管
Makefile                    开发、构建、检查入口
Package.swift               SwiftPM 工程
Sources/OCHApp/             macOS SwiftUI GUI
Sources/OCHPrivilegedHelper/ macOS root XPC helper
Sources/OCHXPCClient/       GUI 到 helper 的 XPC client
rust-cli/                   Rust 单二进制 CLI
src/                        legacy shell setup、内部 VPN helper 和 macOS route wrapper
docs/usage.md               用户使用指南
docs/configuration.md       配置文件说明
examples/                   配置示例
install.sh                  安装脚本
```

## 依赖

- macOS 26+ 或 Debian/Linux
- `bash`
- `ssh`
- `sudo`
- `openconnect`
- Rust/Cargo（仅开发者从源码构建需要）
- GUI 开发（仅 macOS）：Swift 6.2+、SwiftPM、SwiftUI、macOS 26 SDK
- macOS：系统自带 `route`、`nc`、Keychain，以及 Homebrew OpenConnect 的 `vpnc-script`
- Debian/Linux：`ip`

## 快速开始

安装：

```bash
curl -fsSL https://raw.githubusercontent.com/imyangliu/openconnect-ssh/main/install.sh | bash
```

安装脚本会识别系统并下载 GitHub Release 二进制。首版支持 macOS arm64 和 Linux x86_64；macOS 默认安装到 `/opt/homebrew`，Linux 默认安装到 `/usr/local`。macOS GUI App 会作为独立 release 包发布，CLI installer 不自动安装 `.app`。

升级：

```bash
och update
```

配置：

```bash
och setup
```

`och setup` 现在默认启动 Rust 全屏 TUI 向导，VPN 必填，SSH 可以跳过；跳过 SSH 时不会修改旧的 `~/.ssh/och.config`。`src/och-setup.sh` 仅保留为 legacy compatibility，不再作为新安装的 setup 主路径。

完整终端控制台：

```bash
och tui
```

`och tui` 覆盖 Overview、Connection、SSH、Routes & Proxy、Service、Config 和 Logs，适合在终端里完成 GUI 的常用配置和操作。

健康检查：

```bash
och doctor
```

`och doctor` 只读检查本机依赖、配置、secret 权限、SSH Include 和服务状态；发现失败项时返回非零，便于排障或 CI 调用。

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
och doctor
och vpn disconnect
ssh och-target
```

macOS GUI 服务模式：

```bash
make signed-app SIGN_IDENTITY="Developer ID Application: ..."
```

将签名并公证后的 `OCH.app` 放入 `/Applications`，在 GUI 中点击“安装服务”。OCH 会通过 `SMAppService` 注册 `io.github.imyangliu.och.helper` LaunchDaemon，并通过 XPC 调用 root helper；如果系统设置显示需要批准，按 GUI 提示打开 Login Items 批准。

CLI-only 安装没有 app bundle 内的 helper；服务不可用时仍会回到 sudo fallback。排错时可以用 `OCH_DISABLE_SERVICE=1` 强制禁用旧 CLI 服务探测。

## 配置

非敏感配置只读 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。普通配置不能用环境变量覆盖。

允许的 CLI 环境变量：

- `OCH_CONFIG_FILE`
- `OCH_SECRETS_FILE`
- `VPN_PASSWORD`
- `SUDO_ASKPASS`（可选；仅 CLI-only/debug fallback 在没有可用 sudo 缓存时使用）
- `OCH_SERVICE_SOCKET`（测试/开发用；覆盖 legacy CLI 服务 socket）
- `OCH_DISABLE_SERVICE=1`（测试/排错用；强制走 sudo fallback）

VPN 密码由 `VPN_PASSWORD`、secret 文件或 macOS Keychain fallback 提供。管理员密码不保存；正式 macOS GUI 通过系统设置批准 XPC helper。CLI-only/debug fallback 如果不想弹出 askpass，可以先在终端运行 `sudo -v` 让 macOS 缓存 sudo 凭据。

CLI secret 文件（macOS 和 Linux/Debian 默认相同）：

```bash
mkdir -p ~/.config/och
printf 'VPN_PASSWORD="%s"\n' 'your-vpn-password' > ~/.config/och/secrets.env
chmod 600 ~/.config/och/secrets.env
```

## 路由

macOS 默认保持 OpenConnect 原生路由行为。只有 `[routes].mode = "extra"` 且 `[routes].extra` 非空时，OCH 才会通过内置 wrapper 把这些 CIDR 额外加入 OpenConnect tunnel；这不是直连绕过规则。

OpenConnect 下发的 DNS 默认会交给 vpnc-script 处理。macOS 上可设置 `[dns].mode = "ignore"`，让 OCH wrapper 忽略下发 DNS 并保留现有系统 DNS。

Debian/Linux 不自动选择第三方分流脚本。需要额外加入 VPN tunnel 的路由时，请使用系统网络策略、服务端下发路由，或在部署层显式配置 OpenConnect 脚本。

## 开发入口

```bash
make help
make check
make build
make smoke
sudo make install
```

常用目标：

- `make check`：运行全部检查
- `make check-portable`：运行不依赖 Swift 的检查
- `make check-shell`：检查 shell 脚本语法
- `make shellcheck`：有 `shellcheck` 时运行；未安装时跳过
- `make build`：构建 SwiftUI GUI
- `make run-gui`：构建并启动 GUI
- `make smoke`：运行轻量 smoke tests
- `make install`：从本地源码构建并安装 CLI 和运行时文件
- `make clean`：删除 SwiftPM 构建产物

Release CLI 包命名约定：

- `och-cli-<version>-darwin-arm64.tar.gz`
- `och-cli-<version>-linux-x86_64.tar.gz`

macOS GUI App 包命名约定：

- `OCHApp-<version>-darwin-arm64.zip`

## 安全提示

- 不要提交真实的 VPN 用户名、VPN 密码、主机地址或端口。
- CLI secret 文件只允许保存 `VPN_PASSWORD`，且权限必须是 `0600`。macOS GUI 只有启用保存时才写入 Keychain。
- 管理员密码不保存。正式 macOS 服务模式通过 `SMAppService` + XPC helper 授权；CLI-only/debug fallback 只有 sudo 没有可用缓存才使用 `SUDO_ASKPASS`。

## 许可证

MIT，见 `LICENSE`。
