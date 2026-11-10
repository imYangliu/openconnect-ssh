# Native dependency notices

OCH's original authored source retains the MIT license in `LICENSE`. That file does not describe the licenses of all code linked into the native VPN executable. Do not label the combined native binary as MIT-only.

The native implementation uses:

- SagerNet `sing-openconnect`, GPL-3.0-or-later. Source: https://github.com/SagerNet/sing-openconnect . The precise upstream revision is recorded in `third_party/sing-openconnect/OCH_PATCHES.md`. The local fork adds an authenticated-cookie callback for private state persistence and reconnect handling described in that file; its upstream license is preserved.
- Mythologyli `zju-connect`, AGPL-3.0. Source: https://github.com/Mythologyli/zju-connect . The local reduced fork, upstream license, provenance and modifications are in `third_party/`.
- WireGuard's userspace network stack adapter and its gVisor dependency. Consult each pinned module's LICENSE and notices when redistributing.

The executable includes GPL/AGPL components. Preserve their notices and provide the corresponding source and build material under applicable upstream terms, including the AGPL provisions for modified network-accessible software. The repository release workflow includes this checkout's source and local third-party sources with each binary archive. The workflow also includes `go mod vendor` output in `source/vendor`, with module revisions recorded by `go.mod` and `go.sum`.

The first native release, [v0.0.1](https://github.com/imYangliu/openconnect-ssh/releases/tag/v0.0.1), has been published. These notices describe the current source tree; a distributed binary corresponds to its release tag and accompanying source. The archived external-OpenConnect workflow under `legacy/` is a different implementation and does not establish the license of the native executable.
