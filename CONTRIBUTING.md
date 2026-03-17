# Contributing

## Before Opening a PR

Run the local checks:

```bash
bash -n bin/ecnu-ssh
bash -n bin/connect-campus-server.sh
bash -n bin/ecnu-openconnect-keepalive.sh
bash -n install.sh
shellcheck bin/ecnu-ssh bin/connect-campus-server.sh bin/ecnu-openconnect-keepalive.sh install.sh
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
