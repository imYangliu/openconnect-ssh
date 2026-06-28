use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

fn write_executable(path: &Path, body: &str) {
    fs::write(path, body).unwrap();
    fs::set_permissions(path, fs::Permissions::from_mode(0o755)).unwrap();
}

fn fake_bin_dir(dir: &Path) -> PathBuf {
    let bin = dir.join("bin");
    fs::create_dir(&bin).unwrap();
    write_executable(
        &bin.join("ip"),
        "#!/bin/bash\nif [[ \"$1 $2 $3\" == \"route get\"* ]]; then echo '1.2.3.4 via 10.0.0.1 dev tun0'; else echo 'default via 10.0.0.1 dev eth0'; fi\n",
    );
    write_executable(&bin.join("sudo"), "#!/bin/bash\nexit 0\n");
    write_executable(&bin.join("openconnect"), "#!/bin/bash\nexit 0\n");
    bin
}

fn write_config(path: &Path) {
    fs::write(
        path,
        r#"
[vpn]
host = "vpn.example.com"
user = "alice"

[ssh]
host = "och-target"
target_host = "1.2.3.4"
user = "alice"
port = "22"
"#,
    )
    .unwrap();
}

fn write_vpn_only_config(path: &Path) {
    fs::write(
        path,
        r#"
[vpn]
host = "vpn.example.com"
user = "alice"

[ssh]
host = "och-target"
target_host = ""
user = "alice"
port = "22"
"#,
    )
    .unwrap();
}

#[test]
fn proxy_command_reports_missing_vpn_config() {
    let temp = tempfile::tempdir().unwrap();
    let bin = fake_bin_dir(temp.path());
    write_executable(&bin.join("nc"), "#!/bin/bash\nexit 1\n");

    let output = Command::new(env!("CARGO_BIN_EXE_och"))
        .arg("proxy-command")
        .arg("127.0.0.1")
        .arg("22")
        .env("PATH", &bin)
        .env("OS_NAME", "Linux")
        .env("OCH_CONFIG_FILE", temp.path().join("missing.toml"))
        .env("OCH_SECRETS_FILE", temp.path().join("missing-secrets.env"))
        .output()
        .unwrap();

    assert!(!output.status.success());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("未设置 [vpn].host"), "{stderr}");
}

#[test]
fn proxy_command_execs_nc_when_verify_succeeds() {
    let temp = tempfile::tempdir().unwrap();
    let bin = fake_bin_dir(temp.path());
    let nc_log = temp.path().join("nc.log");
    write_executable(
        &bin.join("nc"),
        &format!(
            "#!/bin/bash\nprintf '%s\\n' \"$*\" > '{}'\nexit 0\n",
            nc_log.display()
        ),
    );
    let config = temp.path().join("config.toml");
    write_config(&config);

    let output = Command::new(env!("CARGO_BIN_EXE_och"))
        .arg("proxy-command")
        .arg("1.2.3.4")
        .arg("22")
        .env("PATH", &bin)
        .env("OS_NAME", "Linux")
        .env("OCH_CONFIG_FILE", &config)
        .env("OCH_SECRETS_FILE", temp.path().join("missing-secrets.env"))
        .output()
        .unwrap();

    assert!(
        output.status.success(),
        "stderr={}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(fs::read_to_string(nc_log).unwrap().trim(), "1.2.3.4 22");
}

#[test]
fn vpn_status_accepts_vpn_only_config() {
    let temp = tempfile::tempdir().unwrap();
    let bin = fake_bin_dir(temp.path());
    let config = temp.path().join("config.toml");
    write_vpn_only_config(&config);

    let output = Command::new(env!("CARGO_BIN_EXE_och"))
        .arg("vpn")
        .arg("status")
        .env("PATH", &bin)
        .env("OS_NAME", "Linux")
        .env("OCH_CONFIG_FILE", &config)
        .env("OCH_APP_LANGUAGE", "zh-Hans")
        .env("OCH_SECRETS_FILE", temp.path().join("missing-secrets.env"))
        .env("PID_FILE", temp.path().join("missing.pid"))
        .output()
        .unwrap();

    assert!(
        output.status.success(),
        "stderr={}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("VPN 未连接"), "{stdout}");
    assert!(stdout.contains("未配置 [ssh].target_host"), "{stdout}");
}

#[test]
fn vpn_status_uses_requested_app_language() {
    let temp = tempfile::tempdir().unwrap();
    let bin = fake_bin_dir(temp.path());
    let config = temp.path().join("config.toml");
    write_vpn_only_config(&config);

    let output = Command::new(env!("CARGO_BIN_EXE_och"))
        .arg("vpn")
        .arg("status")
        .env("PATH", &bin)
        .env("OS_NAME", "Linux")
        .env("OCH_CONFIG_FILE", &config)
        .env("OCH_APP_LANGUAGE", "en")
        .env("OCH_SECRETS_FILE", temp.path().join("missing-secrets.env"))
        .env("PID_FILE", temp.path().join("missing.pid"))
        .output()
        .unwrap();

    assert!(
        output.status.success(),
        "stderr={}",
        String::from_utf8_lossy(&output.stderr)
    );
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("VPN disconnected"), "{stdout}");
    assert!(
        stdout.contains("[ssh].target_host is not configured"),
        "{stdout}"
    );
    assert!(!stdout.contains("VPN 未连接"), "{stdout}");
}

#[test]
fn vpn_verify_still_requires_target_host() {
    let temp = tempfile::tempdir().unwrap();
    let bin = fake_bin_dir(temp.path());
    let config = temp.path().join("config.toml");
    write_vpn_only_config(&config);

    let output = Command::new(env!("CARGO_BIN_EXE_och"))
        .arg("vpn")
        .arg("verify")
        .env("PATH", &bin)
        .env("OS_NAME", "Linux")
        .env("OCH_CONFIG_FILE", &config)
        .env("OCH_SECRETS_FILE", temp.path().join("missing-secrets.env"))
        .output()
        .unwrap();

    assert!(!output.status.success());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("未设置 [ssh].target_host"), "{stderr}");
}
