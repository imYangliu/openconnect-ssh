# OCH local changes

Source: github.com/sagernet/sing-openconnect, v0.1.1-0.20261103042440-6c98622bada6. Upstream GPL-3.0-or-later LICENSE retained.

Adds ClientOptions.OnAuthenticatedCookie, called after a successful AnyConnect authentication continuation returns its session. It passes the actual tunnel server URL and reusable webvpn cookie to the caller. Callback failures close the obtained session. OCH binds the cookie to its gateway and stores private authentication state, never logs it or persists account passwords. No protocol or TLS verification changes.

Fixes the reconnect timeout to cover elapsed authentication, dialing, startup, and backoff after an established tunnel fails or requests a new-tunnel rekey. A per-reconnect watchdog publishes ErrReconnectTimeout and cancels the supervisor. Recovery stops the watchdog without attaching an expiring context to the healthy session. Initial connection behavior is unchanged.
