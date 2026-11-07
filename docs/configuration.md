# 原生 OCH 配置

命令通过 `--config FILE` 选择 YAML，默认 `~/.config/och/config.yaml`。此格式不兼容旧版外部 OpenConnect CLI。未知字段、重复键、空文档和多个 YAML 文档均会报错。解析错误不回显配置值。完整示例见 [examples/config.yaml](../examples/config.yaml)。修改后重启后台服务。

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

原生协议后端不会调用外部 OpenConnect。后端支持范围以实际实现和测试为准，认证方式、服务端资源发布方式和域名解析能力不能仅凭协议名称推断。CAS 可通过独立浏览器登录交接到后台；其他 MFA、扫码及浏览器认证方式不保证支持。aTrust 需要正确的登录域，未配置域与服务端不匹配时会明确失败。AnyConnect 使用 cookie 代替密码时，删除不再使用的 password_env 字段；所有指定的凭据环境变量必须非空。

## listeners

每个 `listeners.NAME` 导出一个 SOCKS5/HTTP 混合端口。

```yaml
listeners:
  campus:
    listen: "127.0.0.1:1080"
    via: campus
```

`listen` 必须是回环 IP 与端口，`via` 必须引用 VPN 配置。所有请求通过该配置，不应用自动分流，不在失败后回退直连。监听不是系统代理设置；只有主动使用该端口的应用受影响。

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
