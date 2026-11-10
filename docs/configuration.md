# 原生 OCH 配置

命令通过 `--config FILE` 选择 YAML，默认 `~/.config/och/config.yaml`。此格式不兼容旧版外部 OpenConnect CLI。未知字段、重复键、空文档和多个 YAML 文档均会报错。解析错误不回显配置值。完整示例见 [examples/config.yaml](../examples/config.yaml)。修改后台使用的 VPN、代理监听或分流配置后，重启后台服务。修改 `ssh` 或 `forwards` 后，重新启动对应的 `och forward` 命令。

## socket

顶层 `socket` 指定 Unix socket 的绝对路径。省略时使用配置目录下的 `run/och.sock`。路径需足够短以满足 Unix socket 限制；配置目录与运行目录只供当前用户使用。所有客户端必须指向同一个配置文件和 socket。

## profiles

每个 `profiles.NAME` 是一个独立 VPN 配置。同一份 YAML 可以包含 AnyConnect 和 aTrust 配置。一个后台按名称管理各自的会话。名称 `direct` 保留给直连。

| 字段 | 含义 |
| --- | --- |
| `protocol` | `anyconnect` 或 `atrust`。 |
| `server` | VPN 网关 HTTPS 地址。 |
| `username` | VPN 用户名。 |
| `auth_state_file` | 向导设置为 `auto`。OCH 按配置身份在 `~/.config/och/auth/` 中选择文件。也可指定绝对路径。省略或留空时不保存原生认证状态。aTrust 保存 cookie 和设备状态，AnyConnect 保存可复用 cookie。 |
| `password_env` | 后台服务从此环境变量读取密码，不是密码本身。 |
| `cookie_env` | AnyConnect 已认证 cookie 的环境变量名，可用于服务端需要独立交互认证的情况。 |
| `auth_group` | AnyConnect 认证组。 |
| `login_domain` | aTrust 登录域。 |
| `auth_type` | aTrust 认证类型。密码认证使用 `auth/psw`；CAS 使用 `auth/cas`，首次运行 `och login --via NAME` 或使用 `och setup`，之后优先恢复有效缓存。 |
| `tunnel_server_name` | 可选的 aTrust 数据通道 TLS 名称。已确认的证书只有一个共同的非通配 DNS 名称时，OCH 从证书文件推导。无法唯一推导时，私有证书配置需要显式名称。此字段仅用于数据通道。 |
| `tunnel_certificate_file` | 可选的 aTrust 服务端证书文件。向导获得确认后保存证书并设置为 `auto`。OCH 从该配置在 `~/.config/och/auth/` 下的默认位置读取证书，也可指定绝对路径。要求 `insecure: false`。文件仅包含已确认的 PEM 证书。OCH 核对证书内容、名称、有效期和用途。文件缺失或无效时拒绝连接。 |
| `insecure` | 默认 false，校验证书。仅在明确需要的测试环境中设置 true。 |

原生协议后端不会调用外部 OpenConnect。后端支持范围以实际实现和测试为准，认证方式、服务端资源发布方式和域名解析能力不能仅凭协议名称推断。CAS 可通过独立浏览器登录交接到后台；其他 MFA、扫码及浏览器认证方式不保证支持。aTrust 需要正确的登录域，未配置域与服务端不匹配时会明确失败。

有效认证缓存可在没有密码或 cookie 环境变量时恢复会话。没有可用缓存时，密码认证需要非空的 `password_env`，或在前台运行 `och login` 输入密码；AnyConnect 也可使用非空的 `cookie_env` 代替密码。设置了 `cookie_env` 却既没有环境 cookie，也没有可恢复的缓存 cookie 时会报错。使用 cookie 时可以省略 `password_env`。原生 SSH 的凭据环境变量要求见下文 `ssh` 字段说明。

## listeners

每个 `listeners.NAME` 导出一个 SOCKS5/HTTP 混合端口。

```yaml
listeners:
  campus:
    listen: "127.0.0.1:1080"
    via: campus
```

`listen` 必须是回环 IP 与数值端口，端口范围为 0–65535；0 自动分配端口，实际地址由 `och status` 报告。`via` 必须引用 VPN 配置。所有请求通过该配置，不应用自动分流，不在失败后回退直连。监听不是系统代理设置；只有主动使用该端口的应用受影响。

同一端口支持 SOCKS5 TCP CONNECT、SOCKS5 UDP ASSOCIATE、HTTP CONNECT 和普通 HTTP。UDP 无需额外配置，由 SOCKS5 协商动态分配回环 UDP 端口；TCP 控制连接必须保持打开。目标域名交给所选 VPN 处理，不使用系统 DNS。HTTP 不承载 UDP，也不支持 SOCKS5 分片或 Fake IP 映射。

UDP 关联固定客户端来源，最多保留 64 个目标，空闲目标在 60 秒后回收。后台最多允许 128 个 UDP 关联。aTrust 的 UDP 和 ICMP 需要网关发布对应的 IPv4 IP 资源；TCP 应用资源不代表有 UDP 或 ICMP 权限。`och ping` 直接使用 `profiles.NAME`，不需要配置 listener，命令和超时选项见[使用指南](usage.md#通过指定-vpn-ping-目标)。

`och tcping --via NAME HOST PORT` 同样直接使用 VPN profile，无需新增配置。它检查一次 TCP 建连后立即关闭，不依赖 listener 或 SSH 配置。详细命令与计时范围见[TCP 端口探测](usage.md#通过指定-vpn-检查-tcp-端口)。

## policies

每个 `policies.NAME` 提供有序规则，供 `proxy-command --policy NAME` 使用。

```yaml
policies:
  campus-auto:
    rules:
      - domain_suffix: internal.example
        via: campus
      - cidr: 10.20.0.0/16
        via: campus
```

每条规则必须且只能设置一种匹配条件：`domain` 精确域名，`domain_suffix` 域名及其子域，或 `cidr` 网段。`via` 为 VPN 名称或 `direct`。规则按书写顺序匹配，未命中直接连接；不支持任意 Mihomo 规则语法。

域名匹配忽略大小写与尾部点，并检查域名标签边界。需要 CIDR 匹配时使用系统 DNS，实际连接已选定的 IP，不重新解析。多地址处理以首个命中的规则和该规则命中的地址为准。内网域名应在 CIDR 规则之前配置域名规则，避免过早依赖系统 DNS。

`--via NAME` 明确指定连接后端，绕过自动规则。固定 VPN 的域名由该后端解析或交给网关连接。首版不支持自定义策略 DNS 服务器或 Fake IP 映射；调用方应提供域名或真实 IP。

多个节点使用不同证书时，将已经确认的证书依次放入同一个 PEM 文件。集合是固定的，不会自动采集或信任后续的新证书。任一列出的证书均须通过名称、有效期和服务端用途检查，未列出的证书不会通过节点探测或正式连接。


## 认证缓存与重启

`auth_state_file` 为空时，不持久化原生认证状态。启用后，状态绑定规范化的 HTTPS 网关、协议、认证类型、登录域、账号与认证组；不能复制给另一网关使用。一个运行会话持有状态文件的独占锁，另一个进程不能同时写同一份缓存。

有效缓存会在新进程的首次连接时恢复。明确失效时需要重新登录；网络故障不会直接清除缓存。`och logout --via NAME` 关闭该配置的连接并清理其缓存，不影响其他配置。

不要将认证状态文件放进源码仓库。文件权限保护不等于加密，缓存中的 cookie 属于敏感认证数据。向导默认把状态放在用户私有目录，而不是 YAML 所在目录。

## ssh 与 forwards

`ssh` 定义原生 SSH 连接，`forwards` 定义端口转发规则。这种分离参考 [GOST 的端口转发设计](https://gost.run/tutorials/port-forwarding/)。OCH 使用自己的 YAML 字段，不兼容 GOST 配置文件，也不解析 OpenSSH 配置。

```yaml
ssh:
  compute:
    address: compute.internal.example:22
    username: your_ssh_user
    via: campus
    identity_file: ~/.ssh/id_ed25519
    known_hosts_file: ~/.ssh/known_hosts
forwards:
  jupyter:
    direction: local
    ssh: compute
    listen: 8888
    target: 8888
  callback:
    direction: remote
    ssh: compute
    listen: 9000
    target: 3000
  database:
    via: campus
    listen: 15432
    target: "database.internal.example:5432"
```

运行 `och forward jupyter callback` 启动所选规则。仅定义规则不会让 `och serve` 自动运行它们。多个规则在同一次命令中可共享 SSH 会话。

每个 `ssh.NAME` 的字段：

| 字段 | 含义 |
| --- | --- |
| `address` | 必填 SSH 服务器 `HOST:PORT`，端口为 1–65535。IPv6 使用 `[ADDRESS]:PORT`。 |
| `username` | 必填 SSH 用户名，与 VPN 账号独立。 |
| `via` | SSH 底层连接路径，为 VPN 配置名或 `direct`；默认 `direct`。选择 VPN 后通过已有 OCH 后台连接，不回退直连。 |
| `identity_file` | 可选私钥文件。未设置时使用 agent 或 `password_env`，不自动寻找默认私钥。 |
| `known_hosts_file` | 主机密钥信任文件；默认 `~/.ssh/known_hosts`。未知、变更或已撤销的主机密钥均拒绝，不自动写入。 |
| `password_env` | 可选 SSH 密码的环境变量名；指定变量必须非空。不保存明文密码。 |
| `key_passphrase_env` | 加密私钥口令的环境变量名，要求设置 `identity_file`，指定变量必须非空。 |

密钥和 known_hosts 路径必须是绝对路径或以 `~/` 开头。`~/` 使用运行命令的用户 HOME 展开，不执行 shell、环境变量或 OpenSSH token 替换。SSH agent 通过当前命令的 `SSH_AUTH_SOCK` 使用。认证过程在前台进程中完成，未将 SSH 私钥或密码发送到 VPN 后台。SSH 握手使用配置的服务器名称和端口检查 known_hosts，即使其底层连接通过 Unix socket 传输。

每个 `forwards.NAME` 的字段：

| 字段 | 含义 |
| --- | --- |
| `direction` | `local` 或 `remote`；默认 `local`。 |
| `listen` | 必填监听 `PORT` 或 `IP:PORT`。只写端口时默认 `127.0.0.1`，显式 IP 必须为回环地址；端口为 0–65535，0 自动分配并在运行时报告。 |
| `target` | 必填目标 `PORT` 或 `HOST:PORT`。只写端口时默认 `127.0.0.1`，端口为 1–65535。 |
| `ssh` | 引用 `ssh.NAME`。与 `via` 二选一。`remote` 必须使用 SSH。 |
| `via` | 不使用 SSH 的本地 TCP 转发路径，为 VPN 配置名或 `direct`。与 `ssh` 二选一。 |

端口简写可使用 YAML 数字或字符串，例如 `listen: 8888` 与 `listen: "8888"` 等价。空主机形式 `:8888` 不支持。完整 IPv4、IPv6 或域名目标保持原有含义。

`local` 在 OCH 机器监听。设置 `ssh` 时，由 SSH 服务器连接 target；设置 `via` 时，由 OCH 通过所选路径连接 target。`remote` 在 SSH 服务器监听，target 由 OCH 机器的本地网络直连。远端 `127.0.0.1` 与本机 `127.0.0.1` 是不同地址空间。

SSH 会话中断会关闭已有流。后续本地连接可以重新建立会话，反向监听会重连并重新绑定；端口为 0 时重绑可能分配新端口。一次启动中的多个规则共用同一 SSH 配置；一次性目标连接失败不会使健康 SSH 会话失效。


## 诊断配置与运行状态

`och doctor --config FILE` 验证本地 YAML 并读取后台快照，不触发 VPN 初始化。针对目标的 `--policy` 检查使用后台当前配置；前台文件与运行中后台不同步时，诊断不会拿前台规则代替后台实际规则。修改后台配置后仍需按前述方式重启服务。

`och doctor --ssh NAME` 和 `--forward NAME` 使用前台的 `ssh` 与 `forwards` 配置。它们复用原生认证设置，不读取 OpenSSH 别名，不自动接受主机密钥。`--forward` 会临时检查规则的监听地址和目标，检查结束后释放资源。详细命令和 JSON 输出见[使用指南](usage.md#使用-doctor-定位连接问题)。
