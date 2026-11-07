"""Drive the compiled password prompt over a PTY and private fake IPC server."""
import json
import os
import pathlib
import pty
import select
import signal
import socket
import subprocess
import tempfile
import termios
import threading
import time

binary = pathlib.Path("build/bin/och").resolve()
for cancel in (False, True):
    with tempfile.TemporaryDirectory(prefix="och-password-", dir="/tmp") as work:
        root = pathlib.Path(work)
        address = root / "och.sock"
        config = root / "config.yaml"
        config.write_text(f'socket: "{address}"\nprofiles:\n  campus:\n    protocol: anyconnect\n    server: https://vpn.invalid\n')
        observed = []
        with socket.socket(socket.AF_UNIX) as listener:
            listener.bind(str(address))
            listener.listen()
            listener.settimeout(5)

            def serve():
                try:
                    conn, _ = listener.accept()
                    with conn, conn.makefile("rb") as stream:
                        request = json.loads(stream.readline())
                        assert request["action"] == "login"
                        conn.sendall(b'{"kind":"password"}\n')
                        line = stream.readline()
                        if line:
                            observed.append(json.loads(line).get("password"))
                            conn.sendall(b'{"authenticated":true}\n')
                except (OSError, ValueError):
                    pass

            thread = threading.Thread(target=serve, daemon=True)
            thread.start()
            master, slave = pty.openpty()
            original = termios.tcgetattr(slave)
            proc = subprocess.Popen([binary, "login", "--config", config, "--via", "campus"],
                                    stdin=slave, stdout=slave, stderr=slave,
                                    env={"HOME": work, "PATH": os.environ["PATH"]},
                                    start_new_session=True)
            output = bytearray()
            try:
                deadline = time.monotonic() + 5
                while b"VPN password:" not in output and time.monotonic() < deadline:
                    ready, _, _ = select.select([master], [], [], 0.1)
                    if ready:
                        output.extend(os.read(master, 4096))
                assert b"VPN password:" in output, "password prompt did not appear"
                # Wait for echo to be disabled before supplying the fixture.
                while termios.tcgetattr(slave)[3] & termios.ECHO and time.monotonic() < deadline:
                    time.sleep(0.01)
                assert not termios.tcgetattr(slave)[3] & termios.ECHO
                if cancel:
                    proc.send_signal(signal.SIGINT)
                else:
                    os.write(master, "private-fixture-é".encode() + b"\x7fz\r")
                exit_code = proc.wait(timeout=5)
                assert exit_code == (1 if cancel else 0), "unexpected prompt exit"
                restored = termios.tcgetattr(slave)
                # macOS may set PENDIN itself when restoring canonical input.
                dynamic_pending = getattr(termios, "PENDIN", 0)
                restored[3] &= ~dynamic_pending
                original[3] &= ~dynamic_pending
                assert restored == original, "terminal modes were not restored"
                while select.select([master], [], [], 0)[0]:
                    try:
                        output.extend(os.read(master, 4096))
                    except OSError:
                        break
                assert b"private-fixture" not in output, "password was echoed"
                thread.join(timeout=2)
                if not cancel:
                    assert observed == ["private-fixture-z"], "Unicode backspace changed the credential"
                else:
                    assert not observed, "canceled prompt submitted a credential"
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.wait()
                os.close(master)
                os.close(slave)
print("PTY password entry, Unicode backspace, Ctrl-C cancellation and terminal restoration passed")
