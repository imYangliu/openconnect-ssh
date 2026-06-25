# Repository Guidelines

## 项目结构与模块组织

OCH 是一个混合 macOS/Linux 项目。SwiftPM 代码在 `Sources/`：`OCHApp` 是 SwiftUI GUI，`OCHPrivilegedHelper` 是 macOS root helper，`OCHXPCClient` 和 `OCHXPCRequirement` 负责 XPC 通信支持。Rust CLI 在 `rust-cli/`，构建产物是 `och`。Shell 运行时、安装和 VPN helper 在 `src/`，开发入口包装器是根目录的 `och`。用户文档在 `docs/`，示例在 `examples/`，测试在 `tests/` 和 `rust-cli/tests/`。

## 构建、测试与开发命令

- `make help`：查看所有可用开发目标。
- `make check`：运行完整检查，包含 Swift 构建；适合 macOS 开发环境。
- `make check-portable`：运行不依赖 Swift 的 shell、Rust 和 smoke 检查；适合 Linux/WSL。
- `make build`：构建 SwiftPM macOS app 和 helper targets。
- `make build-rust`：从 `rust-cli/Cargo.toml` 构建 Rust CLI。
- `make run-gui`：组装并启动本地 debug 版 `OCH.app`。
- `make smoke`：构建 Rust CLI 并运行轻量集成 smoke tests。
- `sudo make install`：从本地源码安装 CLI 和运行时文件。

## 编码风格与命名约定

Swift 和 Rust 使用 4 空格缩进，shell 脚本使用 2 空格缩进以匹配现有文件。Shell 脚本保持 Bash 兼容，使用 `#!/usr/bin/env bash` 和 `set -euo pipefail`。Rust 模块和函数使用 `snake_case`；Swift 类型使用 `UpperCamelCase`，属性和方法使用 `lowerCamelCase`。修改 Rust 后运行 `cargo fmt --manifest-path rust-cli/Cargo.toml`。当前没有专用 Swift formatter，请保持现有 SwiftUI 风格。

## 测试指南

Rust 集成测试放在 `rust-cli/tests/`，文件名应描述被测行为，例如 `proxy_command.rs`。Shell 和跨语言检查放在 `tests/`；修改运行时行为时，优先向 `tests/unit-tests.sh` 或小型 smoke 测试添加聚焦用例。macOS GUI/helper 相关 PR 提交前运行 `make check`；便携 CLI 或脚本改动至少运行 `make check-portable`。

## Commit 与 Pull Request 规范

历史提交多使用简短祈使句，也会使用作用域，例如 `feat(gui): ...` 或 `docs(test): ...`。保持提交聚焦，并说明用户可见行为变化。PR 应包含简短摘要、测试结果（`make check` 或 `make check-portable`）、相关 issue 链接；GUI 改动请附截图或录屏。

## 安全与配置提示

不要提交真实 VPN 用户名、密码、主机名、IP、端口、Keychain 数据或本地 `config.toml` 内容。示例必须使用占位值。用户专属配置应放在仓库外，或放入已忽略的本地文件。
