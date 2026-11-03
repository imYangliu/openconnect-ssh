PREFIX ?= $(if $(filter Darwin,$(shell uname -s)),/opt/homebrew,/usr/local)
.PHONY: help build check check-portable smoke install clean
help:
	@echo 'make build | check | install | clean'
build:
	go build -trimpath -o build/bin/och .
	install -d build/libexec/och
	install -m 0755 src/macos-vpnc-route-wrapper.sh build/libexec/och/
check: build
	go vet ./...
	go test ./...
	@if command -v shellcheck >/dev/null; then shellcheck install.sh src/*.sh och tests/*.sh; fi
	bash -n install.sh
	bash -n src/macos-vpnc-route-wrapper.sh
	bash tests/unit-tests.sh
	python3 tests/proxy.py
check-portable smoke: check
install: build
	install -d "$(PREFIX)/bin" "$(PREFIX)/libexec/och"
	install -m 0755 build/bin/och "$(PREFIX)/bin/och"
	install -m 0755 src/macos-vpnc-route-wrapper.sh "$(PREFIX)/libexec/och/"
clean:
	rm -rf build
