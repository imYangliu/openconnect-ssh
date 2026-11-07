# 首次配置与认证恢复

用户填写网关后完成一次认证，保存配置和私有认证状态，随后启动服务并检查恢复结果。

## Sub-features

- `discover` 已实现；aTrust 公开认证方式和基本 AnyConnect XML/认证组发现，未知协议不冒充已识别。
- `setup` 已实现；配置合并、证书明确确认、私有缓存路径、启动后认证恢复验证。
- `setup-ssh` 已实现；用户选择后写独立 SSH 文件，保留已有配置，拒绝覆盖已有明确别名。
- `restart-auth` 已实现；有效 native cookie/state 跨进程恢复；过期后需显式登录。
- `secret-input` 已实现；密码不回显、Unicode 退格、取消与终端恢复。

## How to get to it (user POV)

`och setup`、`och auth-info --server URL`、`och login --via NAME`、`och logout --via NAME`、`och service install --config FILE`。

## Driving it with isolated tests

Preconditions:

运行仓库根目录的 `python3 .agents/skills/verify/scripts/verify.py`，只使用临时 HOME 与 loopback fixtures。

- make check 覆盖 discovery/setup/service/authstate/vpn 测试及真实 PTY 密码输入检查。
- native state 测试用本地 HTTPS/VPN 协议 fixture 保存状态，再启动新的测试进程，不提供密码或浏览器，断言恢复成功且未重复认证。
- 向导测试确认配置保存与后台 Ensure 成功后才报告 ready；认证失败、证书未确认、并发文件修改和旧服务仍运行时不得误报完成。
- SSH 测试仅修改临时 HOME；服务定义测试不操作真实用户服务管理器。

## Gotchas

认证缓存有权限保护但没有加密，不可提交或共享。认证成功后会先保存状态，后续证书/网络步骤取消可能保留私有缓存供重试。logout 清理 VPN 状态，不清理学校浏览器 SSO。AnyConnect 发现不是通用 SAML/MFA 引擎；Linux 服务安装需要可用的 systemd 用户会话。
