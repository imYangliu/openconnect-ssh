# Contributing

Run `make check` for the native Go CLI, vet, Go tests and isolated integration checks.

Keep cmd/och/main.go as the executable entry. Implementation packages live under internal/: config, routing, vpn, proxy, daemon and cli. Keep unit tests beside their owning package; tests/ holds compiled CLI integration checks. Format Go edits with gofmt. Tests use temporary configuration, loopback endpoints and injected VPN session factories; never use personal SSH files, real VPN credentials or privileged network changes.

The v0 OpenConnect workflow is archived under `legacy/openconnect/`. New code does not preserve its CLI or TOML compatibility. Document current commands in `docs/usage.md` and configuration in `docs/configuration.md`.

Native protocol dependencies include copyleft-licensed code. Preserve upstream notices and source provenance; see `THIRD_PARTY_NOTICES.md` before distributing binaries.

For package or interface refactors, save a baseline binary before editing and compare it with the candidate using `python3 tests/equivalence.py /path/to/baseline-och build/bin/och`.
