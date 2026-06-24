# OCH

轻量、可配置的 OpenConnect + SSH helper，用于在 AnyConnect / OpenConnect 网络前自动处理 VPN 可达性、macOS 额外路由和 SSH 代理连接。

Lightweight OpenConnect + SSH tooling for AnyConnect-compatible networks, with a macOS SwiftUI GUI, SSH integration, optional route tuning, and reverse port forwarding.

## Highlights

- Native SwiftUI macOS GUI for one VPN profile and one managed SSH host
- Auto-check VPN reachability before running `ssh`
- Use OpenConnect's default `vpnc-script` on macOS, with optional extra CIDR routes
- Generate a managed `~/.ssh/och.config` for `ssh <host>` integration
- Recover connectivity by restarting a systemd service or calling the VPN script directly
- Optionally expose a local proxy or service to the remote machine with `ssh -R`
- Store environment-specific values outside the repository

## 仓库结构

```text
och
Package.swift
Sources/OCHApp/
src/
  och.sh
  och-vpn.sh
  och-openconnect-keepalive.sh
  macos-vpnc-route-wrapper.sh
  och-sudo-askpass.sh
examples/
  och-vpn.env.example
  och.env.example
  ssh_config.example
.env.example
systemd/
  och-openconnect.service
  och-openconnect-keepalive.service
  och-openconnect-keepalive.timer
install.sh
```

## 依赖

- macOS 或 Linux
- `bash`
- `ssh`
- `sudo`
- `openconnect`
- GUI: Swift 6 / SwiftPM / SwiftUI
- macOS: system `route`, `nc`, Keychain, and Homebrew OpenConnect's `vpnc-script`
- Linux split-tunnel mode: `ip` plus `vpn-slice` or `uvx`
- Optional Linux keepalive: `systemd`

## 快速开始

### 1. 准备 `.env`

```bash
cp .env.example .env
```

然后编辑 `.env`，至少填好：

- `VPN_HOST`
- `VPN_USER`
- `TARGET_HOST`
- `TARGET_PORT`
- `TARGET_SSH_USER`
- `DEFAULT_HOST`

macOS 默认走 OpenConnect 自带的 `vpnc-script`，通常不需要 `VPN_ROUTES`。需要额外走 VPN 的网段可填 `MACOS_EXTRA_ROUTES`。

也可以继续使用拆分配置：

```bash
mkdir -p ~/.config/och
cp examples/och-vpn.env.example ~/.config/och/och-vpn.env
cp examples/och.env.example ~/.config/och/och.env
```

### 2. 运行 CLI

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

### 3. 运行 GUI

```bash
swift build
.build/debug/OCHApp
```

GUI 会读写 `~/.config/och/gui.env`。VPN 密码保存到 macOS Keychain 的 `och` service 下，不写入 `.env`。

### 4. 安装命令

```bash
sudo ./install.sh
```

macOS 上如果存在 `/opt/homebrew/bin`，默认安装到 `/opt/homebrew/bin`，实现文件安装到 `/opt/homebrew/libexec/och`。其他系统默认安装到 `/usr/local`。可以用 `PREFIX=/path/to/prefix ./install.sh` 覆盖。

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

这样只有被 OCH 管理的 Host 会自动 ensure VPN；不会接管所有 `ssh xxx`。

## macOS 路由调节

`MACOS_EXTRA_ROUTES` 支持追加 CIDR 到 OpenConnect tunnel：

```bash
MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16"
```

连接时 `macos-vpnc-route-wrapper.sh` 会先调用 OpenConnect 自带 `/opt/homebrew/etc/vpnc/vpnc-script`，再把这些 CIDR 加到 `TUNDEV`。断开时会尝试删除这些额外路由。不会覆盖或删除服务端下发路由。

## 配置变量

### `och-vpn`

- `VPN_HOST`: VPN 网关地址，必须配置
- `VPN_USER`: VPN 用户名，必须配置
- `VPN_AUTHGROUP`: 可选认证组
- `TARGET_HOST`: 目标主机；`verify` 和 `ssh` 需要
- `TARGET_PORT`: 目标端口，默认 `22`
- `TARGET_SSH_USER`: SSH 用户，默认当前系统用户
- `MACOS_EXTRA_ROUTES`: macOS 上额外加入 VPN tunnel 的 CIDR 列表
- `VPN_ROUTES`: Linux 默认分流模式使用的 CIDR 列表
- `VPN_SCRIPT_CMD`: 可选；覆盖 OpenConnect 使用的 vpnc-script 命令
- `VPN_PASSWORD`: 可选；CLI 场景可用，GUI 不写入该值
- `ENV_FILE`: 可选；显式指定统一 `.env` 文件
- `CONFIG_FILE`: 默认 `~/.config/och/och-vpn.env`

### `och`

- `WRAPPER_CONFIG_FILE`: 默认 `~/.config/och/och.env`
- `CONNECT_SCRIPT`: 默认优先从 `PATH` 查找 `och-vpn`
- `DEFAULT_HOST`: 默认 SSH host；未设置时必须显式传目标
- `PROXY_LOCAL_HOST`: 反向映射到的本地地址，默认 `127.0.0.1`
- `PROXY_LOCAL_PORT`: 本地端口，默认 `7890`
- `PROXY_REMOTE_PORT`: 远端端口，默认 `7890`

## systemd 示例

仓库内提供 Linux systemd 示例：

- `systemd/och-openconnect.service`
- `systemd/och-openconnect-keepalive.service`
- `systemd/och-openconnect-keepalive.timer`

默认假设配置文件位于：

```text
/etc/och/och-vpn.env
```

启用示例：

```bash
sudo INSTALL_SYSTEMD=1 ./install.sh
sudo systemctl daemon-reload
sudo systemctl enable --now och-openconnect-keepalive.timer
```

## 安全提示

- 不要提交真实的 `VPN_USER`、`VPN_PASSWORD`、主机地址或端口。
- GUI 使用 Keychain 保存 VPN 密码；管理员密码不保存，由 `sudo -A` 触发系统授权。
- `install.sh` 只安装示例配置，不覆盖真实配置文件。

## 开发与校验

```bash
bash -n och
bash -n src/och.sh
bash -n src/och-vpn.sh
bash -n src/och-openconnect-keepalive.sh
bash -n src/macos-vpnc-route-wrapper.sh
bash -n src/och-sudo-askpass.sh
swift build
```

如果系统装了 `shellcheck`：

```bash
shellcheck och src/och.sh src/och-vpn.sh src/och-openconnect-keepalive.sh src/macos-vpnc-route-wrapper.sh src/och-sudo-askpass.sh install.sh
```

## License

MIT，见 `LICENSE`。
