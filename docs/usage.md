# 使用指南

本文适用于原生 OCH。示例中的地址、用户名、网段和别名都是占位值，请替换为自己的信息。v0 用户先阅读本文末尾的迁移说明。

## 1. 安装 CLI

在仓库根目录执行：

```bash
bash install.sh --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
och --help
```

需要 Go 1.25.6 或更新版本及 make。安装器只构建并安装 `bin/och`，不安装外部 OpenConnect。后台服务和 SSH 配置由后续 `och setup` 按用户选择完成。将 PATH 设置加入自己的 shell 启动文件，供新终端使用。

如果只想在源码目录运行，执行 `make build`，再用 `./build/bin/och` 替代本文中的 `och`。已有其他版本时，用 `command -v och` 确认实际路径。更新二进制后，已经运行的后台进程仍是旧版本；需要通过对应服务管理器重启后才能使用新增命令。

## 2. 首次配置向导

```bash
och setup
```

也可以直接提供网关：

```bash
och setup --server https://vpn.example.com
```

向导依次完成：

1. 发现协议和公开认证方式。aTrust 可显示认证类型与登录域；只有一种支持的方式时自动选择。AnyConnect 识别基本认证表单及认证组，不承诺自动处理所有 SAML/MFA 流程。
2. 选择配置名与回环混合代理端口。默认配置名为 `campus`。
3. 完成认证。密码在终端隐藏输入，CAS 在专用浏览器中完成。不会把密码写进配置或认证缓存。
4. 对系统不信任的私有通道证书显示指纹，要求明确确认。仅在 OCH 中固定批准的证书，不修改系统信任。认证状态先保存，因此拒绝证书或后续连接失败不必重新做一次 CAS。
5. 保存 YAML，启动服务，并检查新后台进程是否成功恢复认证状态。仅监听就绪不会被报告为 VPN 已就绪。
6. 可选择注册 macOS launchd/Linux systemd 用户服务，并添加 SSH 别名。SSH 写入使用独立文件，保留原配置；已存在的别名不会被覆盖。

默认 YAML 是 `~/.config/och/config.yaml`。认证状态和专用浏览器状态默认位于用户私有的 `~/.config/och/auth/` 与 `~/.config/och/browser/`，即使 `--config` 指向源码仓库，也不会把默认认证缓存写进仓库。

服务已运行时，向导拒绝修改它的配置，避免中断现有连接。确实要修改时，先停止对应服务，再运行向导；替换已有配置需要确认。若用户服务定义已存在，向导不会覆盖它，可在保存后通过原来的服务管理器重启。

## 3. 日常启动、恢复和退出登录

向导完成后，后台服务会按配置提供 SSH 与混合代理。选择用户服务安装后，登录操作系统时会自动启动。没有安装用户服务时，可以手动运行：

```bash
och serve
```

后台对首次使用的 VPN 优先读取已保存的认证状态。有效状态可直接恢复，不需要密码或浏览器。服务端会决定状态何时过期，不能保证永久免登录。

需要重新认证时：

```bash
och login --via campus --remember
```

密码认证会在需要时隐藏读取密码。CAS 使用专用 Chrome/Chromium/Edge；`--remember` 保留学校的浏览器 SSO 状态，SSO 有效时可以减少重复输入。指定浏览器可加 `--browser /absolute/path/to/browser`。学校页面中的验证码和 MFA 由用户完成。

清除某个 VPN 的本地认证状态并断开它的连接：

```bash
och logout --via campus
```

`logout` 不停止其他 VPN 配置，也不会退出其他网站或清除浏览器 SSO。它清除的是 OCH 的 VPN 认证缓存；后续连接需要重新认证。浏览器状态保存在独立私有目录，是否再次要求输入账号由学校 SSO 状态决定。

配置文件不是默认路径时，在这些命令中使用相同的 `--config /absolute/path/to/config.yaml`。

### 认证状态如何保存

缓存保存会话 cookie、设备标识等可复用认证数据，不保存账号密码或一次性 CAS 回调链接。文件权限为 `0600`，父目录为当前用户私有的 `0700`，并绑定网关、协议、认证域、账号/认证组配置。采用独占锁与原子写入，避免多个进程或旧刷新任务覆盖状态。

这是文件权限保护，不是加密保险箱。不要共享或提交认证缓存。损坏、归属不匹配或权限过宽的缓存会被拒绝；临时网络失败不会直接删除有效状态。

### 手动配置与脚本化使用

向导使用默认 socket，并自动安排认证缓存和已确认的私有证书的位置。手动编写 YAML 时可以省略 `socket`。需要保存认证状态时，设置 `auth_state_file: auto`。完整字段见[配置参考](configuration.md)，示例见 [examples/config.yaml](../examples/config.yaml)。

确认私有证书后，如果它们只有一个共同的非通配 DNS 名称，向导会自动使用该名称。只有无法唯一确定时，才需要填写数据通道的 TLS 名称。

有多个 VPN 时，把它们放在同一份 YAML 的 `profiles` 下。运行一个 OCH 后台，在各 SSH 别名中用 `--via` 指定对应配置。每个混合代理端口也通过 `via` 选择配置。

只查看公开认证方式，不登录：

```bash
och auth-info --server https://vpn.example.com
```

只认证并保存配置，不启动新服务：

```bash
och setup --server https://vpn.example.com --no-start
```

写未认证草稿供检查时，必须同时使用 `--skip-login --no-start`，不会把草稿报告为已连接。非交互模式为 `--non-interactive`，所需选择需用参数提供；它不能自动批准新的私有证书。

默认向导记住专用浏览器状态，使用 `--no-remember` 可以关闭这一行为。它与原生 VPN 认证缓存是两份独立状态。

## 4. 配置 SSH

将下面的 Host 块加入自己的 `~/.ssh/config`，替换二进制绝对路径、目标和 SSH 用户名。VPN 用户与 SSH 用户可以不同。

```sshconfig
Host campus-server
  HostName server.internal.example
  User your_ssh_user
  ProxyCommand /absolute/path/to/och proxy-command --via campus %h %p
```

连接时执行：

```bash
ssh campus-server
```

`--via campus` 固定通过校园 VPN，目标域名由该后端处理。SSH 主机密钥验证和用户认证仍由 OpenSSH 完成。SSH 退出后，后台保留 VPN 会话供后续连接复用。

需要按规则选择路径时，将 ProxyCommand 改为：

```sshconfig
ProxyCommand /absolute/path/to/och proxy-command --policy campus-auto %h %p
```

规则按配置顺序匹配，未命中直连。需要 CIDR 匹配的域名会使用系统 DNS 解析；校内专有域名应优先使用域名规则或固定去向。`--via` 与 `--policy` 必须二选一。自定义配置路径时，在目标参数 `%h %p` 之前添加 `--config /absolute/path/to/config.yaml`。

可用 `ssh -G campus-server` 检查生效的 ProxyCommand。OpenSSH 通常采用首先取得的配置值；已有重复 Host 块或 Include 时，仅在文件末尾追加设置可能不会生效。

## 5. 使用 SOCKS5 或 HTTP 代理

上述混合端口 `127.0.0.1:1080` 同时提供 SOCKS5、HTTP CONNECT 和普通 HTTP 代理，固定通过 `campus`：

```bash
curl --proxy socks5h://127.0.0.1:1080 https://service.internal.example/
curl --proxy http://127.0.0.1:1080 https://service.internal.example/
```

`socks5h` 将域名交给代理端处理。实际端口以自己的 YAML 为准。端口不会套用 SSH 的自动规则，也不会自动成为系统全局代理。将它配置为 sing-box 等工具的代理出口时，由上游负责分流。

监听仅允许回环 IP，暂不提供代理密码认证，同机其他进程也能连接。首版不支持 SOCKS5 UDP 或 Fake IP 映射；上游应传入域名或真实目标 IP。

## 管理服务

```bash
och status
och stop
```

`status` 返回 JSON，包含监听地址和配置状态：

| 状态 | 含义 |
| --- | --- |
| `idle` | 尚未建立该 VPN 会话。 |
| `connecting` | 正在初始化。 |
| `ready` | 会话已初始化且未报告终止错误，不保证所有目标都可达。 |
| `failed` | 初始化或会话失败，后续请求在冷却期结束后可重新初始化。 |

`stop` 关闭本服务的监听、连接和 VPN 会话。前台运行时也可按 Ctrl-C。关闭服务会中断所有共享连接，不能恢复已经断开的 SSH 会话。

### 已由 macOS launchd 托管

`och setup` 可按选择注册 launchd；也可以运行 `och service install --config /absolute/path/to/config.yaml`。已配置 LaunchAgent 时，无需在每次 SSH 前手动运行 `serve`。登录后由 launchd 启动服务，凭据需要由该服务的启动器提供，不能依赖终端的临时 export。

使用自己的服务 Label 替换下方 `your.och.service`，查看或重启服务：

```bash
launchctl print "gui/$(id -u)/your.och.service"
launchctl kickstart -k "gui/$(id -u)/your.och.service"
```

重启会中断已有连接。日志路径由 plist 的 `StandardOutPath` 和 `StandardErrorPath` 指定。是否在 `och stop` 后再次启动，取决于 plist 的 KeepAlive 策略。本文不把个人机器上的 Label、凭据或 SSH 别名作为所有用户的默认值。

Linux 使用 systemd 用户服务。向导或 `och service install` 可写入并启用用户服务，不使用 sudo；需要可用的 systemd 用户会话。无法启用时，配置和认证状态仍保留，可以手动运行 `och serve`。

## 故障排查

| 现象 | 检查方法 |
| --- | --- |
| 提示未知命令或缺少 `--via/--policy` | 用 `command -v och`、`och --help` 检查版本；旧 ProxyCommand 格式需迁移。 |
| 提示 service unavailable | 确认后台服务运行，且客户端与服务使用同一个配置及 socket。 |
| 提示 socket 目录权限错误 | 运行目录应仅当前用户可访问，通常为 `0700`。不要为修复问题扩大权限。 |
| 提示密码环境变量为空 | 检查运行服务的进程环境，而不是当前 SSH 终端；变量名必须与 YAML 相同。 |
| VPN ready，但目标不可达 | 分别检查目标 IP、端口、域名解析和服务端资源授权。ready 不是目标连通性检查。 |
| 证书校验失败 | 检查网关地址、系统时间和证书信任；不要直接将 insecure 当作常规修复。 |
| 自动规则选择不符合预期 | 检查规则顺序、域名后缀边界、系统解析结果及目标 CIDR。 |
| 出现认证挑战错误 | 当前没有通用 MFA/浏览器登录向导；该认证流程可能未支持。 |

查看本机日志时避免公开账号、cookie 或网关信息。已选定的 VPN 连接失败会返回错误，不静默改成直连。

## 从 v0 迁移

原生版不读取旧 TOML，不提供原来的 `vpn connect` 或 `update` 命令。新的 `setup` 使用本指南的 YAML 与原生认证流程。迁移时备份旧二进制与 SSH 配置，再创建 YAML、启动服务并更新 ProxyCommand。原有密码文件不会被自动读取；需要由启动器安全加载到 YAML 指定的环境变量。

旧版外部 OpenConnect 的安装器、网段脚本和文档保留在 [legacy/openconnect](../legacy/openconnect/README.md)，不与原生服务同时配置同一套网络路径。

## 开发验证

在仓库根目录运行 `make check`。它使用临时配置和本地测试服务，不需要真实凭据，不修改个人 SSH 配置或系统网络。已验证范围见 [Feature Map](../.agents/skills/verify/features/README.md)及[验证记录](../.agents/skills/verify/verification.md)。
