# 原生共享会话

同一 VPN 配置共享初始化与原生传输。

## Sub-features

- `sessions` 已实现；惰性初始化、并发共享、服务退出清理。
- `auth-state` 已实现；私有、身份绑定的缓存和跨进程恢复。两种协议有独立进程隔离验证，aTrust 另有真实后台重启验证。
- `logout` 已实现；关闭单个配置的连接并清理其缓存，保留其他配置。
- `anyconnect` 已接入原生协议和用户态网络栈；一套既有校园网关凭据的真实认证及 SSH 目标连通性已验证，见验证记录。
- `atrust` 已接入原生认证与数据通道；一套校园 CAS 配置已完成原生会话、SSH 登录与代理端到端验证，使用明确确认的通道证书。
- `cas-login` 已实现独立 `och login --via NAME`、专用 Chromium 窗口、回调校验与共享会话交接；隔离测试通过。
- `mfa-ui` 未实现其他通用认证 UI；学校 CAS 页面内的验证码/MFA 由用户完成。

## How to get to it (user POV)

`och setup`、`och serve`、`och login --via NAME`、`och logout --via NAME`、`och status`、`och stop`；首个 profile 连接优先恢复有效认证状态。

## Driving it with isolated tests

Preconditions:

在仓库根目录使用 `python3 .agents/skills/verify/scripts/verify.py`，具备 SKILL.md 所列工具。

统一入口运行 Go 会话与原生后端测试，使用本地 fixtures 或内存 packet transport。检查初始化次数、取消、关闭和精确数据传输。

## Gotchas

已验证单个 AnyConnect 网关；aTrust 的单个校园 CAS 网关已验证；不代表其他网关、MFA、校内 DNS 或实际断线恢复均已验证。后台状态不等于资源可达，不承诺恢复断开的 SSH 流。
