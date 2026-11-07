# OCH

OCH 用一个 Go 二进制提供原生 VPN 会话、SSH ProxyCommand 和 SOCKS5/HTTP 混合代理。它面向通过 SSH 访问校内服务器的场景，也可以作为 sing-box 等应用的 VPN 代理出口。

**`v0.0.1` 是首个原生版本。** 支持 AnyConnect 兼容协议与 aTrust，不调用外部 OpenConnect，不为连接创建系统 TUN 或修改系统路由。

## 为什么做 OCH

部分高校的 VPN 策略会将全局流量导入隧道，影响其他网络访问，并可能与现有工具的 TUN 模式冲突。OCH 在进程内部维护 VPN 连接，只处理交给它的流量。

- **SSH 固定去向**：指定 VPN 配置，直接通过该 VPN 连接。
- **SSH 自动分流**：按域名、域名后缀或 CIDR 规则选择路径，未匹配直连。
- **混合代理导出**：同一回环端口提供 SOCKS5、HTTP CONNECT 和普通 HTTP 代理，固定绑定一个 VPN，由调用方负责分流。
- **共享会话**：首次使用时认证，多个连接复用同一 VPN 会话；已选定的 VPN 连接失败时返回错误，不回退直连。

## 快速开始

在 macOS 或 Linux 上准备 Go 1.25.6 或更新版本、make，以及 VPN 账号。运行以下命令安装到用户目录：

```bash
git clone https://github.com/imyangliu/openconnect-ssh.git
cd openconnect-ssh
bash install.sh --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
och --help
```

安装器从当前 checkout 构建，不下载 release，也不安装 OpenConnect。原生重写尚未发布二进制，旧版 release 不支持本文命令。

然后运行首次配置向导：

```bash
och setup
```

填写网关后，向导会发现支持的认证方式、引导登录、保存私有认证状态，并在服务重新读取状态后检查 VPN 是否就绪。可选择登录时自动启动，以及配置 SSH 别名。私有通道证书需要明确确认，不会自动放行。

配置成功后，日常只需 `ssh <别名>`，或使用向导输出的混合代理地址。服务重启会优先恢复仍有效的认证状态；状态过期时运行 `och login --via <配置名>`。密码不会写入 YAML。详细步骤见[使用指南](docs/usage.md)。

## 支持范围

| 项目 | 当前状态 |
| --- | --- |
| AnyConnect | 已用一套实际校园配置验证认证、SSH 登录、固定/自动分流、SOCKS5 和 HTTP CONNECT。 |
| aTrust | 已用一套校园 CAS 配置验证原生会话、SSH 登录、SOCKS5 和 HTTP CONNECT；该环境使用用户确认的精确通道证书固定。 |
| macOS | 已在 Apple Silicon 上运行和验证。 |
| Linux | amd64 交叉编译通过；真实部署需单独验收。 |
| 认证 | 支持已实现的密码认证流程；AnyConnect 可传入已认证 cookie；aTrust CAS 可用 `och login` 交互登录。其他 MFA/扫码方式未完整支持。 |
| 代理 | TCP、回环监听；暂不支持 SOCKS5 UDP、Fake IP 映射或代理密码认证。 |

单个网关验证通过不代表所有高校配置均兼容。已有 TUN、校内 DNS、IPv6 和实际断线恢复仍需按环境验证。

## 文档

- [使用指南](docs/usage.md)：安装、启动、SSH、混合代理、后台服务与故障排查。
- [配置参考](docs/configuration.md)：YAML 字段、规则顺序与 DNS 行为。
- [完整配置示例](examples/config.yaml)和 [SSH 示例](examples/ssh_config.example)。
- [架构说明](docs/architecture.md)与[分流方式](docs/split-tunneling.md)。
- [Feature Map](.agents/skills/verify/features/README.md)和[验证记录](.agents/skills/verify/verification.md)。

当前原生版不兼容旧版外部 OpenConnect CLI 的配置和命令。旧版的网段路由脚本及文档归档在 [legacy/openconnect](legacy/openconnect/README.md)。

## 开发与许可

运行 `make check` 完成构建、vet、Go 测试及隔离集成检查，不需要真实 VPN 凭据。贡献约定见 [CONTRIBUTING.md](CONTRIBUTING.md)。

OCH 原始代码保留 MIT 许可，原生 VPN 依赖包含 GPL/AGPL 代码，组合二进制不能标为仅 MIT。依赖来源与分发材料见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
