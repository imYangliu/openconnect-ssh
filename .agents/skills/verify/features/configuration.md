# 原生配置

加载原生 YAML，拒绝旧格式和无效引用。

## Sub-features

- `config-native` 已实现；profiles、policies、listeners、私有 socket。
- `config-reject` 已实现；未知字段、重复键、多文档、空文档、规则多条件、未知 profile、非回环监听拒绝；解析错误不回显配置值。
- `setup-native` 已实现并通过隔离验证；发现认证方式、保存私有状态、合并 YAML、可选用户服务及 SSH 配置。旧 TOML 流程不兼容。

## How to get to it (user POV)

`och serve --config FILE`、`och status --config FILE`、`och proxy-command --config FILE ...`。

## Driving it with isolated tests

Preconditions:

在仓库根目录使用 `python3 .agents/skills/verify/scripts/verify.py`，具备 SKILL.md 所列工具。

运行统一验证入口。Go 配置测试断言合法配置与拒绝分支；CLI 使用临时原生 YAML。

## Gotchas

配置不兼容旧版外部 OpenConnect CLI。验证报告记录执行结果，不能只凭编译通过宣称所有字段均已验证。
