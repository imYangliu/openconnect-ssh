# Contributing

## Before Opening a PR

Run the local checks:

```bash
bash -n och
bash -n src/och.sh
bash -n src/och-vpn.sh
bash -n src/och-openconnect-keepalive.sh
bash -n src/macos-vpnc-route-wrapper.sh
bash -n src/och-sudo-askpass.sh
bash -n install.sh
shellcheck och src/och.sh src/och-vpn.sh src/och-openconnect-keepalive.sh src/macos-vpnc-route-wrapper.sh src/och-sudo-askpass.sh install.sh
```

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
