use crate::config::{load_secret_password, parse_config_file, OchConfig};
use crate::platform::{find_tool, is_macos};
use crate::service;
use crate::setup;
use std::fs;
use std::io::{self, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::Command;

pub(crate) const FAILURE_EXIT: &str = "doctor checks failed";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Level {
    Pass,
    Warn,
    Fail,
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct Check {
    level: Level,
    name: String,
    detail: String,
}

impl Check {
    fn pass(name: impl Into<String>, detail: impl Into<String>) -> Self {
        Self {
            level: Level::Pass,
            name: name.into(),
            detail: detail.into(),
        }
    }

    fn warn(name: impl Into<String>, detail: impl Into<String>) -> Self {
        Self {
            level: Level::Warn,
            name: name.into(),
            detail: detail.into(),
        }
    }

    fn fail(name: impl Into<String>, detail: impl Into<String>) -> Self {
        Self {
            level: Level::Fail,
            name: name.into(),
            detail: detail.into(),
        }
    }
}

pub(crate) fn run() -> Result<(), String> {
    let checks = collect_checks();
    println!("OCH doctor");
    for check in &checks {
        println!(
            "{} {:<24} {}",
            level_label(check.level),
            check.name,
            check.detail
        );
    }

    let failures = checks
        .iter()
        .filter(|check| check.level == Level::Fail)
        .count();
    let warnings = checks
        .iter()
        .filter(|check| check.level == Level::Warn)
        .count();
    println!();
    println!(
        "Summary: {} passed, {} warning(s), {} failure(s)",
        checks.len().saturating_sub(warnings + failures),
        warnings,
        failures
    );

    if failures > 0 {
        let _ = io::stdout().flush();
        Err(FAILURE_EXIT.to_string())
    } else {
        Ok(())
    }
}

fn collect_checks() -> Vec<Check> {
    let mut checks = Vec::new();
    let paths = match setup::setup_paths() {
        Ok(paths) => paths,
        Err(error) => {
            return vec![Check::fail("paths", error)];
        }
    };
    let os_name = std::env::var("OS_NAME").unwrap_or_else(|_| std::env::consts::OS.to_string());

    checks.extend(check_tools(&os_name));
    checks.extend(check_config(&paths.config_file));
    let config = parse_config_file(&paths.config_file, false).ok();
    checks.extend(check_secrets(&paths.secrets_file));
    checks.extend(check_config_values(config.as_ref()));
    checks.extend(check_ssh(config.as_ref(), &paths));
    checks.extend(check_service(&os_name));
    checks
}

fn check_tools(os_name: &str) -> Vec<Check> {
    let mut checks = Vec::new();
    for tool in ["ssh", "openconnect", "sudo", "nc"] {
        checks.push(check_tool(tool));
    }
    if is_macos(os_name) {
        checks.push(check_tool("route"));
    } else {
        checks.push(check_tool("ip"));
    }
    checks
}

fn check_tool(tool: &str) -> Check {
    match find_tool(tool) {
        Some(path) => Check::pass(format!("tool:{tool}"), path.display().to_string()),
        None => Check::fail(format!("tool:{tool}"), "not found in PATH"),
    }
}

fn check_config(path: &Path) -> Vec<Check> {
    if !path.exists() {
        return vec![Check::fail(
            "config.toml",
            format!("missing: {}; run `och setup`", path.display()),
        )];
    }
    match parse_config_file(&path.to_path_buf(), false) {
        Ok(_) => vec![Check::pass("config.toml", path.display().to_string())],
        Err(error) => vec![Check::fail("config.toml", error.to_string())],
    }
}

fn check_secrets(path: &Path) -> Vec<Check> {
    if !path.exists() {
        return vec![Check::warn(
            "secrets.env",
            format!(
                "missing: {}; VPN_PASSWORD env can still be used",
                path.display()
            ),
        )];
    }
    let mut checks = Vec::new();
    match fs::metadata(path) {
        Ok(metadata) => {
            let mode = metadata.permissions().mode() & 0o777;
            if mode == 0o600 {
                checks.push(Check::pass("secrets mode", "0600"));
            } else {
                checks.push(Check::fail(
                    "secrets mode",
                    format!("{mode:o}; expected 600"),
                ));
            }
        }
        Err(error) => checks.push(Check::fail("secrets metadata", error.to_string())),
    }
    match load_secret_password(&path.to_path_buf()) {
        Ok(Some(_)) => checks.push(Check::pass("VPN_PASSWORD", "present in secrets.env")),
        Ok(None) => checks.push(Check::warn("VPN_PASSWORD", "not set in secrets.env")),
        Err(error) => checks.push(Check::fail("VPN_PASSWORD", error.to_string())),
    }
    checks
}

fn check_config_values(config: Option<&OchConfig>) -> Vec<Check> {
    let Some(config) = config else {
        return vec![Check::warn(
            "config values",
            "skipped because config.toml could not be parsed",
        )];
    };
    let mut checks = Vec::new();
    if config.vpn_host.trim().is_empty() {
        checks.push(Check::fail("[vpn].host", "missing"));
    } else {
        checks.push(Check::pass("[vpn].host", &config.vpn_host));
    }
    if config.vpn_user.trim().is_empty() {
        checks.push(Check::fail("[vpn].user", "missing"));
    } else {
        checks.push(Check::pass("[vpn].user", &config.vpn_user));
    }
    match config.routes_mode.as_str() {
        "openconnect" => checks.push(Check::pass("[routes].mode", "openconnect")),
        "extra" => {
            if config.routes_extra.is_empty() {
                checks.push(Check::warn("[routes].extra", "mode=extra but no routes"));
            } else {
                checks.push(Check::pass(
                    "[routes].extra",
                    format!("{} route(s)", config.routes_extra.len()),
                ));
            }
            for route in &config.routes_extra {
                if !setup::valid_cidr(route) {
                    checks.push(Check::fail(
                        "[routes].extra",
                        format!("invalid CIDR: {route}"),
                    ));
                }
            }
        }
        other => checks.push(Check::fail("[routes].mode", format!("invalid: {other}"))),
    }
    if config.proxy_enabled {
        checks.push(check_port("[proxy].local_port", &config.proxy_local_port));
        checks.push(check_port("[proxy].remote_port", &config.proxy_remote_port));
    }
    checks
}

fn check_ssh(config: Option<&OchConfig>, paths: &setup::SetupPaths) -> Vec<Check> {
    let Some(config) = config else {
        return Vec::new();
    };
    let mut checks = Vec::new();
    if !setup::has_managed_ssh_config(config) {
        checks.push(Check::warn(
            "managed SSH",
            "disabled or incomplete; VPN-only setup is valid",
        ));
        return checks;
    }

    checks.push(Check::pass("managed SSH", &config.ssh_host));
    checks.push(check_port("[ssh].port", &config.target_port));

    if main_config_includes_managed(&paths.main_ssh_config) {
        checks.push(Check::pass("SSH Include", "Include ~/.ssh/och.config"));
    } else {
        checks.push(Check::warn(
            "SSH Include",
            format!("missing in {}", paths.main_ssh_config.display()),
        ));
    }

    if paths.managed_ssh_config.exists() {
        checks.push(Check::pass(
            "managed file",
            paths.managed_ssh_config.display().to_string(),
        ));
    } else {
        checks.push(Check::warn(
            "managed file",
            format!("missing: {}", paths.managed_ssh_config.display()),
        ));
    }

    checks.push(validate_generated_ssh_config(config, paths));
    checks
}

fn check_service(os_name: &str) -> Vec<Check> {
    if !is_macos(os_name) {
        return vec![Check::warn(
            "service",
            "macOS service mode unavailable on this OS",
        )];
    }
    if service::service_socket_exists() {
        vec![Check::pass(
            "service socket",
            service::socket_path().display().to_string(),
        )]
    } else {
        vec![Check::warn(
            "service socket",
            format!("not found: {}", service::socket_path().display()),
        )]
    }
}

fn check_port(name: &str, value: &str) -> Check {
    match setup::validate_port(value) {
        Ok(()) => Check::pass(name, value),
        Err(error) => Check::fail(name, error),
    }
}

fn validate_generated_ssh_config(config: &OchConfig, paths: &setup::SetupPaths) -> Check {
    let dir = std::env::temp_dir().join(format!("och-doctor-{}", std::process::id()));
    if let Err(error) = fs::create_dir_all(&dir) {
        return Check::fail("SSH validation", error.to_string());
    }
    let temp_config = dir.join("config");
    let write_result = fs::write(
        &temp_config,
        setup::render_managed_ssh_config(config, &paths.och_bin),
    )
    .and_then(|_| fs::set_permissions(&temp_config, fs::Permissions::from_mode(0o600)));
    if let Err(error) = write_result {
        let _ = fs::remove_dir_all(&dir);
        return Check::fail("SSH validation", error.to_string());
    }
    let output = Command::new("ssh")
        .arg("-F")
        .arg(&temp_config)
        .arg("-G")
        .arg(&config.ssh_host)
        .output();
    let _ = fs::remove_dir_all(&dir);
    match output {
        Ok(output) if output.status.success() => Check::pass("SSH validation", "ssh -G ok"),
        Ok(output) => Check::fail(
            "SSH validation",
            String::from_utf8_lossy(&output.stderr).trim().to_string(),
        ),
        Err(error) => Check::fail("SSH validation", error.to_string()),
    }
}

fn main_config_includes_managed(path: &Path) -> bool {
    fs::read_to_string(path)
        .map(|contents| {
            contents
                .lines()
                .any(|line| line.trim() == "Include ~/.ssh/och.config")
        })
        .unwrap_or(false)
}

fn level_label(level: Level) -> &'static str {
    match level {
        Level::Pass => "[PASS]",
        Level::Warn => "[WARN]",
        Level::Fail => "[FAIL]",
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn reports_missing_config_as_failure() {
        let dir = tempfile::tempdir().unwrap();
        let checks = check_config(&dir.path().join("missing.toml"));
        assert_eq!(checks[0].level, Level::Fail);
        assert!(checks[0].detail.contains("och setup"));
    }

    #[test]
    fn accepts_vpn_only_ssh_as_warning() {
        let config = OchConfig {
            vpn_host: "vpn.example.com".to_string(),
            vpn_user: "alice".to_string(),
            ..OchConfig::default()
        };
        let dir = tempfile::tempdir().unwrap();
        let paths = setup::SetupPaths {
            config_file: dir.path().join("config.toml"),
            secrets_file: dir.path().join("secrets.env"),
            managed_ssh_config: dir.path().join("och.config"),
            main_ssh_config: dir.path().join("ssh-config"),
            och_bin: dir.path().join("och"),
        };
        let checks = check_ssh(Some(&config), &paths);
        assert_eq!(checks[0].level, Level::Warn);
        assert!(checks[0].detail.contains("VPN-only"));
    }

    #[test]
    fn detects_secret_permissions() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("secrets.env");
        fs::write(&path, "VPN_PASSWORD=\"secret\"\n").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
        let checks = check_secrets(&path);
        assert!(checks.iter().any(|check| check.level == Level::Fail));
    }
}
