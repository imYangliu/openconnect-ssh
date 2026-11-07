---
name: verify
description: Verify the native OCH daemon, SSH ProxyCommand, mixed SOCKS5/HTTP proxies and userspace VPN adapters using isolated loopback fixtures. Use for OCH behavior checks without credentials, system networking changes or personal SSH configuration.
---

# Verify native OCH

Read [the feature map](features/README.md). This skill targets the native rewrite, not v0. Run from the repository root.

## Launch

Run `python3 .agents/skills/verify/scripts/verify.py`. It builds `cmd/och` into `build/bin/och`, checks CLI identity, drives the compiled daemon/ProxyCommand journey, and runs `make check`. Requires Go, make, Python 3 and Bash. Dependencies may download into the normal Go cache. No VPN service or credentials are required.

Each run receives a temporary HOME and TMPDIR. Caller VPN variables and OCH settings are not inherited. Build output is shared within a checkout, so do not run simultaneous builds in the same checkout.

## Doctor

The helper runs the absolute built binary with `--help` and checks for the native ProxyCommand entry. It records the binary SHA-256. This establishes build identity, not VPN authentication. The CLI fixture starts `serve` with an isolated socket and checks `status` before driving connections.

## Drive

The helper executes `python3 tests/proxy.py` for the real compiled CLI. Package tests live beside their implementation under `internal/`; daemon tests cover the mixed-proxy and IPC integration. `make check` also runs Go tests covering policies, shared sessions, mixed protocols, IPC, configuration rejection, discovery, setup, private auth state and native packet transport fixtures. It also runs a real PTY test for hidden password input/cancellation. Read the exact tests before claiming a feature is covered.

Mocks are confined to VPN session creation; proxy and daemon tests use real local TCP/HTTP streams. Native packet tests use in-memory packet transport and userspace networking. No fixture should launch OpenConnect, create a system TUN, mutate host routing or inspect personal SSH configuration.

## Evidence

The helper prints a new absolute `och-evidence-*` directory. `report.json` stores commands, exit codes, platform, timestamp, binary hash and cleanup flags. Numbered stdout/stderr files capture command results. Keep that location in the report. Temporary OS storage may be cleaned later; durable summaries belong in verification.md.

A passing CLI fixture proves only the entry points it drives. Injected sessions do not prove AnyConnect/aTrust authentication. Packet-stack tests do not prove real gateway compatibility. Fresh-process state tests prove restore without credentials against local protocol fixtures. Real MFA, VPN DNS and TUN coexistence remain unverified unless explicitly tested with authorized infrastructure.

## Cleanup

Fixtures stop only the daemon and services they create. The helper removes temporary HOME/TMPDIR after success or failure and confirms scratch HOME did not acquire `.ssh`. Evidence stays outside scratch. Build artifacts and Go caches remain for reuse. Inspect retained logs on failure rather than treating an unfinished journey as passing.

## Helpers

`scripts/verify.py` is executable and uses the Python standard library. Invoke it with the command in Launch. When behavior changes, use `pstack:maintain-verification-skill` to reconcile the map and rerun affected journeys.

For behavior-preserving refactors, retain a pre-change binary and run `python3 tests/equivalence.py /path/to/baseline-och build/bin/och`. It compares CLI outputs and isolated IPC/proxy journeys without real VPN credentials.
