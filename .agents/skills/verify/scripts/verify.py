#!/usr/bin/env python3
"""Run OCH's isolated verification and retain evidence outside scratch HOME."""
import datetime
import hashlib
import json
import os
import pathlib
import platform
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[4]


def main():
    evidence = pathlib.Path(tempfile.mkdtemp(prefix="och-evidence-"))
    print(f"Evidence: {evidence}", flush=True)
    report = {"started_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "platform": platform.platform(), "commands": [], "passed": False}
    # Preserve only build-tool locations, never application configuration or credentials.
    env = {"PATH": os.environ["PATH"], "LANG": "en_US.UTF-8", "USER": "fixture"}
    for key in ("GOCACHE", "GOPATH"):
        env[key] = subprocess.check_output(["go", "env", key], text=True).strip()

    def run(feature, argv):
        number = len(report["commands"]) + 1
        entry = {"feature": feature, "argv": argv}
        report["commands"].append(entry)
        with (evidence / f"{number}.stdout.txt").open("w") as stdout, \
                (evidence / f"{number}.stderr.txt").open("w") as stderr:
            result = subprocess.run(argv, cwd=ROOT, env=env, stdin=subprocess.DEVNULL,
                                    stdout=stdout, stderr=stderr, timeout=600)
        entry["exit_code"] = result.returncode
        print(f"{feature}: exit {result.returncode}", flush=True)
        result.check_returncode()
        return (evidence / f"{number}.stdout.txt").read_text()

    scratch = None
    try:
        with tempfile.TemporaryDirectory(prefix="och-verify-") as scratch:
            env["HOME"] = scratch
            env["TMPDIR"] = scratch
            run("launch", ["make", "build"])
            binary = ROOT / "build/bin/och"
            report["binary_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
            help_text = run("doctor", [str(binary), "--help"])
            if "och proxy-command --config FILE" not in help_text:
                raise RuntimeError("Built binary does not expose the expected CLI")
            run("native-cli-journey", ["python3", "tests/proxy.py"])
            run("all-existing-checks", ["make", "check"])
            if (pathlib.Path(scratch) / ".ssh").exists():
                raise RuntimeError("Verification unexpectedly created SSH configuration")
            report["scratch_ssh_absent"] = True
            report["passed"] = True
    finally:
        report["scratch_removed"] = scratch is not None and not pathlib.Path(scratch).exists()
        (evidence / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"Report retained: {evidence / 'report.json'}", flush=True)


if __name__ == "__main__":
    main()
