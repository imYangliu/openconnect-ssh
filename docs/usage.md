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

保存带 YAML 合并键、锚点或别名的配置时，向导会展开别名，保留有效配置和标量文本，避免修改共享锚点时影响其他 VPN。递归别名、过深或展开规模过大的配置会被拒绝，原文件保持不变。新增配置的默认代理端口会避开已保存的监听，即使后台尚未启动。

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

## 原生 SSH 正向与反向端口转发

在 YAML 的 `ssh` 下定义 SSH 连接，在 `forwards` 下定义可复用的转发规则。完整示例见[配置参考](configuration.md#ssh-与-forwards)。`--ssh compute` 引用 OCH 的 `ssh.compute`，不读取 `~/.ssh/config` 中的 Host 别名，也不执行其中的 ProxyCommand 或 ProxyJump。原有 `ssh <别名>` 工作流仍通过 OpenSSH 运行。

运行已保存的一个或多个规则：

```bash
och forward jupyter callback
```

该命令在前台运行，同一命令中使用同一个 SSH 配置的规则复用一条 SSH 会话。`och serve` 不会自动启动保存的转发规则。按 Ctrl-C 关闭该命令的监听、转发连接和 SSH 会话，共享 VPN 后台继续运行。`och status` 仍报告后台的 VPN 与混合代理状态；原生转发的就绪、错误和重连状态显示在 `och forward` 的 stderr。

也可以直接指定本次监听和目标：

```bash
# 本机 8888 → SSH 机器的 localhost:8888。
och forward --ssh compute 8888 8888

# SSH 机器的 9000 → 运行 OCH 的机器的 localhost:3000。
och forward --reverse --ssh compute 9000 3000

# 本机 15432 → 直接经 VPN 连接内网数据库，无需 SSH。
och forward --via campus 15432 database.internal.example:5432
```

正向 SSH 转发的目标由 SSH 服务器连接，目标域名也由服务器解析。反向转发的目标由运行 OCH 的机器直连，不使用 SSH 配置中的 VPN 去向。SSH 连接本身的 VPN 路径由 `ssh.compute.via` 指定。一次性命令的 `--ssh` 和 `--via` 二选一，`--reverse` 必须配合 `--ssh`。参数须放在规则名或地址之前。

监听和目标地址均可只写十进制端口，默认补为 `127.0.0.1:端口`。例如 `8888` 等同于 `127.0.0.1:8888`，正向目标仍在 SSH 服务器上，反向目标仍在 OCH 本机。也可显式使用 `127.0.0.1:端口` 或 `[::1]:端口`；监听仅允许回环 IP，不接受空主机形式 `:端口`。监听端口 `0` 会自动分配端口，实际地址出现在 `ready` 输出中。反向转发需要 SSH 服务端允许远程端口转发；OCH 不会在远端安装软件或启动 shell。

SSH 使用 `known_hosts_file` 校验主机身份。首次使用前，将已独立核验的 SSH 主机公钥加入该文件。未知或变更的主机密钥会失败，OCH 不会自动接受或改写 known_hosts。认证支持显式 `identity_file`、`SSH_AUTH_SOCK` agent 或 `password_env`；加密私钥通过 `key_passphrase_env` 读取口令。不交互询问密码，也不自动搜索默认私钥。

开始时认证、主机密钥或监听失败会退出，并关闭已创建的监听。`ready` 表示监听已建立，不代表目标应用可达。后续单个目标连接失败不销毁健康 SSH 会话。SSH 会话中断后，新正向连接可重建 SSH；反向监听会重连并重新申请远端端口。已有应用连接不会恢复或重放。连接、握手和 SSH 通道操作有 30 秒超时，SSH 保活间隔为 30 秒；反向重连失败后间隔一秒重试。

## 5. 使用 SOCKS5 或 HTTP 代理

上述混合端口 `127.0.0.1:1080` 同时提供 SOCKS5、HTTP CONNECT 和普通 HTTP 代理，固定通过 `campus`：

```bash
curl --proxy socks5h://127.0.0.1:1080 https://service.internal.example/
curl --proxy http://127.0.0.1:1080 https://service.internal.example/
```

`socks5h` 将域名交给代理端处理。实际端口以自己的 YAML 为准。端口不会套用 SSH 的自动规则，也不会自动成为系统全局代理。将它配置为 sing-box 等工具的代理出口时，由上游负责分流。

监听仅允许回环 IP，暂不提供代理密码认证，同机其他进程也能连接。不支持 Fake IP 映射；上游应传入域名或真实目标 IP。

SOCKS5 同时支持 TCP CONNECT 和 UDP ASSOCIATE。使用支持 SOCKS5 UDP 的客户端连接同一个混合端口，无需新增 YAML 字段。HTTP 代理仍只承载 TCP；上面的 curl 命令不验证 UDP。

UDP 客户端须保持 SOCKS5 TCP 控制连接，并向 ASSOCIATE 响应中的回环 UDP 地址发送 SOCKS5 数据报。一个关联可以访问多个目标，目标域名由指定 VPN 解析。OCH 固定客户端来源 IP 和端口，不支持 SOCKS5 分片，丢弃 `FRAG != 0` 的数据报。每个关联最多保留 64 个目标，空闲目标在 60 秒后回收；每个后台最多接受 128 个 UDP 关联。关闭控制连接、注销对应 VPN 或停止后台会关闭该关联。

AnyConnect 通过用户态网络栈承载 UDP。aTrust 的 UDP 使用 IPv4 L3 通道，目标须匹配网关发布的 UDP 或 `all` IP 资源；仅发布为 TCP 应用的资源不能用于 UDP。无权限、解析失败或目标不响应时，客户端通常表现为 UDP 超时，不会回退直连。

## 通过指定 VPN ping 目标

```bash
och ping --via campus 10.0.0.1
och ping --via ecnu --count 8 --interval 1s --timeout 3s host.internal.example
```

替换示例目标为该 VPN 允许访问的 IP 或域名。参数须放在目标之前。命令通过运行中后台的共享 VPN 会话发送真实 ICMP Echo，不调用系统 ping，也不创建系统 TUN 或修改路由。

默认尝试 4 次，探测开始间隔至少 1 秒，每次超时 2 秒。`--count` 范围为 1–65535，`--interval` 为 10ms–60s，`--timeout` 须大于 0 且不超过 60s。一次探测超时包含会话等待、VPN DNS 和回包等待；实际 RTT 只计算 Echo 发出至收到匹配回包的时间。初始化较慢时可增大 `--timeout`，或先运行 `och login --via NAME`。

`--via` 必须指定已配置的 VPN，不接受 `direct` 或自动策略。域名优先使用 aTrust 发布的静态 DNS 资源，否则使用隧道 DNS，不使用系统 DNS。每次探测可重新解析域名；每条成功结果显示该次实际访问的 IP。AnyConnect 可以使用隧道已分配的 IPv4 或 IPv6 地址族。aTrust 当前只支持 IPv4，且目标须匹配 ICMP 或 `all` IP 资源。

按 Ctrl-C 结束并查看已完成部分的统计。统计中的尝试次数包括初始化或解析失败的尝试，不等同于实际发出的 ICMP 包数。收到至少一个有效回包时退出码为 0，无回包时为非零。超时只说明未收到匹配回包，不能据此判断 VPN 认证过期。此命令不配置周期保活。

更新 OCH 后，需使用新二进制重启后台，才能使用新增 ping IPC 和 SOCKS5 UDP 功能。重启会中断既有连接。

## 通过指定 VPN 检查 TCP 端口

```bash
och tcping --via ecnu host.internal.example 22
och tcping --via campus --count 8 --interval 1s --timeout 3s 10.0.0.1 443
```

目标可以是 IP 或域名，端口须为 1–65535 的十进制数字。参数须放在目标之前。`--via` 必须指定已配置的 VPN，不接受 `direct` 或自动策略。每次尝试通过后台的共享 VPN 建立一次 TCP 连接，随后立即关闭，不向目标发送应用数据。

默认次数、间隔、超时范围与 `och ping` 相同。超时覆盖等待 VPN、DNS 和建连。输出的后端拨号耗时从会话准备结束后开始计算，包含 VPN 内 DNS、TCP 建连和可能的中转握手；它不是纯 SYN 往返时间。并发重建会话时，拨号还可能等待新会话就绪。显示的地址是请求的目标，避免把 aTrust 中转节点误当成实际目标。

按 Ctrl-C 查看部分统计。至少一次连接成功时退出码为 0，全部失败时为非零。统计记录探测尝试与成功连接次数。端口可连接不代表应用健康，也不能证明 UDP 或 ICMP 被允许。

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

## 使用 doctor 定位连接问题

只验证配置并读取后台状态：

```bash
och doctor
```

此命令不会建立 VPN 会话、加载 SSH 私钥、连接目标或探测监听端口。`idle`、`ready` 等状态是后台快照；`ready` 不表示某个目标端口可达。

指定目标或转发规则后，会执行主动检查：

```bash
och doctor --via campus server.internal.example 22
och doctor --policy campus-auto server.internal.example 22
och doctor --ssh compute
och doctor --ssh compute 8888
och doctor --forward jupyter
och doctor --forward callback
och doctor --json --timeout 10s --forward callback
```

`--via`、`--policy`、`--ssh`、`--forward` 四选一。`--via` 和 `--policy` 后的目标使用 `HOST PORT`；`--ssh` 可省略目标，也可使用 `PORT` 或 `HOST:PORT`。SSH 名称来自 OCH YAML。参数须放在目标之前。

固定 VPN 或策略检查使用运行中后台的真实路由逻辑，返回所选 VPN、实际拨号地址、命中规则和 DNS 方式。域名交给 VPN 后端时只报告委托解析，不把它当成独立 DNS 成功检查。VPN 初始化和目标 TCP 使用不同检查项，目标拒绝连接不会被报告成 VPN 登录失败。纯直连检查和仅使用 `direct` 的原生 SSH 不要求后台运行。

原生 SSH 检查会验证 known_hosts 和用户认证；指定目标时再检查 SSH 服务器能否连接该端口。转发规则检查还会临时申请并释放本地或远端监听，反向目标由 OCH 本机直连。端口检查只是当前可用性的快照，不会为下一次转发预留端口。已经运行的转发也会占用自己的监听端口，doctor 不识别或停止端口的所属进程。SSH 远端拒绝监听可能来自端口占用或服务端权限，不能仅凭该错误判定具体原因。

网络检查总预算默认 10 秒，可用 `--timeout` 调整；后台单次探测最多 60 秒。主动检查可能初始化所选 VPN，但不会启动或修复后台、打开认证浏览器或修改系统路由。`login_required` 会给出重新登录提示。旧版后台不支持诊断协议时，需要使用当前构建重启后台。

默认输出分阶段文本。`--json` 输出一个带 `version`、`ok`、`source` 和 `checks` 的 JSON 对象；路由信息在 `route`，后台快照在 `profiles` 与 `listeners`。检查失败时进程返回状态码 1，配置文件无效时也会保留 JSON 输出。检查成功只证明已执行的层次；TCP 建连成功不代表 HTTP、数据库或应用认证已通过。

### 端口占用时的行为

固定本地端口被占用时，`och forward` 会报告 `address already in use` 并以状态码 1 退出。OCH 不会终止原监听进程，也不会自动增加端口号；只有显式使用监听端口 `0` 才会请求自动分配。多个规则一起启动时，任一初始监听失败会回滚本次已创建的监听。SSH 反向监听被拒绝同样会启动失败。已经运行的反向转发在 SSH 断线后会重连并重新申请监听，重绑失败会报告错误并按现有间隔重试。

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
