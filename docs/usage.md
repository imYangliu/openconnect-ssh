# OCH 用户使用指南

OCH 是一个 OpenConnect + SSH 辅助工具。它不会常驻后台保活，而是在你运行 `och <host>`、`ssh och-target` 或 GUI 连接时检查 VPN；如果 VPN 不可达，就按需尝试连接。

本文面向最终用户，默认以 macOS GUI 为主。WSL/Linux 用户可以直接看“CLI-only 用法”和“WSL/Linux 注意事项”。配置文件字段、读取顺序和覆盖规则见 [配置文件说明](configuration.md)。

## 安装

先准备依赖：

- macOS：`openconnect`、`ssh`、`sudo`、`nc`、系统 Keychain；构建 GUI 需要 Swift 6 / SwiftPM。
- WSL/Linux：`openconnect`、`ssh`、`sudo`、`ip`；分流通常还需要 `vpn-slice` 或 `uvx`。

从源码安装命令：

```bash
sudo make install
```

macOS Apple Silicon 默认安装到 `/opt/homebrew`，其他系统默认安装到 `/usr/local`。可以覆盖安装位置：

```bash
PREFIX=/path/to/prefix sudo make install
```

## macOS GUI 用法

构建并启动 GUI：

```bash
make build
make run-gui
```

在 GUI 中填写：

- VPN：网关、用户、认证组和 VPN 密码。
- SSH：托管 Host 名称、真实 HostName、SSH 用户和端口。
- 路由与代理：额外 VPN CIDR 路由，以及 `och --proxy` 使用的反向端口映射。
- 高级：`och`、`och-vpn` 和 `askpass` 路径。

点击“保存”后，GUI 会写入：

- `~/.config/och/config.toml`：GUI 主配置。
- `~/.ssh/och.config`：托管 SSH Host。
- Keychain：VPN 密码，不写入配置文件。

`och` 和 `och-vpn` 也会读取这份 TOML 配置，所以 GUI 保存后可以直接配合 CLI 使用。

点击“安装 Include”后，GUI 会确保 `~/.ssh/config` 包含：

```sshconfig
Include ~/.ssh/och.config
```

之后可以直接运行：

```bash
ssh och-target
```

也可以在 GUI 中点击“连接”“断开”“状态”来管理 VPN。

## CLI-only 用法

如果不使用 GUI，也使用同一份 TOML 配置。

创建 `~/.config/och/config.toml`：

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
```

完整字段说明见 [配置文件说明](configuration.md)。CLI 密码建议放在当前目录 `.env` 或 `ENV_FILE=/path/to/private.env` 指向的私有覆盖文件里，不写入 `config.toml`。

常用命令：

```bash
och-vpn connect
och-vpn status
och-vpn verify
och-vpn logs
och-vpn disconnect
```

通过 OCH 发起 SSH：

```bash
och och-target
och --proxy och-target
och --proxy -N och-target
```

如果设置了 `DEFAULT_HOST`，也可以直接运行：

```bash
och
och --proxy
```

## SSH 自动代理

GUI 生成的 `~/.ssh/och.config` 类似：

```sshconfig
Host och-target
  HostName your-campus-host.example
  User your-ssh-user
  Port 22
  ProxyCommand /opt/homebrew/bin/och --proxy-command %h %p
  ServerAliveInterval 30
  ServerAliveCountMax 3
```

这只影响被 OCH 管理的 Host，不会接管其他 SSH 配置。`ProxyCommand` 会先确保 VPN 可达，再把 stdio 连接到目标 `host:port`。

## 路由与代理

macOS 默认使用 OpenConnect 自带的 `vpnc-script`，通常不需要配置 `VPN_ROUTES`。如果只想额外让某些网段走 VPN，配置：

```bash
MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16"
```

GUI 中的“额外路由”会写入同等含义的配置。

`och --proxy` 会追加 SSH 反向端口映射，默认是：

```text
远端 7890 -> 本地 127.0.0.1:7890
```

可以用这些变量调整：

```bash
PROXY_LOCAL_HOST=127.0.0.1
PROXY_LOCAL_PORT=7890
PROXY_REMOTE_PORT=7890
```

## 配置读取顺序

`och` 和 `och-vpn` 都默认读取 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。随后会按 `ENV_FILE`、当前目录 `.env`、`PROJECT_ENV_FILE` 的顺序读取覆盖配置。

详细规则和字段表见 [配置文件说明](configuration.md)。

## WSL/Linux 注意事项

WSL/Linux 不使用 macOS GUI 和 Keychain，按 CLI-only 方式配置即可：

```bash
sudo make install
och-vpn connect
och och-target
```

Linux 分流通常需要设置 `VPN_ROUTES`：

```bash
VPN_ROUTES="10.0.0.0/8 192.168.0.0/16"
```

如果你提供了自定义脚本，也可以设置：

```bash
VPN_SCRIPT_CMD="/usr/local/bin/vpn-slice 10.0.0.0/8"
```

## 常见问题

### 提示“未设置 VPN_HOST”或“未设置 VPN_USER”

检查 `~/.config/och/config.toml` 或当前目录 `.env` 是否填写了 VPN 网关和用户名。

### `ssh och-target` 没有走 OCH

确认 `~/.ssh/config` 包含：

```sshconfig
Include ~/.ssh/och.config
```

再检查生成的 `~/.ssh/och.config` 里是否有 `ProxyCommand ... och --proxy-command %h %p`。

### Linux 上提示需要 `VPN_ROUTES`

在 `~/.config/och/config.toml` 的 `[routes].extra` 中填写网段，或在 `.env` / `ENV_FILE` 中设置 `VPN_ROUTES`。需要自定义脚本时设置 `VPN_SCRIPT_CMD`。

### macOS 上目标网段没有走 VPN

优先检查 VPN 服务端是否下发了路由。如果只是需要额外网段，填写 `MACOS_EXTRA_ROUTES`，然后重新连接。

### 连接失败但不知道原因

查看最近日志：

```bash
och-vpn logs
```

默认日志文件是：

```text
/tmp/och-openconnect-$USER.log
```

### 想确认目标是否可达

运行：

```bash
och-vpn verify
```

它会显示默认路由、目标路由，并检查 `TARGET_HOST:TARGET_PORT` 是否可达。
