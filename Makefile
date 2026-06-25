SHELL := /bin/bash

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

.PHONY: help check check-portable check-shell shellcheck build run-gui smoke install clean

help:
	@printf '%s\n' \
		'可用命令:' \
		'  make check           运行全部检查（含 swift build，需 macOS）' \
		'  make check-portable  运行不依赖 Swift 的检查（适合 Linux/WSL）' \
		'  make check-shell     检查 shell 脚本语法' \
		'  make shellcheck      运行 shellcheck；未安装时跳过' \
		'  make build           构建 SwiftUI GUI' \
		'  make run-gui         构建并后台启动 GUI' \
		'  make smoke           运行轻量 smoke tests' \
		'  make install         安装 CLI 和运行时文件' \
		'  make clean           删除 SwiftPM 构建产物'

check: check-shell shellcheck build smoke

# 不含 swift build，可在 Linux/WSL（无 Swift 工具链）上运行
check-portable: check-shell shellcheck smoke

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

build:
	swift build

run-gui: build
	@app=".build/debug/OCHApp.app"; \
	rm -rf "$$app"; \
	mkdir -p "$$app/Contents/MacOS"; \
	mkdir -p "$$app/Contents/Resources/bin"; \
	mkdir -p "$$app/Contents/Resources/libexec/och"; \
	cp .build/debug/OCHApp "$$app/Contents/MacOS/OCHApp"; \
	cp -R .build/debug/OCH_OCHApp.bundle "$$app/OCH_OCHApp.bundle"; \
	install -m 0755 och "$$app/Contents/Resources/bin/och"; \
	install -m 0755 src/och-config.sh "$$app/Contents/Resources/libexec/och/och-config.sh"; \
	install -m 0755 src/och.sh "$$app/Contents/Resources/libexec/och/och.sh"; \
	install -m 0755 src/och-vpn.sh "$$app/Contents/Resources/libexec/och/och-vpn.sh"; \
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
	open -n "$$app"; \
	osascript -e 'tell application "OCH" to activate' >/dev/null 2>&1 || true; \
	echo "OCH launched: $$app"

smoke:
	@bash -euo pipefail -c '\
		tmpdir="$$(mktemp -d "$${TMPDIR:-/tmp}/och-smoke.XXXXXX")"; \
		trap "rm -rf \"$$tmpdir\"" EXIT; \
		./och --help >/dev/null; \
		: >"$$tmpdir/empty.env"; \
		set +e; \
		OCH_CONFIG_FILE="$$tmpdir/missing.toml" \
		ENV_FILE="$$tmpdir/empty.env" \
		./och --proxy-command 127.0.0.1 22 >"$$tmpdir/proxy-command.log" 2>&1; \
		status=$$?; \
		set -e; \
		if [[ $$status -eq 0 ]]; then \
			echo "och --proxy-command 在未配置 VPN 时不应成功" >&2; \
			cat "$$tmpdir/proxy-command.log" >&2; \
			exit 1; \
		fi; \
		grep -q "未设置 VPN_HOST" "$$tmpdir/proxy-command.log"; \
		reason=connect \
		TUNDEV=utun9 \
		MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16" \
		OCH_ROUTE_DRY_RUN=1 \
		VPNC_SCRIPT_BASE=/usr/bin/true \
		src/macos-vpnc-route-wrapper.sh >"$$tmpdir/routes-connect.log"; \
		grep -F "route -n add -net 10.0.0.0/8 -interface utun9" "$$tmpdir/routes-connect.log" >/dev/null; \
		grep -F "route -n add -net 192.168.0.0/16 -interface utun9" "$$tmpdir/routes-connect.log" >/dev/null; \
		reason=disconnect \
		TUNDEV=utun9 \
		MACOS_EXTRA_ROUTES="10.0.0.0/8 192.168.0.0/16" \
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
			"  ProxyCommand /opt/homebrew/bin/och --proxy-command %h %p" \
			>"$$tmpdir/ssh_config"; \
		ssh -F "$$tmpdir/ssh_config" -T -G och-target >"$$tmpdir/ssh-g.log"; \
		grep -F "proxycommand /opt/homebrew/bin/och --proxy-command %h %p" "$$tmpdir/ssh-g.log" >/dev/null; \
		echo "smoke tests passed"; \
	'
	@bash tests/unit-tests.sh

install:
	./install.sh

clean:
	rm -rf .build
