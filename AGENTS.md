# Repository guidelines

OCH is a Go command-line tool for macOS and Linux. Keep configuration, setup, VPN lifecycle and command dispatch in the root Go package. The macOS vpnc-script hook is in src/. The public CLI is och.

Format Go changes with gofmt. Run make check for the build, vet, Go tests and isolated integration checks. Keep tests independent of real credentials, privileged network changes and personal SSH configuration. Document commands in docs/usage.md and configuration in docs/configuration.md.
