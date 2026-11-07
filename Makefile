PREFIX ?= $(if $(filter Darwin,$(shell uname -s)),/opt/homebrew,/usr/local)
.PHONY: help build check check-portable smoke install clean
help:
	@echo 'make build | check | install | clean'
build:
	go build -trimpath -o build/bin/och ./cmd/och
check: build
	go vet ./...
	go test ./...
	go test github.com/mythologyli/zju-connect/client/atrust/...
	python3 tests/proxy.py
	python3 tests/password_prompt.py
	@for script in install.sh legacy/openconnect/*.sh legacy/openconnect/src/*.sh; do bash -n "$$script" || exit; done
	bash legacy/openconnect/tests-unit-v0.sh
check-portable smoke: check
install: build
	install -d "$(PREFIX)/bin"
	install -m 0755 build/bin/och "$(PREFIX)/bin/och"
clean:
	rm -rf build
