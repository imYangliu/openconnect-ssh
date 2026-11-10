# 原生 SSH 与 TCP 端口转发

`och forward` 在前台管理原生 SSH 会话和所选转发规则。

## Sub-features

- `forward-local` 已实现；本地监听，经原生 SSH 连接远端目标。
- `forward-remote` 已实现；SSH 服务器监听，OCH 从本机连接目标。
- `forward-direct` 已实现；本地监听，经指定 VPN 或 direct 连接目标。
- `forward-sharing` 已实现；同次命令中复用具名 SSH 会话，失效后新连接可重建，反向监听重连后重绑。
- `forward-trust` 已实现；known_hosts 校验，私钥、加密私钥口令环境变量、密码环境变量与 SSH agent。

## How to get to it (user POV)

在 YAML 中配置 `ssh` 与 `forwards`，运行 `och forward jupyter callback`。也可使用 `och forward --ssh compute LISTEN TARGET`、`--reverse --ssh compute` 或 `--via campus`。监听与目标都可只写端口，默认 `127.0.0.1`，例如 `och forward --ssh compute 8888 8888`；YAML 支持数字和字符串端口。SSH 名称属于 OCH YAML，不是 OpenSSH 别名。

## Driving it with isolated tests

`go test ./internal/config ./internal/forward` 验证端口简写和严格配置、真实回环 SSH 握手、正反向二进制流、半关闭、会话复用、目标拒绝、断线重绑、密钥与认证拒绝、agent 和取消。`TestCompiledCLINativeForwardingThroughIPC` 构建真实 CLI，在临时 HOME 中经私有 IPC 使用注入的回环 VPN 后端；PATH 不提供系统 ssh，并检查无个人 SSH 配置写入。`make check` 包含这些 Go 测试。

## Gotchas

仅 TCP、回环监听。SSH 主机密钥需事先独立核验；不自动接受或更新。`och serve` 不自动启动转发规则。SSH 或 VPN 重建不恢复已断开的应用连接。隔离 SSH 测试不证明真实高校网关或所有 SSH 服务配置均可用。
