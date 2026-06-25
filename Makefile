SHELL := /bin/bash
RUST_CLI_MANIFEST := rust-cli/Cargo.toml
RUST_CLI_DEBUG_BIN := rust-cli/target/debug/och

SHELL_SCRIPTS := \
	och \
	src/och-config.sh \
	src/och.sh \
	src/och-setup.sh \
	src/och-vpn.sh \
	src/macos-vpnc-route-wrapper.sh \
	src/och-sudo-askpass.sh \
	install.sh \
	tests/unit-tests.sh

.PHONY: help check check-portable check-shell shellcheck check-rust check-swift-parsing build build-rust run-gui write-launchdaemon-plist signed-app smoke install clean

help:
	@printf '%s\n' \
		'可用命令:' \
		'  make check           运行全部检查（含 swift build，需 macOS）' \
		'  make check-portable  运行不依赖 Swift 的检查（适合 Linux/WSL）' \
		'  make check-shell     检查 shell 脚本语法' \
		'  make shellcheck      运行 shellcheck；未安装时跳过' \
		'  make check-rust      运行 Rust CLI 测试' \
		'  make check-swift-parsing  检查 Swift 状态解析' \
		'  make build           构建 SwiftUI GUI' \
		'  make build-rust      构建 Rust CLI' \
		'  make run-gui         构建并后台启动 GUI' \
		'  make signed-app      构建并签名 macOS 26+ App bundle（需要 SIGN_IDENTITY）' \
		'  make smoke           运行轻量 smoke tests' \
		'  make install         安装 CLI 和运行时文件' \
		'  make clean           删除 SwiftPM 构建产物'

check: check-shell shellcheck check-rust check-swift-parsing build smoke

# 不含 swift build，可在 Linux/WSL（无 Swift 工具链）上运行
check-portable: check-shell shellcheck check-rust smoke

check-shell:
	@for script in $(SHELL_SCRIPTS); do \
		echo "bash -n $$script"; \
		bash -n "$$script"; \
	done

shellcheck:
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck $(SHELL_SCRIPTS); \
	else \
		echo 'shellcheck 未安装，跳过。'; \
	fi

check-rust:
	cargo test --manifest-path $(RUST_CLI_MANIFEST)

check-swift-parsing:
	@status_bin="$$(mktemp "$${TMPDIR:-/tmp}/och-status-parser.XXXXXX")"; \
	config_bin="$$(mktemp "$${TMPDIR:-/tmp}/och-config-parser.XXXXXX")"; \
	trap 'rm -f "$$status_bin" "$$config_bin"' EXIT; \
	swiftc Sources/OCHApp/StatusParsing.swift tests/status-parser-smoke.swift -o "$$status_bin"; \
	"$$status_bin"; \
	swiftc Sources/OCHApp/AppConfig.swift tests/app-config-smoke-support.swift tests/app-config-smoke.swift -o "$$config_bin"; \
	"$$config_bin"

build-rust:
	cargo build --manifest-path $(RUST_CLI_MANIFEST)

build:
	swift build

run-gui: build build-rust
	@app=".build/debug/OCHApp.app"; \
	rm -rf "$$app"; \
	mkdir -p "$$app/Contents/MacOS"; \
	mkdir -p "$$app/Contents/Resources/bin"; \
	mkdir -p "$$app/Contents/Resources/libexec/och"; \
	mkdir -p "$$app/Contents/Library/LaunchServices"; \
	mkdir -p "$$app/Contents/Library/LaunchDaemons"; \
	cp .build/debug/OCHApp "$$app/Contents/MacOS/OCHApp"; \
	install -m 0755 .build/debug/OCHPrivilegedHelper "$$app/Contents/Library/LaunchServices/io.github.imyangliu.och.helper"; \
	cp -R .build/debug/OCH_OCHApp.bundle "$$app/OCH_OCHApp.bundle"; \
	install -m 0755 $(RUST_CLI_DEBUG_BIN) "$$app/Contents/Resources/bin/och"; \
	install -m 0755 src/och-config.sh "$$app/Contents/Resources/libexec/och/och-config.sh"; \
	install -m 0755 src/och-setup.sh "$$app/Contents/Resources/libexec/och/och-setup.sh"; \
	install -m 0755 src/macos-vpnc-route-wrapper.sh "$$app/Contents/Resources/libexec/och/macos-vpnc-route-wrapper.sh"; \
	install -m 0755 src/och-sudo-askpass.sh "$$app/Contents/Resources/libexec/och/och-sudo-askpass.sh"; \
	printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0">' \
		'<dict>' \
		'  <key>CFBundleExecutable</key>' \
		'  <string>OCHApp</string>' \
		'  <key>CFBundleIdentifier</key>' \
		'  <string>io.github.imyangliu.och</string>' \
		'  <key>CFBundleName</key>' \
		'  <string>OCH</string>' \
		'  <key>CFBundleDisplayName</key>' \
		'  <string>OCH</string>' \
		'  <key>CFBundlePackageType</key>' \
		'  <string>APPL</string>' \
		'  <key>CFBundleVersion</key>' \
		'  <string>1</string>' \
		'  <key>CFBundleShortVersionString</key>' \
		'  <string>0.1.0</string>' \
		'</dict>' \
		'</plist>' \
		> "$$app/Contents/Info.plist"; \
	$(MAKE) write-launchdaemon-plist APP="$$app"; \
	open -n "$$app"; \
	osascript -e 'tell application "OCH" to activate' >/dev/null 2>&1 || true; \
	echo "OCH launched: $$app"

write-launchdaemon-plist:
	@plist="$${APP}/Contents/Library/LaunchDaemons/io.github.imyangliu.och.helper.plist"; \
	printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0">' \
		'<dict>' \
		'  <key>Label</key>' \
		'  <string>io.github.imyangliu.och.helper</string>' \
		'  <key>BundleProgram</key>' \
		'  <string>Contents/Library/LaunchServices/io.github.imyangliu.och.helper</string>' \
		'  <key>MachServices</key>' \
		'  <dict>' \
		'    <key>io.github.imyangliu.och.helper</key>' \
		'    <true/>' \
		'  </dict>' \
		'</dict>' \
		'</plist>' \
		> "$$plist"

signed-app:
	@test -n "$${SIGN_IDENTITY:-}" || { echo 'SIGN_IDENTITY is required, e.g. SIGN_IDENTITY="Developer ID Application: ..."' >&2; exit 2; }
	cargo build --manifest-path $(RUST_CLI_MANIFEST) --release
	swift build -c release
	@set -e; \
	app=".build/release/OCH.app"; \
	rm -rf "$$app"; \
	mkdir -p "$$app/Contents/MacOS" "$$app/Contents/Resources/bin" "$$app/Contents/Resources/libexec/och" "$$app/Contents/Library/LaunchServices" "$$app/Contents/Library/LaunchDaemons"; \
	cp .build/release/OCHApp "$$app/Contents/MacOS/OCHApp"; \
	install -m 0755 .build/release/OCHPrivilegedHelper "$$app/Contents/Library/LaunchServices/io.github.imyangliu.och.helper"; \
	cp -R .build/release/OCH_OCHApp.bundle "$$app/OCH_OCHApp.bundle"; \
	install -m 0755 rust-cli/target/release/och "$$app/Contents/Resources/bin/och"; \
	install -m 0755 src/och-config.sh "$$app/Contents/Resources/libexec/och/och-config.sh"; \
	install -m 0755 src/och-setup.sh "$$app/Contents/Resources/libexec/och/och-setup.sh"; \
	install -m 0755 src/macos-vpnc-route-wrapper.sh "$$app/Contents/Resources/libexec/och/macos-vpnc-route-wrapper.sh"; \
	install -m 0755 src/och-sudo-askpass.sh "$$app/Contents/Resources/libexec/och/och-sudo-askpass.sh"; \
	printf '%s\n' \
		'<?xml version="1.0" encoding="UTF-8"?>' \
		'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
		'<plist version="1.0">' \
		'<dict>' \
		'  <key>CFBundleExecutable</key>' \
		'  <string>OCHApp</string>' \
		'  <key>CFBundleIdentifier</key>' \
		'  <string>io.github.imyangliu.och</string>' \
		'  <key>CFBundleName</key>' \
		'  <string>OCH</string>' \
		'  <key>CFBundleDisplayName</key>' \
		'  <string>OCH</string>' \
		'  <key>CFBundlePackageType</key>' \
		'  <string>APPL</string>' \
		'  <key>CFBundleVersion</key>' \
		'  <string>1</string>' \
		'  <key>CFBundleShortVersionString</key>' \
		'  <string>0.1.0</string>' \
		'</dict>' \
		'</plist>' \
		> "$$app/Contents/Info.plist"; \
	$(MAKE) write-launchdaemon-plist APP="$$app"; \
	codesign --force --options runtime --timestamp --sign "$$SIGN_IDENTITY" "$$app/Contents/Library/LaunchServices/io.github.imyangliu.och.helper"; \
	codesign --force --options runtime --timestamp --sign "$$SIGN_IDENTITY" "$$app/Contents/Resources/bin/och"; \
	codesign --force --options runtime --timestamp --deep --sign "$$SIGN_IDENTITY" "$$app"; \
	codesign --verify --deep --strict --verbose=2 "$$app"; \
	echo "Signed app: $$app"

smoke: build-rust
	@bash -euo pipefail -c '\
		tmpdir="$$(mktemp -d "$${TMPDIR:-/tmp}/och-smoke.XXXXXX")"; \
		trap "rm -rf \"$$tmpdir\"" EXIT; \
		$(RUST_CLI_DEBUG_BIN) --help >/dev/null; \
		mkdir -p "$$tmpdir/bin"; \
		printf "%s\n" "#!/bin/bash" "if [[ \"\$$1 \$$2 \$$3\" == \"route get\"* ]]; then echo \"1.2.3.4 via 10.0.0.1 dev tun0\"; else echo \"default via 10.0.0.1 dev eth0\"; fi" >"$$tmpdir/bin/ip"; \
		printf "%s\n" "#!/bin/bash" "exit 1" >"$$tmpdir/bin/nc"; \
		printf "%s\n" "#!/bin/bash" "exit 0" >"$$tmpdir/bin/sudo"; \
		printf "%s\n" "#!/bin/bash" "exit 0" >"$$tmpdir/bin/openconnect"; \
		chmod +x "$$tmpdir/bin/ip" "$$tmpdir/bin/nc" "$$tmpdir/bin/sudo" "$$tmpdir/bin/openconnect"; \
		set +e; \
		OCH_CONFIG_FILE="$$tmpdir/missing.toml" \
		OCH_SECRETS_FILE="$$tmpdir/missing-secrets.env" \
		OS_NAME=Linux \
		PATH="$$tmpdir/bin:$$PATH" \
		$(RUST_CLI_DEBUG_BIN) proxy-command 127.0.0.1 22 >"$$tmpdir/proxy-command.log" 2>&1; \
		status=$$?; \
		set -e; \
		if [[ $$status -eq 0 ]]; then \
			echo "och proxy-command 在未配置 VPN 时不应成功" >&2; \
			cat "$$tmpdir/proxy-command.log" >&2; \
			exit 1; \
		fi; \
		grep -q "未设置 \\[vpn\\].host" "$$tmpdir/proxy-command.log"; \
		reason=connect \
		TUNDEV=utun9 \
		OCH_ROUTES_EXTRA="10.0.0.0/8 192.168.0.0/16" \
		OCH_ROUTE_DRY_RUN=1 \
		VPNC_SCRIPT_BASE=/usr/bin/true \
		src/macos-vpnc-route-wrapper.sh >"$$tmpdir/routes-connect.log"; \
		grep -F "route -n add -net 10.0.0.0/8 -interface utun9" "$$tmpdir/routes-connect.log" >/dev/null; \
		grep -F "route -n add -net 192.168.0.0/16 -interface utun9" "$$tmpdir/routes-connect.log" >/dev/null; \
		reason=disconnect \
		TUNDEV=utun9 \
		OCH_ROUTES_EXTRA="10.0.0.0/8 192.168.0.0/16" \
		OCH_ROUTE_DRY_RUN=1 \
		VPNC_SCRIPT_BASE=/usr/bin/true \
		src/macos-vpnc-route-wrapper.sh >"$$tmpdir/routes-disconnect.log"; \
		grep -F "route -n delete -net 10.0.0.0/8 -interface utun9" "$$tmpdir/routes-disconnect.log" >/dev/null; \
		grep -F "route -n delete -net 192.168.0.0/16 -interface utun9" "$$tmpdir/routes-disconnect.log" >/dev/null; \
		printf "%s\n" \
			"Host och-target" \
			"  HostName 127.0.0.1" \
			"  User test-user" \
			"  Port 22" \
			"  ProxyCommand /opt/homebrew/bin/och proxy-command %h %p" \
			>"$$tmpdir/ssh_config"; \
		ssh -F "$$tmpdir/ssh_config" -T -G och-target >"$$tmpdir/ssh-g.log"; \
		grep -F "proxycommand /opt/homebrew/bin/och proxy-command %h %p" "$$tmpdir/ssh-g.log" >/dev/null; \
		echo "smoke tests passed"; \
	'
	@bash tests/unit-tests.sh

install:
	@prefix="$${PREFIX:-}"; \
	if [[ -z "$$prefix" ]]; then \
		if [[ "$$(uname -s)" == "Darwin" ]]; then \
			prefix="/opt/homebrew"; \
		else \
			prefix="/usr/local"; \
		fi; \
	fi; \
	bin_dir="$${BIN_DIR:-$$prefix/bin}"; \
	libexec_dir="$${LIBEXEC_DIR:-$$prefix/libexec/och}"; \
	config_dir="$${CONFIG_DIR:-/etc/och}"; \
	cargo build --manifest-path $(RUST_CLI_MANIFEST) --release; \
	install -d "$$bin_dir" "$$libexec_dir" "$$config_dir"; \
	install -m 0755 rust-cli/target/release/och "$$bin_dir/och"; \
	install -m 0755 src/och-config.sh "$$libexec_dir/och-config.sh"; \
	install -m 0755 src/och-setup.sh "$$libexec_dir/och-setup.sh"; \
	install -m 0755 src/macos-vpnc-route-wrapper.sh "$$libexec_dir/macos-vpnc-route-wrapper.sh"; \
	install -m 0755 src/och-sudo-askpass.sh "$$libexec_dir/och-sudo-askpass.sh"; \
	install -m 0644 examples/ssh_config.example "$$config_dir/ssh_config.example"; \
	echo "Installed local source build to $$bin_dir/och"

clean:
	rm -rf .build
