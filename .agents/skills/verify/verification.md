# 原生版验证记录

2026-11-07 在 macOS arm64 上验证本次 Go 重写。此记录替代旧版外部 OpenConnect 的验证记录，不把旧版测试结果计入原生能力。

| 检查 | 结果 | 范围 |
| --- | --- | --- |
| `make check` | 通过 | 原生构建、vet、Go 测试、aTrust 本地 fork 测试、真实 CLI 旅程及 legacy 隔离脚本检查。 |
| `go test -race ./...` | 通过 | 会话并发、IPC、混合代理、规则选择、用户态 packet 栈。 |
| `go test -race github.com/mythologyli/zju-connect/client/atrust/...` | 通过 | 证书默认校验、无效会话错误、初始化结束后的认证刷新。 |
| `python3 .agents/skills/verify/scripts/verify.py` | 通过 | 从构建到 CLI 数据流、状态、退出及临时配置清理。 |
| Linux amd64 `CGO_ENABLED=0 go build` | 通过 | 交叉编译，不代表 Linux 实机运行。 |
| `bash install.sh --prefix <临时目录>` | 通过 | 仅安装 bin/och，安装产物 help 正确，临时目录已清理。 |
| 独立复核 | 通过 | 已修复半关闭退出阻塞、aTrust 刷新上下文错误和失效会话仍显示 ready 的问题。 |

CLI 旅程启动真实编译后的 daemon，用 ProxyCommand 转发包含二进制字节的 loopback 数据，然后查询状态并停止服务。混合代理测试通过注入的 session 工厂连接真实本地 TCP/HTTP 服务。AnyConnect/aTrust 的用户态 packet 栈通过内存中两个网络栈完成 TCP 往返；自签名 TLS 网关被拒绝，认证数据未发送。

终止隧道显示 failed，后续请求触发五秒冷却后的重建。普通目标端口拒绝不会清除健康 VPN。隧道地址变化使旧网络栈失效；这验证了失效处理逻辑，不代表真实服务端重连已验证。

本轮完整证据位于 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-rt_xrtca`。`report.json` 保存命令与退出码；四组 stdout/stderr 日志在临时 HOME 清理后仍保留。二进制 SHA-256 为 `56ad9d0569291c32417427b89ffcb6936fa040c553350849de056acc10666204`。系统清理临时目录后需重新生成原始证据。

上述隔离测试未使用真实凭据，未修改个人 SSH 配置或系统网络。此时真实校园网关尚未验证；后续授权实测见下节。MFA、校内 DNS、IPv6/aTrust 资源兼容性、其他 TUN 工具共存及实际断线恢复仍需独立验收。通用交互式认证 UI、UDP、Fake IP、空闲会话回收尚未实现。没有提交、发布原生 release 或安装到系统目录。


## YAML 迁移与授权的真实 AnyConnect 测试

2026-11-07 按用户授权读取本机旧配置及私有凭据文件，仅用于用户原有校园 VPN 网关。仓库没有写入网关、账号、密码、目标地址或凭据内容。

原生配置已改为 YAML，默认 config.yaml；拒绝未知字段、重复键、空文档及多个 YAML 文档，解析错误不回显配置值。示例、CLI 测试和发布文件路径同步迁移。make check 通过。

实际使用 YAML 启动仓库编译的原生服务，并对既有 SSH 配置中的目标执行以下检查：

| 路径 | 结果 |
| --- | --- |
| 原生 AnyConnect 认证及会话建立 | 通过，服务状态 ready。 |
| ProxyCommand 固定去向 | 通过，收到 SSH 握手标识。 |
| ProxyCommand 自动策略 | 通过，匹配该目标规则并收到 SSH 握手标识。 |
| 同一服务的 SOCKS5 导出 | 通过，收到同一目标的 SSH 握手标识。 |
| 同一服务的 HTTP CONNECT 导出 | 通过，收到同一目标的 SSH 握手标识。 |
| 清理及系统路由 | 服务停止、socket 和临时配置移除；测试前后 netstat -rn 快照一致。 |

首次使用旧配置中的默认别名时，VPN 已 ready，但 VPN DNS 无法解析该别名；改用原有实际 SSH 目标后通过。以上只验证 VPN 认证、目标 TCP 和 SSH 协议标识，没有执行远端 SSH 用户认证或 shell 命令。没有启动外部 OpenConnect/helper，没有修改个人 SSH 文件。此结果仅代表该 AnyConnect 网关与本次账号认证流程；aTrust、其他认证方式和网关仍未实测。

YAML 版本的完整隔离验证证据位于 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-i7k7o8k8`，四阶段退出码均为 0，清理后证据仍保留。二进制 SHA-256 为 `58bc43d7ac51199b7d9eeb3a16a241f862cfbf851df6853d8eeca2ff8434b992`。独立 YAML 复核通过。

## 用户服务部署与真实 SSH 登录

2026-11-07 按用户授权替换本机旧版 CLI，保留旧二进制与 SSH 配置备份，配置登录时启动的 macOS 用户 LaunchAgent。服务通过私有凭据文件加载密码，plist 不保存密码。已迁移使用同一旧 CLI 的 ProxyCommand 条目。

安装二进制与仓库构建 SHA-256 一致。新服务初始 idle；禁用 SSH 连接复用、使用已有主机密钥校验和密钥认证，对既有校园目标执行仅输出验证标识的命令，退出码 0 且标识匹配。随后会话为 ready，launchd 为 running，没有外部 OpenConnect 进程。原生服务按用户要求保持运行。该结果只覆盖此次用户配置和一个 SSH 目标，不代表 aTrust 或其他认证方式通过。


## 按职责拆包

2026-11-07 将入口移至 cmd/och，实现移至 internal/config、routing、vpn、proxy、daemon 和 cli。原有 24 个回归测试完整保留，CLI、YAML 与 IPC 协议不变。AnyConnect 和 aTrust 暂留同一 vpn 包。

重构前后编译产物通过 tests/equivalence.py 对比，17 项 CLI/IPC/代理观察一致，包括精确二进制数据、固定/自动连接、失败响应及清理。make check、go test -race ./...、项目 verify 技能和 Linux amd64 交叉编译通过。独立源码审查未发现阻断项。没有替换本机已安装二进制、修改个人配置或重启后台服务。

对比命令为 `python3 tests/equivalence.py /path/to/baseline-och build/bin/och`。它只使用临时配置、loopback 服务和缺失凭据的失败路径，不连接真实 VPN。

此次完整验证证据位于 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-8r7aobke`，二进制 SHA-256 为 `eb827ef8071e58e98b61f2abef9b62b40010e4e3fbe34ba3b8289c583da1b739`。


## aTrust 网关实测受阻

2026-11-07 按用户提供的公开校园网关进行验证。TLS 校验开启，`/public/manifest` 与 `/passport/v1/public/authConfig` 返回成功。可用认证方式为 CAS 统一身份认证及 aTrust 扫码，不提供当前配置可直接使用的密码认证入口。

使用原生 OCH 的临时 YAML 配置选择 `auth_type: auth/cas` 和服务端公布的登录域。认证流程停在 `unsupported external login challenge: cas`，会话为 failed。未提交账号密码，未取得登录会话，也未验证真实资源连接或数据通道。临时服务与文件已清理，现有 AnyConnect 服务未改动。

结论是该网关的协议发现成功，但当前 OCH 缺少 CAS 外部登录交互，aTrust 端到端仍未通过。需要实现外部登录与回调交接后重新验证，不能通过改成密码认证或关闭证书校验解决。


## CAS 浏览器交接实现与实测边界

2026-11-07 新增独立 `och login --via NAME [--browser PATH]`，经私有 Unix socket 将交互认证接入后台共享会话。Chrome/Chromium/Edge 使用临时配置目录，在 CAS 回调发送前中止请求，收到中止确认后交给原生后端。回调 origin、路径、登录域与 ticket 唯一性均校验，诊断只输出白名单类别/数字码。OCH 不持久化 ticket/VPN cookie，临时浏览器目录结束时清理。

隔离及竞态测试覆盖回调截获确认、取消、临时目录清理、无并发重复初始化、成功会话不随登录命令退出关闭、失败错误不泄露 URL/包装内容，以及错误证书仍被拒绝。修复了上游 portal 重定向将隐式/显式 HTTPS 443 判为不同 origin 的兼容问题。增加可选 `tunnel_server_name`，只作用于 aTrust 数据通道，保留证书链校验，不改变 HTTPS 认证身份。

用户完成了实际校园 CAS 页面登录，OCH 捕获回调后继续初始化，但返回证书校验失败。显式通道名称的再次测试仍失败，因此不能将原因仅归为主机名不匹配，也不能宣称 aTrust 原生会话或资源访问通过。没有关闭证书校验，没有提交密码到命令参数，没有修改或重启已有 AnyConnect 服务。三次临时服务与浏览器配置均已清理。此前“缺少 CAS 外部交互”的代码缺口已补，当前真实网关受阻于后续证书校验，需要继续确认服务端通道证书的可信链与用途。

最终 CAS 版隔离验证证据位于 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-iujn_5cy`，所有阶段退出码为 0。修正了隔离测试临时路径过长超过 macOS Unix socket 限制的问题。二进制 SHA-256 为 `e3cb73b1f0b6b5080fa64abca4df3b1a2df92594c73330e7a5cacbf199c8d2b7`。Linux amd64 交叉编译通过。


## aTrust 证书根因与端到端通过

2026-11-07 后续诊断区分了网页与数据通道。公开 HTTPS 的证书链正常；数据通道使用私有、自签名服务端证书，macOS 标准验证拒绝它。网关公布多个节点，两个可达节点使用不同证书，原先未验证证书的延迟探测可能选择不在信任范围内的节点。

在用户明确确认首次指纹后，仅为该 OCH aTrust 配置启用精确证书文件。没有安装官方客户端，没有加入系统钥匙串，没有关闭主机名、有效期或服务端用途验证。节点探测改用与正式隧道相同的 TLS 校验，因此未获信任的节点不能参与选择，也没有自动信任其他证书。

实际验证通过 CAS 回调、原生会话 ready、ProxyCommand 收到 SSH 标识、SOCKS5、HTTP CONNECT，以及禁止 OpenSSH 连接复用的真实 SSH 登录与远端固定标识。aTrust 运行在独立的用户后台服务，未改写现有 SSH 配置。

用户授权保留专用登录状态，新增 --remember 保存私有 OCH 浏览器配置。目录按 VPN 配置、网关和登录域隔离，带私有权限与独占锁；不使用日常浏览器配置。学校 SSO 有效时可复用，但不能保证状态永不过期，OCH 仍不持久化后台 VPN cookie。

证书与节点诊断只保存在用户的私有 OCH 目录，仓库不包含账号、cookie、ticket、实际节点地址或用户信任文件。官方分发包只做静态检查，未安装或执行。

本轮最终隔离验证证据为 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-pkfnsnf_`，make check 全阶段通过。固定证书/节点选择/浏览器持久化的独立竞态复核通过。aTrust 用户服务保持 ready；检查时旧 AnyConnect 会话已失效，经现有凭据重新连接后恢复 ready，既有 SSH 配置未改动。


## 当前节点证书集合

2026-11-07 用户明确允许信任当前节点的证书集合。记录的四个通道地址对应两张不同证书；两地址可直接访问，另外两地址经已有 VPN 代理可读取公开证书。仅收集当前这些地址，没有自动信任未来证书。

`tunnel_certificate_file` 现支持有限 PEM 证书集合，去重且拒绝非证书/畸形内容；隧道必须精确匹配集合中的叶证书，同时保留名称、时间与用途检查。测试覆盖两个批准证书均可用，以及未列入集合的证书仍被拒绝。make check 和相关竞态测试通过。

用户私有配置已启用两证书集合，单独重启 aTrust 服务后重新认证成功；禁用 OpenSSH 连接复用的真实 SSH 登录再次通过。AnyConnect 的配置与 SSH 默认去向未修改。首次使用仍需要手动 YAML、认证参数和证书设置；--remember 是浏览器状态保存，不是后台 VPN 认证状态的跨进程持久化。


## 首次向导、自动发现与认证状态持久化

2026-11-07 新增 setup、auth-info、用户服务安装、密码/CAS 登录恢复、单配置 logout，以及原生认证缓存。缓存为当前用户私有目录0700、文件0600的权限保护，并非加密；绑定规范化网关、协议、认证域、账号及认证组，使用独占锁和原子替换。不保存账号密码或一次性 CAS 回调。

自动化验证覆盖 aTrust 与 AnyConnect 从磁盘状态在独立 OS 子进程中恢复，未提供密码或浏览器且没有额外认证请求。另覆盖过期状态、临时网络失败、跨网关/身份错配、文件权限与符号链接、刷新/退出并发、配置并发修改、SSH保留、服务定义与启动，以及真实 PTY 密码不回显/Unicode退格/取消恢复。

实际校园验证先完成一次 aTrust 登录并写入私有状态，然后停止后台，确认新 PID 的进程仅靠缓存完成目标连接及新的 SSH 用户认证，没有运行 login 或浏览器。再次运行完整 setup 流程时，将浏览器可执行路径设为不存在以排除交互回退，仍成功保存配置、启动后台并确认认证恢复。最后恢复已有 launchd 用户服务，它再次从缓存连通目标，状态 ready。首次向导尝试曾因用户未及时完成网页登录超时退出，未覆盖配置；之后的认证与恢复验证已完成。

AnyConnect 的真实校园缓存恢复尚未单独实测，其自动化测试使用真实本地 HTTPS/XML/CSTP fixture及新进程；aTrust 除隔离测试外有上述真实校园证据。Linux 用户服务安装未做实机验收。服务器令牌过期时仍需重新登录，不能保证永久免认证。当前默认 SSH/AnyConnect配置未修改。

最终完整验证证据位于 `/var/folders/qp/_k1cf56107g30ymkbp55_wy00000gn/T/och-evidence-o7ukeim_`，make check 全阶段通过。二进制 SHA-256 为 `bb5117de52e77c0ba31ce5ef358ff665ced46537ac23373b162f516e2304d44f`。独立竞态复核与 Linux amd64 交叉编译通过。
