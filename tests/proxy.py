"""Exercise the compiled ProxyCommand over a real TCP stream, without sudo or a VPN."""
import os
import pathlib
import socket
import subprocess
import tempfile
import threading

binary = pathlib.Path("build/bin/och").resolve()
for needs_vpn in (False, True):
    with tempfile.TemporaryDirectory() as work, socket.socket() as server:
        root = pathlib.Path(work)
        server.bind(("127.0.0.1", 0))
        port = server.getsockname()[1]
        if not needs_vpn:
            server.listen()
        (root / "config.toml").write_text('[vpn]\nhost="vpn.example"\nuser="user"\n')
        # A pipe signals the test server only after the simulated VPN starts.
        os.mkfifo(root / "ready")
        for name, script in {
            "sudo": 'shift; exec "$@"',
            "openconnect": 'read -r pw; test "$pw" = fixture || exit 1; echo up > "$HOME/ready"',
        }.items():
            path = root / name
            path.write_text("#!/bin/sh\n" + script)
            path.chmod(0o700)
        def serve():
            if needs_vpn:
                with (root / "ready").open() as ready:
                    ready.read()
                server.listen()
            conn, _ = server.accept()
            with conn, conn.makefile("rb") as stream:
                conn.sendall(b"SSH-2.0-test\r\n" + stream.read())
        threading.Thread(target=serve, daemon=True).start()
        env = dict({k:v for k,v in os.environ.items() if k not in ("LOG_FILE","PID_FILE","SUDO_ASKPASS")}, HOME=work, VPN_PASSWORD="fixture", OCH_CONFIG_FILE=str(root/"config.toml"),
                   PATH=work+os.pathsep+os.environ["PATH"])
        result = subprocess.run([binary, "proxy-command", "127.0.0.1", str(port)],
                                input=b"payload\x00\xff", capture_output=True, env=env, timeout=12)
        assert result.returncode == 0, result.stderr
        assert result.stdout == b"SSH-2.0-test\r\npayload\x00\xff", result.stdout
        assert (root/".config/och/run/openconnect.log").exists() == needs_vpn
print("ProxyCommand direct and on-demand streams passed")
