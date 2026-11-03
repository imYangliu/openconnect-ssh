# Contributing

## Before Opening a PR

Run the local checks:

```bash
make check
```

This builds the Go CLI and checks its configuration, installer, route wrapper and SSH proxy.

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
