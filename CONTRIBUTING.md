# Contributing

## Before Opening a PR

Run the local checks:

```bash
bash -n ecnu-ssh
bash -n src/ecnu-ssh.sh
bash -n src/connect-campus-server.sh
bash -n src/ecnu-openconnect-keepalive.sh
bash -n install.sh
shellcheck ecnu-ssh src/ecnu-ssh.sh src/connect-campus-server.sh src/ecnu-openconnect-keepalive.sh install.sh
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
