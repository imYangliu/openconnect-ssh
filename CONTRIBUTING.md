# Contributing

## Before Opening a PR

Run the local checks:

```bash
make check            # macOS：含 swift build
make check-portable   # Linux/WSL：不依赖 Swift 工具链
```

Both run shell syntax checks, `shellcheck`, and the smoke tests over `och`,
`src/och.sh`, `src/och-vpn.sh`, `src/macos-vpnc-route-wrapper.sh`,
`src/och-sudo-askpass.sh`, and `install.sh`.

## Security Rules

- Do not commit real VPN usernames, passwords, hostnames, IP addresses, or ports.
- Keep all user-specific values in local config files outside the repository, or in ignored files.
- If you add new examples, use placeholders instead of real infrastructure.

## Scope

Small, targeted fixes are preferred:

- improve reliability
- improve configurability
- improve documentation
- keep Linux shell compatibility intact
