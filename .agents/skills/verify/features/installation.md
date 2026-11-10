# 构建与安装

从源码构建单个原生二进制，不安装外部 OpenConnect。

## Sub-features

- `build-native` 已实现 make build。
- `install-local` 已实现本地源码安装。
- `user-service` 已实现用户级 launchd/systemd 定义生成与安装入口，隔离测试验证定义和进程启动；Linux 系统服务实机尚未验证。
- `release-native` v0.0.2 发布原生 CLI，提供 macOS arm64 和 Linux x86_64 包；每个包附完整源码与 vendored 依赖，功能以对应标签为准。
- `legacy` 旧安装器与网络脚本独立保留。

## How to get to it (user POV)

`make build`、`bash install.sh --prefix PATH`。

## Driving it with isolated tests

Preconditions:

在仓库根目录使用 `python3 .agents/skills/verify/scripts/verify.py`，具备 SKILL.md 所列工具。

统一入口验证 build。安装如需验收使用临时 prefix 并核对产物，不写系统目录。legacy 仅做语法检查，不能视作真实网络测试。

## Gotchas

依赖涉及 GPL/AGPL。构建成功不等于已发布，不执行旧版安装器代替原生安装验证。
