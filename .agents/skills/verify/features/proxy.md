# SSH 固定与自动分流

ProxyCommand 通过共享服务连接目标。

## Sub-features

- `ssh-via` 已实现；固定 profile 或 direct。
- `ssh-policy` 已实现；有序域名、后缀、CIDR，未命中直连。
- `ssh-stream` 已实现；IPC 握手后转发与半关闭。
- `ssh-real-auth` 已在用户授权的单个校园目标完成真实 OpenSSH 登录验证；其他目标及认证方式未验证。

## How to get to it (user POV)

`och proxy-command --via NAME HOST PORT` 或 `--policy NAME`；OpenSSH 的 ProxyCommand 调用该入口。

## Driving it with isolated tests

Preconditions:

在仓库根目录使用 `python3 .agents/skills/verify/scripts/verify.py`，具备 SKILL.md 所列工具。

统一入口执行 tests/proxy.py 驱动编译后的 CLI；Go 测试注入 session 验证路由和无直连回退。应断言精确字节与实际选定地址。

## Gotchas

固定入口绕过策略。CIDR 使用真实 IP；Fake IP 不支持。域名多地址需匹配并连接同一地址，校内 DNS 需另行验收。
