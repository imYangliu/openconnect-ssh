# OCH 用户使用指南

OCH 是一个 OpenConnect + SSH 辅助工具。它不会常驻后台保活，而是在你运行 `och <host>`、`ssh och-target` 或 GUI 连接时检查 VPN；如果 VPN 不可达，就按需尝试连接。

本文面向 macOS GUI 和 Debian/Linux CLI。配置字段和严格解析规则见 [配置文件说明](configuration.md)。

## 安装

依赖：

- macOS：`openconnect`、`ssh`、`sudo`、`nc`、系统 Keychain；构建 GUI 需要 Swift 6 / SwiftPM。
- Debian/Linux：`openconnect`、`ssh`、`sudo`、`ip`。

安装：

```bash
sudo make install
```

macOS Apple Silicon 默认安装到 `/opt/homebrew`，其他系统默认安装到 `/usr/local`。可以用 `PREFIX=/path/to/prefix sudo make install` 覆盖安装位置。

## macOS GUI 用法

```bash
make build
make run-gui
```

在 GUI 中填写 VPN、SSH、额外路由、反向代理端口和语言。保存后会写入：

- `~/.config/och/config.toml`：严格 TOML 配置，不含密码。
- `~/.ssh/och.config`：托管 SSH Host。
- Keychain：VPN 密码，service 为 `och`。

如果关键配置缺失，GUI 会打开首次引导。选择已有 SSH Host 时，OCH 默认生成 `och-<原 Host>`，路由 CIDR 默认是目标 IPv4 的 `/32`。

点击“安装 Include”后，GUI 会确保 `~/.ssh/config` 包含：

```sshconfig
Include ~/.ssh/och.config
```

之后可以直接运行：

```bash
ssh och-target
```

GUI 使用 app bundle 内置 helper；不会读取或猜测配置里的 helper 路径。

## CLI-only 用法

推荐先运行引导：

```bash
och setup
```

引导会生成 `config.toml`、`~/.ssh/och.config`，并保存密码：

- macOS：Keychain。
- Debian/Linux：`~/.config/och/secrets.env`，权限 `0600`。

最小配置示例：

```toml
[vpn]
host = "vpn.example.com"
user = "your_vpn_username"
auth_group = ""

[ssh]
host = "och-target"
target_host = "your-campus-host.example"
user = "your-ssh-user"
port = "22"

[routes]
extra = []

[proxy]
local_host = "127.0.0.1"
local_port = "7890"
remote_port = "7890"

[paths]
# Runtime helper paths are fixed by the installed app or CLI layout.

[app]
language = "system"
```

常用命令：

```bash
och vpn connect
och vpn status
och vpn verify
och vpn logs
och vpn disconnect
```

通过 OCH 发起 SSH：

```bash
och och-target
och --proxy och-target
och --proxy -N och-target
och
och --proxy
```

未传目标主机时，`och` 使用 `[ssh].host`。

## SSH 自动代理

GUI 或 `och setup` 生成的 `~/.ssh/och.config` 类似：

```sshconfig
Host och-target
  HostName your-campus-host.example
  User your-ssh-user
  Port 22
  ProxyCommand /opt/homebrew/bin/och proxy-command %h %p
  ServerAliveInterval 30
  ServerAliveCountMax 3
```

这只影响被 OCH 管理的 Host，不会接管其他 SSH 配置。`ProxyCommand` 会先确保 VPN 可达，再把 stdio 连接到目标 `host:port`。

## 路由与代理

macOS 默认使用 OpenConnect 自带的 `vpnc-script`。如果 `[routes].extra` 有 CIDR，OCH 会通过内置 wrapper 把它们加到 OpenConnect tunnel。

Debian/Linux 不自动选择第三方分流脚本。需要额外路由时，请使用系统网络策略、服务端下发路由，或在部署层显式配置 OpenConnect 脚本。

`och --proxy` 会追加 SSH 反向端口映射，默认是：

```text
远端 7890 -> 本地 127.0.0.1:7890
```

端口来自 `config.toml` 的 `[proxy]`。

## 配置与 Secret

`och` 默认读取 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。

CLI 只接受这些环境变量：

- `OCH_CONFIG_FILE`
- `OCH_SECRETS_FILE`
- `VPN_PASSWORD`
- `SUDO_ASKPASS`

普通配置不能用环境变量覆盖。Linux secret 文件只允许：

```bash
VPN_PASSWORD="your-vpn-password"
```

并且必须：

```bash
chmod 600 ~/.config/och/secrets.env
```

## 常见问题

### 提示缺少 `[vpn].host` 或 `[vpn].user`

检查 `OCH_CONFIG_FILE` 指向的 TOML，并确保 `[vpn]` 中填写了 `host` 和 `user`。

### `ssh och-target` 没有走 OCH

确认 `~/.ssh/config` 包含：

```sshconfig
Include ~/.ssh/och.config
```

再检查生成的 `~/.ssh/och.config` 里是否有 `ProxyCommand ... och proxy-command %h %p`。

### Linux 上额外路由没有生效

OCH 不再自动选择分流脚本。检查 OpenConnect 服务端下发路由，或在系统/部署层显式配置路由策略。

### 连接失败但不知道原因

```bash
och vpn logs
```

默认日志文件是：

```text
/tmp/och-openconnect-$USER.log
```

### 想确认目标是否可达

```bash
och vpn verify
```

它会显示默认路由、目标路由，并检查配置的目标主机端口是否可达。
