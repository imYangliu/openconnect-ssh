# OCH 用户使用指南

OCH 是一个 OpenConnect + SSH 辅助工具。默认情况下，它不会常驻后台保活，而是在你运行 `ssh och-target`、GUI 连接或 `och vpn ...` 管理命令时按需检查 VPN；如果 VPN 不可达，就尝试连接。macOS 26+ 签名 App 可以注册本机 privileged XPC helper，让连接和断开由 root 后台服务执行。

本文面向 macOS GUI 和 Debian/Linux CLI。配置字段和严格解析规则见 [配置文件说明](configuration.md)。

## 安装

依赖：

- macOS 26+：`openconnect`、`ssh`、`sudo`、`nc`；GUI 可选使用系统 Keychain。
- Debian/Linux：`openconnect`、`ssh`、`sudo`、`ip`、`nc`。

安装：

```bash
curl -fsSL https://raw.githubusercontent.com/imyangliu/openconnect-ssh/main/install.sh | bash
```

安装脚本会下载 GitHub Release 二进制包，不要求本机安装 Rust/Cargo。首版支持 macOS arm64 和 Linux x86_64；macOS 默认安装到 `/opt/homebrew`，Linux 默认安装到 `/usr/local`。可以用 `PREFIX=/path/to/prefix` 覆盖安装位置。

升级：

```bash
och update
```

macOS GUI App 会作为 `OCHApp-<version>-darwin-arm64.zip` 独立发布；CLI installer 不自动安装 `.app`。完整服务模式要求签名、公证后的 App 放在 `/Applications`。

开发者从源码安装：

```bash
sudo make install
```

## macOS GUI 用法

```bash
make build
make run-gui
```

本地 `make run-gui` 用于调试，未签名时不保证能注册 privileged helper。正式服务模式使用签名包：

```bash
make signed-app SIGN_IDENTITY="Developer ID Application: ..."
```

在 GUI 中填写 VPN、SSH、额外路由、反向代理端口和语言。保存后会写入：

- `~/.config/och/config.toml`：严格 TOML 配置，不含密码。
- `~/.ssh/och.config`：托管 SSH Host。
- Keychain：可选保存 VPN 密码，service 为 `och`。

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

引导会启动 Rust 全屏 TUI，生成 `config.toml`，并保存密码。SSH 是可选项：启用时会生成 `~/.ssh/och.config`；跳过时不会修改旧 SSH 文件。仓库中的 `src/och-setup.sh` 只作为 legacy compatibility 保留，不再是 `och setup` 的默认入口。

- macOS 和 Debian/Linux：`~/.config/och/secrets.env`，权限 `0600`。

完整终端控制台：

```bash
och tui
```

`och tui` 提供 Overview、Connection、SSH、Routes & Proxy、Service、Config 和 Logs 页面。常用按键是 `Up/Down` 切左侧页面、`Left/Right` 或 `Tab` 切字段、`Enter` 执行动作、`Ctrl-S` 保存、`Esc` 退出；SSH Host 导入列表用 `PageUp/PageDown` 选择。

本机健康检查：

```bash
och doctor
```

`och doctor` 不会写文件，只检查依赖工具、`config.toml`、`secrets.env` 权限、托管 SSH 配置和 macOS 服务状态。输出包含 `[PASS]`、`[WARN]`、`[FAIL]`；存在失败项时命令返回非零。

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
mode = "openconnect"
extra = []

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
och doctor
och vpn disconnect
```

## 排障

先运行：

```bash
och doctor
```

常见结果含义：

- `[FAIL] config.toml`：配置文件缺失或 TOML 无法解析，先运行 `och setup` 或在 `och tui` 的 Config 页修复。
- `[FAIL] secrets mode`：运行 `chmod 600 ~/.config/och/secrets.env`。
- `[WARN] managed SSH`：当前是 VPN-only 配置或 SSH 字段不完整；如果你不需要 `ssh och-target`，可以忽略。
- `[WARN] service socket`：CLI-only 或未安装 macOS helper 时常见，仍可用 sudo fallback。

## macOS 服务模式

正式 macOS GUI 通过 `SMAppService` 注册 `io.github.imyangliu.och.helper` LaunchDaemon，并通过 XPC 调用 root helper。安装服务在 GUI 的“服务”页完成；如果状态显示 `requiresApproval`，点击“打开系统设置”到 Login Items 批准。

helper 负责维护：

```text
/var/run/och/openconnect.pid
/var/log/och/openconnect.log
/var/log/och/service.log
```

CLI-only 安装没有 app bundle 内的 helper；服务不可用时会回到原来的 sudo fallback。排错时可以强制禁用 legacy CLI 服务探测：

```bash
OCH_DISABLE_SERVICE=1 och vpn connect
```

卸载服务在 GUI 的“服务”页完成。

通过托管 Host 发起 SSH：

```bash
ssh och-target
```

这里的 `och-target` 来自 `[ssh].host`，实际目标主机来自 `[ssh].target_host`。

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

写入 `~/.ssh/och.config` 前，GUI 和 `och setup` 都会先把候选配置写入临时文件，并运行 `ssh -F <临时文件> -G <host>` 校验。校验失败时不会覆盖现有 SSH 配置，避免无效 Host、端口或命令格式影响用户日常 SSH。

## 路由与代理

macOS 默认使用 OpenConnect 自带的 `vpnc-script`，不额外接管路由。只有 `[routes].mode = "extra"` 且 `[routes].extra` 有 CIDR 时，OCH 才会通过内置 wrapper 把这些 CIDR 加到 OpenConnect tunnel；这不是直连绕过规则。

Debian/Linux 不自动选择第三方分流脚本。需要额外加入 VPN tunnel 的路由时，请使用系统网络策略、服务端下发路由，或在部署层显式配置 OpenConnect 脚本。

`[proxy]` 是可选字段，默认不会写入配置。它保留给 GUI 和旧 shell wrapper 的反向端口映射兼容能力；当前 Rust CLI 主路径是 `ssh och-target`，不会直接提供 `och --proxy` 命令。

启用 Proxy 或手动添加 `[proxy]` 后，旧 wrapper 使用这些字段时，默认映射是：

```text
远端 7890 -> 本地 127.0.0.1:7890
```

端口来自 `config.toml` 的 `[proxy]`；省略该 section 时等同于未启用 Proxy。

## 配置与 Secret

`och` 默认读取 `~/.config/och/config.toml`，也可以用 `OCH_CONFIG_FILE` 指向其他 TOML 文件。

CLI 只接受这些环境变量：

- `OCH_CONFIG_FILE`
- `OCH_SECRETS_FILE`
- `VPN_PASSWORD`
- `SUDO_ASKPASS`（可选；仅 CLI-only/debug fallback 在没有可用 sudo 缓存时使用）
- `OCH_SERVICE_SOCKET`（测试/开发用；覆盖 legacy CLI 服务 socket）
- `OCH_DISABLE_SERVICE=1`（测试/排错用；强制走 sudo fallback）

VPN 密码由 `VPN_PASSWORD`、secret 文件或 macOS Keychain fallback 提供。管理员密码不保存。正式 macOS GUI 通过系统设置批准 XPC helper；CLI-only/debug fallback 如果不想弹出 askpass，可以先在终端运行 `sudo -v` 让 macOS 缓存 sudo 凭据。

普通配置不能用环境变量覆盖。CLI secret 文件只允许：

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

确认 `[routes].mode = "extra"`，且 `[routes].extra` 包含需要加入 VPN tunnel 的 CIDR。Linux 上还要检查 OpenConnect 服务端下发路由，或在系统/部署层显式配置路由策略。

### 连接失败但不知道原因

```bash
och vpn logs
```

默认日志文件是：

```text
/tmp/och-openconnect-$USER.log
```

如果启用了 macOS 服务模式，日志文件是：

```text
/var/log/och/openconnect.log
```

### 想确认目标是否可达

```bash
och vpn verify
```

它会显示默认路由、目标路由，并检查配置的目标主机端口是否可达。
