"""Compare two OCH binaries over isolated CLI, daemon and proxy journeys.

Usage: python3 tests/equivalence.py BASELINE CANDIDATE
"""
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import threading
import time


def main():
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    binaries = [str(pathlib.Path(p).resolve()) for p in sys.argv[1:]]
    with tempfile.TemporaryDirectory(prefix="och-equivalence-", dir="/tmp") as scratch, socket.socket() as listener:
        root = pathlib.Path(scratch)
        listener.bind(("127.0.0.1", 0))
        listener.listen()
        listener.settimeout(0.2)
        stopped = threading.Event()
        port = listener.getsockname()[1]

        def echo(conn):
            with conn:
                conn.settimeout(5)
                data = bytearray()
                while True:
                    chunk = conn.recv(4096)
                    if not chunk:
                        break
                    data.extend(chunk)
                conn.sendall(b"SSH-2.0-equivalence\r\n" + data)

        def accept():
            while not stopped.is_set():
                try:
                    conn, _ = listener.accept()
                except socket.timeout:
                    continue
                except OSError:
                    return
                threading.Thread(target=echo, args=(conn,), daemon=True).start()

        thread = threading.Thread(target=accept)
        thread.start()
        env = {"HOME": scratch, "PATH": os.environ["PATH"]}
        config = root / "config.yaml"
        invalid = root / "invalid.yaml"
        invalid.write_text("unexpected: private-fixture-value\n")
        config.write_text(f'''socket: "{root / 'och.sock'}"
profiles:
  campus:
    protocol: anyconnect
    server: https://vpn.invalid
    password_env: OCH_EQ_MISSING_SECRET
listeners:
  campus:
    listen: "127.0.0.1:0"
    via: campus
policies:
  local:
    rules:
      - domain: localhost
        via: direct
      - cidr: 127.0.0.0/8
        via: direct
  fallback:
    rules: []
''')

        def exercise(binary):
            results = {}

            def run(name, args, payload=b""):
                result = subprocess.run([binary] + args, input=payload, capture_output=True, env=env, timeout=10)
                results[name] = [result.returncode, result.stdout.decode("utf-8", "backslashreplace"), result.stderr.decode("utf-8", "backslashreplace")]
                return result

            for args in [["--help"], ["unknown"], ["serve", "--help"], ["proxy-command", "--help"], ["status", "--config", str(invalid)]]:
                run(" ".join(args), args)
            with (root / "daemon.log").open("w+") as log:
                daemon = subprocess.Popen([binary, "serve", "--config", str(config)], env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log)
                try:
                    for _ in range(100):
                        if daemon.poll() is not None:
                            log.seek(0)
                            raise AssertionError(log.read())
                        if (root / "och.sock").exists():
                            break
                        time.sleep(0.02)
                    state = subprocess.run([binary, "status", "--config", str(config)], capture_output=True, env=env, check=True, timeout=5)
                    state = json.loads(state.stdout)
                    proxy_address = state["listeners"]["campus"]
                    state["listeners"]["campus"] = "127.0.0.1:<ephemeral>"
                    results["initial_status"] = state
                    payload = b"payload\x00\xff"
                    for name, selector, host in [
                        ("fixed_direct", ["--via", "direct"], "127.0.0.1"),
                        ("cidr", ["--policy", "local"], "127.0.0.1"),
                        ("domain", ["--policy", "local"], "localhost"),
                        ("unmatched", ["--policy", "fallback"], "127.0.0.1"),
                        ("vpn_failure", ["--via", "campus"], "127.0.0.1"),
                        ("unknown_policy", ["--policy", "missing"], "127.0.0.1"),
                        ("unknown_profile", ["--via", "missing"], "127.0.0.1"),
                    ]:
                        result = run(name, ["proxy-command", "--config", str(config)] + selector + [host, str(port)], payload)
                        if name in ("fixed_direct", "cidr", "domain", "unmatched"):
                            assert result.returncode == 0 and result.stdout == b"SSH-2.0-equivalence\r\n" + payload, name
                        else:
                            assert result.returncode != 0 and not result.stdout, name
                    proxy_host, proxy_port = proxy_address.rsplit(":", 1)
                    with socket.create_connection((proxy_host, int(proxy_port)), timeout=5) as conn:
                        conn.sendall(b"\x05\x01\x00")
                        assert conn.recv(2) == b"\x05\x00"
                        conn.sendall(b"\x05\x01\x00\x01\x7f\x00\x00\x01" + port.to_bytes(2, "big"))
                        results["socks_failure"] = conn.makefile("rb").read(10).hex()
                    for name, req in [
                        ("connect_failure", f"CONNECT 127.0.0.1:{port} HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n\r\n"),
                        ("http_failure", f"GET http://127.0.0.1:{port}/ HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\n\r\n"),
                    ]:
                        with socket.create_connection((proxy_host, int(proxy_port)), timeout=5) as conn:
                            conn.sendall(req.encode())
                            results[name] = conn.makefile("rb").read().decode()
                    run("stop", ["stop", "--config", str(config)])
                    assert daemon.wait(timeout=5) == 0
                    assert not (root / "och.sock").exists()
                finally:
                    if daemon.poll() is None:
                        daemon.terminate()
                        try:
                            daemon.wait(timeout=5)
                        except subprocess.TimeoutExpired:
                            daemon.kill()
                            daemon.wait()
            return results

        try:
            before, after = [exercise(binary) for binary in binaries]
            if before != after:
                for name in before:
                    if before[name] != after.get(name):
                        print(json.dumps({"case": name, "before": before[name], "after": after.get(name)}, ensure_ascii=False))
                raise SystemExit("CLI behavior changed")
            print(f"Equivalent: {len(before)} CLI/IPC/proxy observations; exact binary payloads and daemon cleanup passed")
        finally:
            stopped.set()
            thread.join(timeout=2)


if __name__ == "__main__":
    main()
