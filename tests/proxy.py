"""Exercise the native daemon and ProxyCommand with loopback-only direct policy."""
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import threading
import time

binary = pathlib.Path("build/bin/och").resolve()
with tempfile.TemporaryDirectory(prefix="och-cli-") as work, socket.socket() as server:
    root = pathlib.Path(work)
    config = root / "config.yaml"
    config.write_text(f'socket: "{root / "och.sock"}"\npolicies:\n  local: {{}}\n')
    env = {"PATH": os.environ["PATH"], "HOME": work}
    server.bind(("127.0.0.1", 0))
    server.listen()
    def serve_echo():
        conn, _ = server.accept()
        with conn, conn.makefile("rb") as stream:
            conn.sendall(b"SSH-2.0-fixture\r\n" + stream.read())
    threading.Thread(target=serve_echo, daemon=True).start()
    with (root / "daemon.log").open("w+") as log:
        daemon = subprocess.Popen([binary, "serve", "--config", config], env=env,
                                  stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        try:
            for _ in range(100):
                if daemon.poll() is not None:
                    log.seek(0)
                    raise AssertionError(log.read())
                if (root / "och.sock").exists():
                    break
                time.sleep(0.05)
            else:
                raise AssertionError("daemon not ready")
            result = subprocess.run([binary, "proxy-command", "--config", config,
                                     "--policy", "local", "127.0.0.1", str(server.getsockname()[1])],
                                    input=b"payload\x00\xff", capture_output=True, env=env, timeout=10)
            assert result.returncode == 0, result.stderr
            assert result.stdout == b"SSH-2.0-fixture\r\npayload\x00\xff", result.stdout
            status = subprocess.run([binary, "status", "--config", config],
                                    capture_output=True, env=env, timeout=5, check=True)
            assert not json.loads(status.stdout).get("error")
            subprocess.run([binary, "stop", "--config", config], capture_output=True,
                           env=env, timeout=5, check=True)
            assert daemon.wait(timeout=5) == 0
            assert not (root / "och.sock").exists()
            assert not (root / ".ssh").exists()
        finally:
            if daemon.poll() is None:
                daemon.terminate()
                try:
                    daemon.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.wait()
print("Native daemon, ProxyCommand binary stream, status and clean stop passed")
