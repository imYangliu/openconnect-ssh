use serde::Deserialize;
use std::collections::BTreeSet;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("cannot read config file {path}: {source}")]
    ReadConfig {
        path: String,
        source: std::io::Error,
    },
    #[error("invalid TOML in {path}: {source}")]
    Toml {
        path: String,
        source: toml::de::Error,
    },
    #[error("unknown config section: {0}")]
    UnknownSection(String),
    #[error("[paths] is fixed by the installed runtime layout; remove {0}")]
    PathsKey(String),
    #[error("unknown config key: {0}")]
    UnknownKey(String),
    #[error("invalid app.language: {0}")]
    InvalidLanguage(String),
    #[error("missing required config value: {0}")]
    MissingRequired(&'static str),
    #[error("cannot read secrets file {path}: {source}")]
    ReadSecrets {
        path: String,
        source: std::io::Error,
    },
    #[error("secrets file must have 0600 permissions: {0}")]
    SecretMode(String),
    #[error("invalid secrets line {line}: {content}")]
    InvalidSecretLine { line: usize, content: String },
    #[error("unsupported secret key at line {line}: {key}")]
    UnsupportedSecretKey { line: usize, key: String },
}

#[derive(Debug, Clone, Default)]
pub struct Runtime {
    pub config_file: PathBuf,
    pub keychain_service: String,
    pub pid_file: PathBuf,
    pub log_file: PathBuf,
    pub sudo_askpass: Option<String>,
    pub os_name: String,
    pub config: OchConfig,
    pub vpn_password: Option<String>,
    pub target: RuntimeTarget,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct RuntimeTarget {
    pub host: Option<String>,
    pub port: Option<String>,
    pub user: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OchConfig {
    pub vpn_host: String,
    pub vpn_user: String,
    pub vpn_auth_group: String,
    pub ssh_host: String,
    pub target_host: String,
    pub target_user: String,
    pub target_port: String,
    pub routes_extra: Vec<String>,
    pub proxy_local_host: String,
    pub proxy_local_port: String,
    pub proxy_remote_port: String,
    pub app_language: String,
}

impl Default for OchConfig {
    fn default() -> Self {
        Self {
            vpn_host: String::new(),
            vpn_user: String::new(),
            vpn_auth_group: String::new(),
            ssh_host: String::new(),
            target_host: String::new(),
            target_user: std::env::var("USER").unwrap_or_default(),
            target_port: "22".to_string(),
            routes_extra: Vec::new(),
            proxy_local_host: "127.0.0.1".to_string(),
            proxy_local_port: "7890".to_string(),
            proxy_remote_port: "7890".to_string(),
            app_language: "system".to_string(),
        }
    }
}

#[derive(Debug, Deserialize)]
struct TomlConfig {
    vpn: Option<VpnSection>,
    ssh: Option<SshSection>,
    routes: Option<RoutesSection>,
    proxy: Option<ProxySection>,
    app: Option<AppSection>,
    _paths: Option<toml::Table>,
}

#[derive(Debug, Deserialize)]
struct VpnSection {
    host: Option<String>,
    user: Option<String>,
    auth_group: Option<String>,
}

#[derive(Debug, Deserialize)]
struct SshSection {
    host: Option<String>,
    target_host: Option<String>,
    user: Option<String>,
    port: Option<String>,
}

#[derive(Debug, Deserialize)]
struct RoutesSection {
    extra: Option<Vec<String>>,
}

#[derive(Debug, Deserialize)]
struct ProxySection {
    local_host: Option<String>,
    local_port: Option<String>,
    remote_port: Option<String>,
}

#[derive(Debug, Deserialize)]
struct AppSection {
    language: Option<String>,
}

pub fn load_runtime(validate_required: bool) -> Result<Runtime, ConfigError> {
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
    let user = std::env::var("USER").unwrap_or_else(|_| "user".to_string());
    let config_file = std::env::var("OCH_CONFIG_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".config/och/config.toml"));
    let secrets_file = std::env::var("OCH_SECRETS_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".config/och/secrets.env"));
    let keychain_service =
        std::env::var("OCH_KEYCHAIN_SERVICE").unwrap_or_else(|_| "och".to_string());
    let pid_file = std::env::var("PID_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(format!("/tmp/och-openconnect-{user}.pid")));
    let log_file = std::env::var("LOG_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(format!("/tmp/och-openconnect-{user}.log")));
    let sudo_askpass = non_empty_env("SUDO_ASKPASS");
    let os_name = std::env::var("OS_NAME").unwrap_or_else(|_| std::env::consts::OS.to_string());

    let mut config = OchConfig::default();
    if config_file.is_file() {
        config = parse_config_file(&config_file, validate_required)?;
    }

    let vpn_password = match non_empty_env("VPN_PASSWORD") {
        Some(value) => Some(value),
        None => load_secret_password(&secrets_file)?,
    };

    let target = RuntimeTarget {
        host: non_empty_env("OCH_RUNTIME_TARGET_HOST"),
        port: non_empty_env("OCH_RUNTIME_TARGET_PORT"),
        user: non_empty_env("OCH_RUNTIME_TARGET_USER"),
    };

    Ok(Runtime {
        config_file,
        keychain_service,
        pid_file,
        log_file,
        sudo_askpass,
        os_name,
        config,
        vpn_password,
        target,
    })
}

pub fn parse_config_file(
    path: &PathBuf,
    validate_required: bool,
) -> Result<OchConfig, ConfigError> {
    let raw = fs::read_to_string(path).map_err(|source| ConfigError::ReadConfig {
        path: path.display().to_string(),
        source,
    })?;
    parse_config_str(&raw, validate_required, &path.display().to_string())
}

pub fn parse_config_str(
    raw: &str,
    validate_required: bool,
    path_label: &str,
) -> Result<OchConfig, ConfigError> {
    let value = toml::from_str::<toml::Value>(raw).map_err(|source| ConfigError::Toml {
        path: path_label.to_string(),
        source,
    })?;
    validate_keys(&value)?;

    let parsed: TomlConfig = value.try_into().map_err(|source| ConfigError::Toml {
        path: path_label.to_string(),
        source,
    })?;

    let mut config = OchConfig::default();
    if let Some(vpn) = parsed.vpn {
        config.vpn_host = vpn.host.unwrap_or_default();
        config.vpn_user = vpn.user.unwrap_or_default();
        config.vpn_auth_group = vpn.auth_group.unwrap_or_default();
    }
    if let Some(ssh) = parsed.ssh {
        config.ssh_host = ssh.host.unwrap_or_default();
        config.target_host = ssh.target_host.unwrap_or_default();
        config.target_user = ssh
            .user
            .unwrap_or_else(|| std::env::var("USER").unwrap_or_default());
        config.target_port = ssh.port.unwrap_or_else(|| "22".to_string());
    }
    if let Some(routes) = parsed.routes {
        config.routes_extra = routes.extra.unwrap_or_default();
    }
    if let Some(proxy) = parsed.proxy {
        config.proxy_local_host = proxy.local_host.unwrap_or_else(|| "127.0.0.1".to_string());
        config.proxy_local_port = proxy.local_port.unwrap_or_else(|| "7890".to_string());
        config.proxy_remote_port = proxy.remote_port.unwrap_or_else(|| "7890".to_string());
    }
    if let Some(app) = parsed.app {
        if let Some(language) = app.language {
            match language.as_str() {
                "system" | "en" | "zh-Hans" => config.app_language = language,
                _ => return Err(ConfigError::InvalidLanguage(language)),
            }
        }
    }

    if validate_required {
        validate_required_config(&config)?;
    }

    Ok(config)
}

fn validate_required_config(config: &OchConfig) -> Result<(), ConfigError> {
    for (name, value) in [
        ("OCH_VPN_HOST", &config.vpn_host),
        ("OCH_VPN_USER", &config.vpn_user),
    ] {
        if value.is_empty() {
            return Err(ConfigError::MissingRequired(name));
        }
    }
    Ok(())
}

fn validate_keys(value: &toml::Value) -> Result<(), ConfigError> {
    let Some(table) = value.as_table() else {
        return Ok(());
    };
    let allowed_sections = BTreeSet::from(["vpn", "ssh", "routes", "proxy", "app", "paths"]);
    for (section, section_value) in table {
        if !allowed_sections.contains(section.as_str()) {
            return Err(ConfigError::UnknownSection(section.clone()));
        }
        let keys = section_value.as_table().cloned().unwrap_or_default();
        let allowed_keys = match section.as_str() {
            "vpn" => BTreeSet::from(["host", "user", "auth_group"]),
            "ssh" => BTreeSet::from(["host", "target_host", "user", "port"]),
            "routes" => BTreeSet::from(["extra"]),
            "proxy" => BTreeSet::from(["local_host", "local_port", "remote_port"]),
            "app" => BTreeSet::from(["language"]),
            "paths" => BTreeSet::new(),
            _ => unreachable!(),
        };
        for key in keys.keys() {
            if section == "paths" {
                return Err(ConfigError::PathsKey(key.clone()));
            }
            if !allowed_keys.contains(key.as_str()) {
                return Err(ConfigError::UnknownKey(format!("{section}.{key}")));
            }
        }
    }
    Ok(())
}

pub fn load_secret_password(path: &PathBuf) -> Result<Option<String>, ConfigError> {
    if !path.exists() {
        return Ok(None);
    }
    let metadata = fs::metadata(path).map_err(|source| ConfigError::ReadSecrets {
        path: path.display().to_string(),
        source,
    })?;
    let mode = metadata.permissions().mode() & 0o777;
    if mode != 0o600 {
        return Err(ConfigError::SecretMode(path.display().to_string()));
    }
    let raw = fs::read_to_string(path).map_err(|source| ConfigError::ReadSecrets {
        path: path.display().to_string(),
        source,
    })?;

    let mut password = None;
    for (index, raw_line) in raw.lines().enumerate() {
        let line_number = index + 1;
        let line = strip_comment(raw_line).trim().to_string();
        if line.is_empty() {
            continue;
        }
        let Some((key, value)) = line.split_once('=') else {
            return Err(ConfigError::InvalidSecretLine {
                line: line_number,
                content: line,
            });
        };
        let key = key.trim();
        if key != "VPN_PASSWORD" {
            return Err(ConfigError::UnsupportedSecretKey {
                line: line_number,
                key: key.to_string(),
            });
        }
        password = Some(unquote(value.trim()));
    }
    Ok(password)
}

fn non_empty_env(name: &str) -> Option<String> {
    std::env::var(name).ok().filter(|value| !value.is_empty())
}

fn strip_comment(line: &str) -> String {
    let mut result = String::new();
    let mut in_single = false;
    let mut in_double = false;
    let mut escaping = false;
    for ch in line.chars() {
        if escaping {
            result.push(ch);
            escaping = false;
            continue;
        }
        if ch == '\\' {
            result.push(ch);
            if in_double {
                escaping = true;
            }
            continue;
        }
        if ch == '"' && !in_single {
            in_double = !in_double;
        } else if ch == '\'' && !in_double {
            in_single = !in_single;
        } else if ch == '#' && !in_single && !in_double {
            break;
        }
        result.push(ch);
    }
    result
}

fn unquote(value: &str) -> String {
    if value.len() >= 2 && value.starts_with('"') && value.ends_with('"') {
        value[1..value.len() - 1]
            .replace("\\\"", "\"")
            .replace("\\\\", "\\")
    } else if value.len() >= 2 && value.starts_with('\'') && value.ends_with('\'') {
        value[1..value.len() - 1].to_string()
    } else {
        value.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::{load_secret_password, parse_config_str};
    use std::fs;
    use std::os::unix::fs::PermissionsExt;

    #[test]
    fn parses_current_toml_shape() {
        let config = parse_config_str(
            r#"
[vpn]
host = "vpn.example.com"
user = "alice"
auth_group = "staff"

[ssh]
host = "och-target"
target_host = "10.0.0.10"
user = "deploy"
port = "2222"

[routes]
extra = ["10.0.0.0/8", "192.168.0.0/16"]

[proxy]
local_host = "127.0.0.1"
local_port = "7897"
remote_port = "7890"

[app]
language = "zh-Hans"
"#,
            true,
            "test",
        )
        .unwrap();

        assert_eq!(config.vpn_host, "vpn.example.com");
        assert_eq!(config.ssh_host, "och-target");
        assert_eq!(config.routes_extra, ["10.0.0.0/8", "192.168.0.0/16"]);
        assert_eq!(config.proxy_local_port, "7897");
        assert_eq!(config.app_language, "zh-Hans");
    }

    #[test]
    fn rejects_paths_keys() {
        let error = parse_config_str("[paths]\noch = \"/tmp/och\"\n", false, "test")
            .unwrap_err()
            .to_string();
        assert!(error.contains("[paths] is fixed"));
    }

    #[test]
    fn rejects_unknown_keys() {
        let error = parse_config_str("[vpn]\nextra = true\n", false, "test")
            .unwrap_err()
            .to_string();
        assert!(error.contains("unknown config key: vpn.extra"));
    }

    #[test]
    fn parses_secret_password_with_comments() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("secrets.env");
        fs::write(&path, "VPN_PASSWORD=\"abc#123\" # comment\n").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();

        assert_eq!(
            load_secret_password(&path).unwrap(),
            Some("abc#123".to_string())
        );
    }

    #[test]
    fn rejects_secret_mode() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("secrets.env");
        fs::write(&path, "VPN_PASSWORD=secret\n").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();

        let error = load_secret_password(&path).unwrap_err().to_string();
        assert!(error.contains("0600"));
    }
}
