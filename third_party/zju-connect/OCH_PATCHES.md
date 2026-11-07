# OCH embedded aTrust source

Source: https://github.com/Mythologyli/zju-connect/tree/8576e7a
Retrieved 2026-11-07. Upstream LICENSE is preserved. This source subset is AGPL-3.0; OCH's own MIT notice does not replace those obligations.

Only the packages needed by `client/atrust` are retained. The `resolve` package is reduced to the upstream context keys and helpers used by its TCP tunnel. The standalone GetAuthInfoList, SetTrusted and platform-underlay convenience functions are omitted; OCH supplies a plain Go underlay dialer. No upstream command, system TUN, DNS-server or OS-routing code is used.

Local changes:

- TLS certificate verification is enabled by default for authentication, IP allocation, TCP tunnels and L3 tunnels. ClientOptions adds a TLSConfig field, passed to the auth HTTP transport and tunnel dialers. OCH's explicit insecure profile option is propagated per client, without global TLS state.
- Invalid session authentication returns errors instead of exiting the process. Session refresh marks the session invalid and stops that client's refresh loop. L3 resource failures complete their waiting authentication with an error.
- Logging is suppressed at this module's logger boundary. Upstream diagnostics can include authentication responses, personal details and packet payloads. OCH owns diagnostics; the embedded logger has no Fatal or stdout output.
- SessionError exposes terminal control-plane failure without exposing credentials. Close marks the client closed; invalid TCP-session responses mark the client terminal.
- Auth sessions transfer from the setup context to the client lifecycle context before background refresh starts. Successful login's deferred setup cancellation no longer cancels every later refresh request.
- `och_test.go` verifies secure tunnel defaults, invalid-session errors, terminal health and refresh after setup cancellation against a local TLS server.

Run local fork checks from the OCH repository root:

```sh
go test github.com/mythologyli/zju-connect/client/atrust/...
```

Real aTrust gateways, MFA/browser challenges, server-driven address changes and vendor-specific resource policies have not been validated here. Unsupported authentication challenges return an explicit error. The embedded code is a pinned source snapshot, not an automatic upstream update.

CAS portal redirects normalize HTTPS origin comparison so an explicit port 443 matches the configured default port. Foreign hosts/ports, userinfo, fragments, encoded alternate paths and ambiguous data parameters remain rejected. Malformed redirect/data errors no longer echo URLs or ticket-bearing JSON. Tests cover both accepted origin forms and rejection cases.

ClientOptions also accepts TunnelServerName. It sets the SNI and verification name only on a cloned TLS configuration used by IP allocation, TCP and L3 tunnel connections. HTTPS authentication retains its own endpoint identity. Certificate chain verification remains enabled unless the caller independently opts into insecure mode. The tunnel TLS tests cover IP dialing with a DNS certificate, wrong names and untrusted issuers.

Optional explicitly configured TunnelCertificates create a fresh tunnel-only root pool anchored to the finite certificate set and additionally require constant-time equality of the leaf DER with a configured member. Hostname, validity and server-auth EKU checks remain active. This is never populated automatically from the network or used for HTTPS authentication; initial trust must be established separately. Tests reject altered certificates with the same key, wrong names, expiry, future validity and wrong EKU.

Node selection now passes the same tunnel TLS settings into reachability probes. Untrusted nodes cannot win on latency; trusted WAN selection and trusted LAN fallback remain. Probes send no session credentials or application data. Node-index tracking also skips malformed node strings correctly. Tests cover faster untrusted nodes, exact-pinned selection and LAN fallback.

The PEM loader accepts one or more certificate blocks, deduplicates exact DER, and rejects empty, malformed, nested, private-key, header-bearing or trailing non-certificate content. Multiple approved node certificates are supported without automatically trusting later changes. Tests accept each approved member and reject an unlisted certificate, including a changed certificate using an approved public key.

Authentication-state integration adds ResumeOnly to reject expired saved sessions before submitting empty credentials, a BeforeTunnel hook for explicit first-use certificate approval after authenticated resource discovery, and a synchronized ConfigureTunnelTrust boundary before tunnel initialization. The reusable authenticated snapshot is saved before certificate/node setup, and subsequent refreshes use the same save callback. Explicit invalid-SID IP responses wrap ErrSessionInvalid so stale state can be cleared; ordinary network/TLS/resource errors do not erase cache.
