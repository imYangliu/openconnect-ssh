#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Language {
    English,
    ZhHans,
    ZhHant,
}

impl Language {
    pub fn resolve(raw: &str) -> Self {
        match raw {
            "en" => Self::English,
            "zh-Hant" => Self::ZhHant,
            "zh-Hans" => Self::ZhHans,
            "system" | "" => Self::from_environment(),
            value if value.starts_with("zh_TW") || value.starts_with("zh-Hant") => Self::ZhHant,
            value if value.starts_with("zh") => Self::ZhHans,
            _ => Self::English,
        }
    }

    fn from_environment() -> Self {
        for key in ["LC_ALL", "LC_MESSAGES", "LANG"] {
            if let Ok(value) = std::env::var(key) {
                if value.starts_with("zh_TW") || value.starts_with("zh-Hant") {
                    return Self::ZhHant;
                }
                if value.starts_with("zh") {
                    return Self::ZhHans;
                }
            }
        }
        Self::English
    }
}

pub fn vpn_connected_pid(language: &str, pid: &str) -> String {
    match Language::resolve(language) {
        Language::English => format!("VPN connected, PID: {pid}"),
        Language::ZhHans => format!("VPN 已连接，PID: {pid}"),
        Language::ZhHant => format!("VPN 已連線，PID: {pid}"),
    }
}

pub fn vpn_disconnected(language: &str) -> &'static str {
    match Language::resolve(language) {
        Language::English => "VPN disconnected",
        Language::ZhHans => "VPN 未连接",
        Language::ZhHant => "VPN 未連線",
    }
}

pub fn default_route_label(language: &str) -> &'static str {
    match Language::resolve(language) {
        Language::English => "Default route:",
        Language::ZhHans | Language::ZhHant => "默认路由:",
    }
}

pub fn target_route_label(language: &str) -> &'static str {
    match Language::resolve(language) {
        Language::English => "Target route:",
        Language::ZhHans | Language::ZhHant => "目标路由:",
    }
}

pub fn missing_target_route(language: &str) -> &'static str {
    match Language::resolve(language) {
        Language::English => "Target route: [ssh].target_host is not configured",
        Language::ZhHans | Language::ZhHant => "目标路由: 未配置 [ssh].target_host",
    }
}

#[cfg(test)]
mod tests {
    use super::{vpn_disconnected, Language};

    #[test]
    fn resolves_explicit_english() {
        assert_eq!(Language::resolve("en"), Language::English);
        assert_eq!(vpn_disconnected("en"), "VPN disconnected");
    }
}
