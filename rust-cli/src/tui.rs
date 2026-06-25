use crate::config::{load_secret_password, parse_config_str, OchConfig};
use crate::setup;
use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyModifiers};
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, List, ListItem, Paragraph, Wrap};
use ratatui::{Frame, Terminal};
use std::fs;
use std::io::{self, IsTerminal};
use std::path::PathBuf;
use std::process::Command;

const INCLUDE_LINE: &str = "Include ~/.ssh/och.config";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Pane {
    Overview,
    Connection,
    Ssh,
    Routes,
    Service,
    Config,
    Logs,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ConfirmAction {
    UninstallService,
    OverwriteInvalidConfig,
}

impl Pane {
    const ALL: [Pane; 7] = [
        Pane::Overview,
        Pane::Connection,
        Pane::Ssh,
        Pane::Routes,
        Pane::Service,
        Pane::Config,
        Pane::Logs,
    ];

    fn title(self) -> &'static str {
        match self {
            Pane::Overview => "Overview",
            Pane::Connection => "Connection",
            Pane::Ssh => "SSH",
            Pane::Routes => "Routes & Proxy",
            Pane::Service => "Service",
            Pane::Config => "Config",
            Pane::Logs => "Logs",
        }
    }
}

#[derive(Debug)]
struct TuiState {
    pane: Pane,
    active: usize,
    config: OchConfig,
    vpn_password: String,
    extra_routes_text: String,
    config_text: String,
    logs: String,
    status: String,
    paths: setup::SetupPaths,
    ssh_enabled: bool,
    ssh_filter: String,
    ssh_hosts: Vec<String>,
    selected_ssh: usize,
    connection_summary: String,
    service_summary: String,
    confirm: Option<ConfirmAction>,
    config_load_failed: bool,
    canceled: bool,
}

pub(crate) fn run() -> Result<(), String> {
    if !io::stdin().is_terminal() || !io::stdout().is_terminal() {
        return Err("och tui 需要交互式终端。请在真实 TTY 中运行。".into());
    }

    let mut state = TuiState::load()?;
    let mut terminal = TerminalSession::enter()?;
    while !state.canceled {
        terminal.draw(|frame| render(frame, &state))?;
        let Event::Key(key) = event::read().map_err(|error| error.to_string())? else {
            continue;
        };
        if let Err(error) = state.handle_key(key) {
            state.status = error;
        }
    }
    Ok(())
}

struct TerminalSession {
    terminal: Terminal<CrosstermBackend<io::Stdout>>,
}

impl TerminalSession {
    fn enter() -> Result<Self, String> {
        enable_raw_mode().map_err(|error| error.to_string())?;
        let mut stdout = io::stdout();
        execute!(stdout, EnterAlternateScreen).map_err(|error| error.to_string())?;
        let backend = CrosstermBackend::new(stdout);
        let terminal = Terminal::new(backend).map_err(|error| error.to_string())?;
        Ok(Self { terminal })
    }

    fn draw<F>(&mut self, f: F) -> Result<(), String>
    where
        F: FnOnce(&mut Frame),
    {
        self.terminal
            .draw(f)
            .map(|_| ())
            .map_err(|error| error.to_string())
    }
}

impl Drop for TerminalSession {
    fn drop(&mut self) {
        let _ = disable_raw_mode();
        let _ = execute!(self.terminal.backend_mut(), LeaveAlternateScreen);
        let _ = self.terminal.show_cursor();
    }
}

impl TuiState {
    fn load() -> Result<Self, String> {
        let paths = setup::setup_paths()?;
        let (mut config, warning) = setup::load_existing_config(&paths.config_file);
        let config_load_failed = warning.is_some();
        if config.ssh_host.is_empty() {
            config.ssh_host = "och-target".to_string();
        }
        if config.target_port.is_empty() {
            config.target_port = "22".to_string();
        }
        let vpn_password = load_secret_password(&paths.secrets_file)
            .map_err(|error| error.to_string())?
            .unwrap_or_default();
        let extra_routes_text = config.routes_extra.join("\n");
        let config_text = setup::render_config_toml(&config);
        let ssh_enabled = setup::has_managed_ssh_config(&config);
        let ssh_hosts = setup::list_ssh_hosts(&paths.main_ssh_config, &paths.managed_ssh_config);
        let mut state = Self {
            pane: Pane::Overview,
            active: 0,
            config,
            vpn_password,
            extra_routes_text,
            config_text,
            logs: String::new(),
            status: warning.unwrap_or_else(|| {
                "↑/↓ 切左侧页面，←/→ 或 Tab 切字段，Enter 执行，Ctrl-S 保存，Esc 退出".to_string()
            }),
            paths,
            ssh_enabled,
            ssh_filter: String::new(),
            ssh_hosts,
            selected_ssh: 0,
            connection_summary: "未知".to_string(),
            service_summary: "未刷新".to_string(),
            confirm: None,
            config_load_failed,
            canceled: false,
        };
        state.refresh_logs();
        Ok(state)
    }

    fn handle_key(&mut self, key: KeyEvent) -> Result<(), String> {
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('c') {
            self.canceled = true;
            return Ok(());
        }
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('s') {
            return self.save_settings();
        }
        if let Some(action) = self.confirm {
            return match key.code {
                KeyCode::Enter => self.run_confirmed(action),
                KeyCode::Esc => {
                    self.confirm = None;
                    self.status = "已取消确认操作".to_string();
                    Ok(())
                }
                _ => {
                    self.status = "当前有待确认操作：Enter 确认，Esc 取消".to_string();
                    Ok(())
                }
            };
        }

        match key.code {
            KeyCode::Esc => {
                self.canceled = true;
                Ok(())
            }
            KeyCode::Up => {
                self.previous_pane();
                Ok(())
            }
            KeyCode::Down => {
                self.next_pane();
                Ok(())
            }
            KeyCode::PageUp => self.previous_list_item(),
            KeyCode::PageDown => self.next_list_item(),
            KeyCode::Left => self.previous_field(),
            KeyCode::Right | KeyCode::Tab => self.next_field(),
            KeyCode::BackTab => self.previous_field(),
            KeyCode::Enter => self.enter(),
            KeyCode::Backspace => self.backspace(),
            KeyCode::Char(ch) => self.push_char(ch),
            _ => Ok(()),
        }
    }

    fn next_pane(&mut self) {
        let index = Pane::ALL
            .iter()
            .position(|pane| *pane == self.pane)
            .unwrap_or(0);
        self.pane = Pane::ALL[(index + 1) % Pane::ALL.len()];
        self.active = 0;
        self.status = self.pane_hint();
    }

    fn previous_pane(&mut self) {
        let index = Pane::ALL
            .iter()
            .position(|pane| *pane == self.pane)
            .unwrap_or(0);
        self.pane = Pane::ALL[(index + Pane::ALL.len() - 1) % Pane::ALL.len()];
        self.active = 0;
        self.status = self.pane_hint();
    }

    fn next_field(&mut self) -> Result<(), String> {
        self.active = (self.active + 1) % self.field_count();
        Ok(())
    }

    fn previous_field(&mut self) -> Result<(), String> {
        let count = self.field_count();
        self.active = if self.active == 0 {
            count - 1
        } else {
            self.active - 1
        };
        Ok(())
    }

    fn next_list_item(&mut self) -> Result<(), String> {
        if self.pane != Pane::Ssh || self.active != 1 {
            return self.next_field();
        }
        let count = self.filtered_ssh_hosts().len();
        if count > 0 {
            self.selected_ssh = (self.selected_ssh + 1).min(count - 1);
        }
        Ok(())
    }

    fn previous_list_item(&mut self) -> Result<(), String> {
        if self.pane != Pane::Ssh || self.active != 1 {
            return self.previous_field();
        }
        self.selected_ssh = self.selected_ssh.saturating_sub(1);
        Ok(())
    }

    fn field_count(&self) -> usize {
        match self.pane {
            Pane::Overview => 4,
            Pane::Connection => 7,
            Pane::Ssh => 6,
            Pane::Routes => 6,
            Pane::Service => 4,
            Pane::Config => 3,
            Pane::Logs => 2,
        }
    }

    fn enter(&mut self) -> Result<(), String> {
        match self.pane {
            Pane::Overview => match self.active {
                0 => self.run_och(["vpn", "status"]),
                1 => self.run_och(["service", "status"]),
                2 => self.install_include(),
                _ => self.save_settings(),
            },
            Pane::Connection => match self.active {
                4 => self.run_och(["vpn", "connect"]),
                5 => self.run_och(["vpn", "disconnect"]),
                6 => self.probe_auth_groups(),
                _ => Ok(()),
            },
            Pane::Ssh => match self.active {
                0 => {
                    self.ssh_enabled = !self.ssh_enabled;
                    self.status = if self.ssh_enabled {
                        "已启用 SSH；保存时会写 managed SSH config".to_string()
                    } else {
                        "已禁用 SSH；保存时不修改旧 managed SSH config".to_string()
                    };
                    Ok(())
                }
                1 => self.apply_selected_ssh_host(),
                _ => Ok(()),
            },
            Pane::Routes => match self.active {
                0 => {
                    self.config.routes_mode = if self.config.routes_mode == "extra" {
                        "openconnect".to_string()
                    } else {
                        "extra".to_string()
                    };
                    Ok(())
                }
                2 => {
                    self.config.proxy_enabled = !self.config.proxy_enabled;
                    Ok(())
                }
                _ => Ok(()),
            },
            Pane::Service => match self.active {
                0 => self.run_och(["service", "status"]),
                1 => self.run_och(["service", "install"]),
                2 => {
                    self.confirm = Some(ConfirmAction::UninstallService);
                    self.status = "确认卸载服务？再按 Enter 确认，Esc 取消".to_string();
                    Ok(())
                }
                _ => self.refresh_logs_action(),
            },
            Pane::Config => match self.active {
                0 => self.apply_config_text(),
                1 => {
                    self.sync_config_text();
                    Ok(())
                }
                _ => {
                    self.config_text.push('\n');
                    Ok(())
                }
            },
            Pane::Logs => self.refresh_logs_action(),
        }
    }

    fn push_char(&mut self, ch: char) -> Result<(), String> {
        match (self.pane, self.active) {
            (Pane::Connection, 0) => self.config.vpn_host.push(ch),
            (Pane::Connection, 1) => self.config.vpn_user.push(ch),
            (Pane::Connection, 2) => self.vpn_password.push(ch),
            (Pane::Connection, 3) => self.config.vpn_auth_group.push(ch),
            (Pane::Ssh, 1) => {
                self.ssh_filter.push(ch);
                self.selected_ssh = 0;
            }
            (Pane::Ssh, 2) => self.config.ssh_host.push(ch),
            (Pane::Ssh, 3) => self.config.target_host.push(ch),
            (Pane::Ssh, 4) => self.config.target_user.push(ch),
            (Pane::Ssh, 5) => self.config.target_port.push(ch),
            (Pane::Routes, 1) => self.extra_routes_text.push(ch),
            (Pane::Routes, 3) => self.config.proxy_local_host.push(ch),
            (Pane::Routes, 4) => self.config.proxy_local_port.push(ch),
            (Pane::Routes, 5) => self.config.proxy_remote_port.push(ch),
            (Pane::Config, 2) => self.config_text.push(ch),
            _ => {}
        }
        Ok(())
    }

    fn backspace(&mut self) -> Result<(), String> {
        match (self.pane, self.active) {
            (Pane::Connection, 0) => {
                self.config.vpn_host.pop();
            }
            (Pane::Connection, 1) => {
                self.config.vpn_user.pop();
            }
            (Pane::Connection, 2) => {
                self.vpn_password.pop();
            }
            (Pane::Connection, 3) => {
                self.config.vpn_auth_group.pop();
            }
            (Pane::Ssh, 1) => {
                self.ssh_filter.pop();
                self.selected_ssh = 0;
            }
            (Pane::Ssh, 2) => {
                self.config.ssh_host.pop();
            }
            (Pane::Ssh, 3) => {
                self.config.target_host.pop();
            }
            (Pane::Ssh, 4) => {
                self.config.target_user.pop();
            }
            (Pane::Ssh, 5) => {
                self.config.target_port.pop();
            }
            (Pane::Routes, 1) => {
                self.extra_routes_text.pop();
            }
            (Pane::Routes, 3) => {
                self.config.proxy_local_host.pop();
            }
            (Pane::Routes, 4) => {
                self.config.proxy_local_port.pop();
            }
            (Pane::Routes, 5) => {
                self.config.proxy_remote_port.pop();
            }
            (Pane::Config, 2) => {
                self.config_text.pop();
            }
            _ => {}
        }
        Ok(())
    }

    fn filtered_ssh_hosts(&self) -> Vec<&str> {
        let filter = self.ssh_filter.to_lowercase();
        self.ssh_hosts
            .iter()
            .filter(|host| filter.is_empty() || host.to_lowercase().contains(&filter))
            .map(String::as_str)
            .collect()
    }

    fn apply_selected_ssh_host(&mut self) -> Result<(), String> {
        let hosts = self.filtered_ssh_hosts();
        let Some(host) = hosts.get(self.selected_ssh).copied() else {
            self.status = "没有匹配的 SSH Host；可手动填写".to_string();
            return Ok(());
        };
        let host = host.to_string();
        let resolved = setup::resolve_ssh_host(&self.paths.main_ssh_config, &host);
        self.config.ssh_host = setup::managed_alias(&host);
        self.config.target_host = resolved.host;
        self.config.target_user = resolved.user;
        self.config.target_port = resolved.port;
        self.ssh_enabled = true;
        if self.extra_routes_text.trim().is_empty() {
            self.extra_routes_text =
                setup::default_cidr_for_host(&self.config.target_host).unwrap_or_default();
        }
        self.status = format!("已导入 {host}");
        Ok(())
    }

    fn apply_config_text(&mut self) -> Result<(), String> {
        let next = parse_config_str(&self.config_text, false, "TUI config editor")
            .map_err(|error| error.to_string())?;
        self.config = next;
        self.extra_routes_text = self.config.routes_extra.join("\n");
        self.ssh_enabled = setup::has_managed_ssh_config(&self.config);
        self.status = "已应用 TOML 到当前设置，Ctrl-S 保存到文件".to_string();
        Ok(())
    }

    fn sync_config_text(&mut self) {
        self.sync_routes_from_text();
        self.config_text = setup::render_config_toml(&self.config);
        self.status = "已从当前设置同步 TOML 预览".to_string();
    }

    fn sync_routes_from_text(&mut self) {
        self.config.routes_extra = self
            .extra_routes_text
            .split(|ch: char| ch == '\n' || ch == ' ' || ch == '\t' || ch == ',')
            .map(str::trim)
            .filter(|route| !route.is_empty())
            .map(ToString::to_string)
            .collect();
    }

    fn save_settings(&mut self) -> Result<(), String> {
        if self.config_load_failed {
            self.confirm = Some(ConfirmAction::OverwriteInvalidConfig);
            self.status = "现有 TOML 曾解析失败；再按 Enter 确认覆盖，Esc 取消".to_string();
            return Ok(());
        }
        self.save_settings_confirmed()
    }

    fn save_settings_confirmed(&mut self) -> Result<(), String> {
        self.validate_settings()?;
        self.sync_routes_from_text();
        setup::write_private_file(
            &self.paths.config_file,
            &setup::render_config_toml(&self.config),
            0o600,
        )?;
        if !self.vpn_password.is_empty() {
            setup::write_private_file(
                &self.paths.secrets_file,
                &setup::render_secrets(&self.vpn_password),
                0o600,
            )?;
        }
        if self.ssh_enabled {
            setup::write_managed_ssh_config(&self.config, &self.paths)?;
            setup::ensure_include_line(&self.paths.main_ssh_config, INCLUDE_LINE)?;
        }
        self.sync_config_text();
        self.status = if self.ssh_enabled {
            format!("已保存配置和 SSH Host: {}", self.config.ssh_host)
        } else {
            "已保存 VPN-only 配置；未修改旧 SSH 文件".to_string()
        };
        Ok(())
    }

    fn run_confirmed(&mut self, action: ConfirmAction) -> Result<(), String> {
        self.confirm = None;
        match action {
            ConfirmAction::UninstallService => self.run_och(["service", "uninstall"]),
            ConfirmAction::OverwriteInvalidConfig => {
                self.config_load_failed = false;
                self.save_settings_confirmed()
            }
        }
    }

    fn validate_settings(&self) -> Result<(), String> {
        require_non_empty("VPN 网关", &self.config.vpn_host)?;
        require_non_empty("VPN 用户", &self.config.vpn_user)?;
        if self.ssh_enabled {
            require_non_empty("托管 Host 别名", &self.config.ssh_host)?;
            require_non_empty("HostName/IP", &self.config.target_host)?;
            require_non_empty("SSH 用户", &self.config.target_user)?;
            setup::validate_port(&self.config.target_port)?;
        }
        if self.config.routes_mode == "extra" {
            for route in self
                .extra_routes_text
                .split(|ch: char| ch == '\n' || ch == ' ' || ch == '\t' || ch == ',')
                .map(str::trim)
                .filter(|route| !route.is_empty())
            {
                if !setup::valid_cidr(route) {
                    return Err(format!("CIDR 无效: {route}"));
                }
            }
        }
        if self.config.proxy_enabled {
            require_non_empty("Proxy local_host", &self.config.proxy_local_host)?;
            setup::validate_port(&self.config.proxy_local_port)?;
            setup::validate_port(&self.config.proxy_remote_port)?;
        }
        Ok(())
    }

    fn run_och<const N: usize>(&mut self, args: [&str; N]) -> Result<(), String> {
        let exe = std::env::current_exe().map_err(|error| error.to_string())?;
        let output = Command::new(exe)
            .args(args)
            .env("OCH_CONFIG_FILE", &self.paths.config_file)
            .env("OCH_SECRETS_FILE", &self.paths.secrets_file)
            .output()
            .map_err(|error| error.to_string())?;
        let mut text = String::new();
        text.push_str(&String::from_utf8_lossy(&output.stdout));
        text.push_str(&String::from_utf8_lossy(&output.stderr));
        let summary = if output.status.success() {
            format!("命令成功: och {}", args.join(" "))
        } else {
            format!("命令失败: och {} ({})", args.join(" "), output.status)
        };
        let joined = args.join(" ");
        if joined == "vpn status" {
            self.connection_summary = summarize(&text);
        } else if joined == "service status" {
            self.service_summary = summarize(&text);
        } else if joined == "vpn logs" {
            self.logs = text.clone();
        }
        if !text.trim().is_empty() {
            self.logs.push_str("\n$ och ");
            self.logs.push_str(&joined);
            self.logs.push('\n');
            self.logs.push_str(text.trim_end());
            self.logs.push('\n');
        }
        self.status = summary;
        Ok(())
    }

    fn install_include(&mut self) -> Result<(), String> {
        if !self.ssh_enabled {
            return Err("SSH 未启用，无法安装 Include".to_string());
        }
        self.validate_settings()?;
        setup::write_managed_ssh_config(&self.config, &self.paths)?;
        setup::ensure_include_line(&self.paths.main_ssh_config, INCLUDE_LINE)?;
        self.status = "已安装 SSH Include".to_string();
        Ok(())
    }

    fn probe_auth_groups(&mut self) -> Result<(), String> {
        require_non_empty("VPN 网关", &self.config.vpn_host)?;
        require_non_empty("VPN 用户", &self.config.vpn_user)?;
        let output = Command::new("openconnect")
            .arg(&self.config.vpn_host)
            .arg("-u")
            .arg(&self.config.vpn_user)
            .arg("--authenticate")
            .arg("--non-inter")
            .output()
            .map_err(|error| format!("无法运行 openconnect 探测认证组: {error}"))?;
        let mut text = String::new();
        text.push_str(&String::from_utf8_lossy(&output.stdout));
        text.push_str(&String::from_utf8_lossy(&output.stderr));
        let groups = parse_auth_groups(&text);
        if let Some(group) = groups.first() {
            self.config.vpn_auth_group = group.clone();
            self.status = format!("已探测到 {} 个认证组，已填入: {group}", groups.len());
        } else {
            self.status = "未探测到认证组，可手动填写或留空".to_string();
        }
        self.logs
            .push_str("\n$ openconnect --authenticate --non-inter\n");
        self.logs.push_str(text.trim_end());
        self.logs.push('\n');
        Ok(())
    }

    #[cfg(test)]
    fn parse_and_set_auth_groups_for_test(&mut self, output: &str) {
        if let Some(group) = parse_auth_groups(output).first() {
            self.config.vpn_auth_group = group.clone();
        }
    }

    fn refresh_logs_action(&mut self) -> Result<(), String> {
        self.run_och(["vpn", "logs"])?;
        self.refresh_logs();
        Ok(())
    }

    fn refresh_logs(&mut self) {
        let log_path = runtime_log_file();
        if let Ok(raw) = fs::read_to_string(&log_path) {
            self.logs = tail_lines_text(&raw, 80);
        }
    }

    fn pane_hint(&self) -> String {
        match self.pane {
            Pane::Overview => "Overview: ↑/↓ 切页面，←/→ 切操作，Enter 执行".to_string(),
            Pane::Connection => {
                "Connection: ←/→ 切字段，编辑 VPN 字段，Enter 连接/断开/探测认证组，Ctrl-S 保存"
                    .to_string()
            }
            Pane::Ssh => {
                "SSH: ←/→ 切字段，输入过滤 Host，PageUp/PageDown 选 Host，Enter 导入".to_string()
            }
            Pane::Routes => {
                "Routes & Proxy: ←/→ 切字段，Enter 切换 mode/proxy，Ctrl-S 保存".to_string()
            }
            Pane::Service => {
                "Service: ←/→ 切操作，Enter 执行 status/install/uninstall/logs".to_string()
            }
            Pane::Config => "Config: ←/→ 切操作/编辑器，Enter 插入换行，Ctrl-S 保存".to_string(),
            Pane::Logs => "Logs: ↑/↓ 切页面，Enter 刷新日志".to_string(),
        }
    }
}

fn render(frame: &mut Frame, state: &TuiState) {
    let area = frame.area();
    let root = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Min(12), Constraint::Length(3)])
        .split(area);
    let body = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(24), Constraint::Min(40)])
        .split(root[0]);

    render_sidebar(frame, body[0], state);
    match state.pane {
        Pane::Overview => render_overview(frame, body[1], state),
        Pane::Connection => render_connection(frame, body[1], state),
        Pane::Ssh => render_ssh(frame, body[1], state),
        Pane::Routes => render_routes(frame, body[1], state),
        Pane::Service => render_service(frame, body[1], state),
        Pane::Config => render_config(frame, body[1], state),
        Pane::Logs => render_logs(frame, body[1], state),
    }
    let footer = Paragraph::new(state.status.as_str())
        .block(Block::default().borders(Borders::TOP).title("Status"))
        .wrap(Wrap { trim: true });
    frame.render_widget(footer, root[1]);
}

fn render_sidebar(frame: &mut Frame, area: Rect, state: &TuiState) {
    let items = Pane::ALL
        .iter()
        .map(|pane| {
            let marker = if *pane == state.pane { "> " } else { "  " };
            ListItem::new(format!("{marker}{}", pane.title()))
        })
        .collect::<Vec<_>>();
    let list = List::new(items).block(Block::default().title("OCH").borders(Borders::ALL));
    frame.render_widget(list, area);
}

fn render_overview(frame: &mut Frame, area: Rect, state: &TuiState) {
    let include = main_config_includes_managed(&state.paths.main_ssh_config);
    let rows = vec![
        action_line(0, state.active, "刷新 VPN 状态", &state.connection_summary),
        action_line(1, state.active, "刷新服务状态", &state.service_summary),
        action_line(
            2,
            state.active,
            "安装 SSH Include",
            if include { "已安装" } else { "未安装" },
        ),
        action_line(
            3,
            state.active,
            "保存配置",
            &state.paths.config_file.display().to_string(),
        ),
        Line::raw(""),
        Line::from(format!(
            "VPN: {} / {}",
            empty_dash(&state.config.vpn_host),
            empty_dash(&state.config.vpn_user)
        )),
        Line::from(format!(
            "SSH: {}",
            if state.ssh_enabled {
                format!("{} -> {}", state.config.ssh_host, state.config.target_host)
            } else {
                "disabled".to_string()
            }
        )),
        Line::from(format!("Routes mode: {}", state.config.routes_mode)),
    ];
    render_lines(frame, area, "Overview", rows);
}

fn render_connection(frame: &mut Frame, area: Rect, state: &TuiState) {
    let rows = vec![
        field_line(0, state.active, "VPN 网关", &state.config.vpn_host, false),
        field_line(1, state.active, "VPN 用户", &state.config.vpn_user, false),
        field_line(2, state.active, "VPN 密码", &state.vpn_password, true),
        field_line(
            3,
            state.active,
            "认证组",
            &state.config.vpn_auth_group,
            false,
        ),
        action_line(4, state.active, "连接 VPN", "och vpn connect"),
        action_line(5, state.active, "断开 VPN", "och vpn disconnect"),
        action_line(6, state.active, "探测认证组", "openconnect --authenticate"),
    ];
    render_lines(frame, area, "Connection", rows);
}

fn render_ssh(frame: &mut Frame, area: Rect, state: &TuiState) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(38), Constraint::Percentage(62)])
        .split(area);
    let hosts = state.filtered_ssh_hosts();
    let items = if hosts.is_empty() {
        vec![ListItem::new("无匹配；可手动填写")]
    } else {
        hosts
            .iter()
            .enumerate()
            .take(16)
            .map(|(index, host)| {
                let marker = if state.active == 1 && state.selected_ssh == index {
                    "> "
                } else {
                    "  "
                };
                ListItem::new(format!("{marker}{host}"))
            })
            .collect()
    };
    frame.render_widget(
        List::new(items).block(
            Block::default()
                .title(format!("Import: {}", state.ssh_filter))
                .borders(Borders::ALL),
        ),
        chunks[0],
    );
    let rows = vec![
        field_line(
            0,
            state.active,
            "启用 SSH",
            yes_no(state.ssh_enabled),
            false,
        ),
        Line::from("左侧焦点时 Enter 导入选中 Host"),
        Line::raw(""),
        field_line(2, state.active, "托管 Host", &state.config.ssh_host, false),
        field_line(
            3,
            state.active,
            "HostName/IP",
            &state.config.target_host,
            false,
        ),
        field_line(
            4,
            state.active,
            "SSH 用户",
            &state.config.target_user,
            false,
        ),
        field_line(
            5,
            state.active,
            "SSH 端口",
            &state.config.target_port,
            false,
        ),
    ];
    render_lines(frame, chunks[1], "SSH", rows);
}

fn render_routes(frame: &mut Frame, area: Rect, state: &TuiState) {
    let rows = vec![
        field_line(
            0,
            state.active,
            "Route mode",
            &state.config.routes_mode,
            false,
        ),
        field_line(
            1,
            state.active,
            "Extra routes",
            &state.extra_routes_text,
            false,
        ),
        field_line(
            2,
            state.active,
            "启用 Proxy",
            yes_no(state.config.proxy_enabled),
            false,
        ),
        field_line(
            3,
            state.active,
            "Proxy local_host",
            &state.config.proxy_local_host,
            false,
        ),
        field_line(
            4,
            state.active,
            "Proxy local_port",
            &state.config.proxy_local_port,
            false,
        ),
        field_line(
            5,
            state.active,
            "Proxy remote_port",
            &state.config.proxy_remote_port,
            false,
        ),
    ];
    render_lines(frame, area, "Routes & Proxy", rows);
}

fn render_service(frame: &mut Frame, area: Rect, state: &TuiState) {
    let rows = vec![
        action_line(0, state.active, "刷新状态", "och service status"),
        action_line(1, state.active, "安装服务", "och service install"),
        action_line(2, state.active, "卸载服务", "och service uninstall"),
        action_line(3, state.active, "刷新日志", "och vpn logs"),
        Line::raw(""),
        Line::from(format!("最近服务状态: {}", state.service_summary)),
        Line::from("非 macOS 或未 root 时，服务命令会直接显示不可用/权限错误。"),
    ];
    render_lines(frame, area, "Service", rows);
}

fn render_config(frame: &mut Frame, area: Rect, state: &TuiState) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(4), Constraint::Min(8)])
        .split(area);
    let rows = vec![
        action_line(0, state.active, "Apply TOML", "把编辑器内容应用到当前设置"),
        action_line(
            1,
            state.active,
            "Sync from settings",
            "从当前设置重新生成 TOML",
        ),
        action_line(
            2,
            state.active,
            "TOML editor",
            "输入字符编辑，Enter 插入换行，Ctrl-S 保存",
        ),
    ];
    render_lines(frame, chunks[0], "Config Actions", rows);
    let editor = Paragraph::new(state.config_text.as_str())
        .block(Block::default().title("TOML").borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(editor, chunks[1]);
}

fn render_logs(frame: &mut Frame, area: Rect, state: &TuiState) {
    let rows = vec![
        action_line(0, state.active, "刷新日志", "och vpn logs"),
        action_line(1, state.active, "刷新日志", "同上"),
        Line::raw(""),
        Line::from(state.logs.clone()),
    ];
    render_lines(frame, area, "Logs", rows);
}

fn render_lines(frame: &mut Frame, area: Rect, title: &str, rows: Vec<Line<'static>>) {
    let widget = Paragraph::new(rows)
        .block(Block::default().title(title).borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(widget, area);
}

fn field_line(
    index: usize,
    active: usize,
    label: &str,
    value: &str,
    secret: bool,
) -> Line<'static> {
    let shown = if secret {
        "*".repeat(value.chars().count())
    } else if value.is_empty() {
        "<empty>".to_string()
    } else {
        value.replace('\n', " ")
    };
    styled_line(index, active, label, &shown)
}

fn action_line(index: usize, active: usize, label: &str, detail: &str) -> Line<'static> {
    styled_line(index, active, label, detail)
}

fn styled_line(index: usize, active: usize, label: &str, detail: &str) -> Line<'static> {
    let style = if index == active {
        Style::default()
            .fg(Color::Yellow)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default()
    };
    let prefix = if index == active { "> " } else { "  " };
    Line::from(vec![
        Span::styled(format!("{prefix}{label}: "), style),
        Span::raw(detail.to_string()),
    ])
}

fn require_non_empty(label: &str, value: &str) -> Result<(), String> {
    if value.trim().is_empty() {
        Err(format!("{label}不能为空"))
    } else {
        Ok(())
    }
}

fn main_config_includes_managed(path: &PathBuf) -> bool {
    fs::read_to_string(path)
        .map(|contents| contents.lines().any(|line| line.trim() == INCLUDE_LINE))
        .unwrap_or(false)
}

fn runtime_log_file() -> PathBuf {
    let user = std::env::var("USER").unwrap_or_else(|_| "user".to_string());
    std::env::var("LOG_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(format!("/tmp/och-openconnect-{user}.log")))
}

fn tail_lines_text(text: &str, count: usize) -> String {
    let lines: Vec<&str> = text.lines().collect();
    let start = lines.len().saturating_sub(count);
    lines[start..].join("\n")
}

fn summarize(text: &str) -> String {
    text.lines()
        .find(|line| !line.trim().is_empty())
        .unwrap_or("无输出")
        .trim()
        .to_string()
}

fn parse_auth_groups(output: &str) -> Vec<String> {
    let mut groups = Vec::new();
    for line in output.lines() {
        if let Some(start) = line.find("GROUP:") {
            if let Some(open) = line[start..].find('[') {
                if let Some(close) = line[start + open + 1..].find(']') {
                    let body = &line[start + open + 1..start + open + 1 + close];
                    for item in body.split('|') {
                        push_unique(&mut groups, item);
                    }
                }
            }
        }
        let trimmed = line.trim();
        if let Some((head, tail)) = trimmed.split_once(')') {
            if head.chars().all(|ch| ch.is_ascii_digit()) {
                push_unique(&mut groups, tail);
            }
        }
        if let Some((head, tail)) = trimmed.split_once('.') {
            if head.chars().all(|ch| ch.is_ascii_digit()) {
                push_unique(&mut groups, tail);
            }
        }
        let mut rest = line;
        while let Some(start) = rest.find("<option") {
            rest = &rest[start..];
            let Some(gt) = rest.find('>') else {
                break;
            };
            let after = &rest[gt + 1..];
            let Some(end) = after.find("</option>") else {
                break;
            };
            push_unique(&mut groups, &after[..end]);
            rest = &after[end + "</option>".len()..];
        }
    }
    groups
}

fn push_unique(values: &mut Vec<String>, value: &str) {
    let trimmed = value.trim();
    if !trimmed.is_empty() && !values.iter().any(|item| item == trimmed) {
        values.push(trimmed.to_string());
    }
}

fn yes_no(value: bool) -> &'static str {
    if value {
        "yes"
    } else {
        "no"
    }
}

fn empty_dash(value: &str) -> &str {
    if value.is_empty() {
        "-"
    } else {
        value
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ratatui::backend::TestBackend;

    fn state_for_test() -> TuiState {
        let dir = tempfile::tempdir().unwrap().keep();
        let config = OchConfig {
            vpn_host: "vpn.example.com".to_string(),
            vpn_user: "alice".to_string(),
            vpn_auth_group: String::new(),
            ssh_host: "och-target".to_string(),
            target_host: String::new(),
            target_user: "alice".to_string(),
            target_port: "22".to_string(),
            routes_mode: "openconnect".to_string(),
            routes_extra: Vec::new(),
            proxy_enabled: false,
            proxy_local_host: "127.0.0.1".to_string(),
            proxy_local_port: "7890".to_string(),
            proxy_remote_port: "7890".to_string(),
            app_language: "system".to_string(),
        };
        TuiState {
            pane: Pane::Overview,
            active: 0,
            config: config.clone(),
            vpn_password: "secret".to_string(),
            extra_routes_text: String::new(),
            config_text: setup::render_config_toml(&config),
            logs: String::new(),
            status: "ready".to_string(),
            paths: setup::SetupPaths {
                config_file: dir.join("config.toml"),
                secrets_file: dir.join("secrets.env"),
                managed_ssh_config: dir.join("och.config"),
                main_ssh_config: dir.join("ssh-config"),
                och_bin: PathBuf::from("och"),
            },
            ssh_enabled: false,
            ssh_filter: String::new(),
            ssh_hosts: Vec::new(),
            selected_ssh: 0,
            connection_summary: "未知".to_string(),
            service_summary: "未知".to_string(),
            confirm: None,
            config_load_failed: false,
            canceled: false,
        }
    }

    #[test]
    fn renders_overview() {
        let state = state_for_test();
        let backend = TestBackend::new(100, 28);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let text = format!("{:?}", terminal.backend().buffer());
        assert!(text.contains("Overview"));
        assert!(text.contains("Connection"));
    }

    #[test]
    fn renders_routes_and_config() {
        let mut state = state_for_test();
        state.pane = Pane::Routes;
        let backend = TestBackend::new(100, 28);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let text = format!("{:?}", terminal.backend().buffer());
        assert!(text.contains("Routes & Proxy"));

        state.pane = Pane::Config;
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let text = format!("{:?}", terminal.backend().buffer());
        assert!(text.contains("TOML"));
    }

    #[test]
    fn vpn_only_save_does_not_write_ssh_files() {
        let mut state = state_for_test();
        state.save_settings().unwrap();
        assert!(state.paths.config_file.is_file());
        assert!(state.paths.secrets_file.is_file());
        assert!(!state.paths.managed_ssh_config.exists());
    }

    #[test]
    fn invalid_route_is_status_error_not_panic() {
        let mut state = state_for_test();
        state.pane = Pane::Routes;
        state.config.routes_mode = "extra".to_string();
        state.extra_routes_text = "10.2.3.999/32".to_string();
        let error = state.save_settings().unwrap_err();
        assert!(error.contains("CIDR 无效"));
    }

    #[test]
    fn invalid_config_save_requires_confirmation() {
        let mut state = state_for_test();
        state.config_load_failed = true;
        state.save_settings().unwrap();
        assert_eq!(state.confirm, Some(ConfirmAction::OverwriteInvalidConfig));
        assert!(!state.paths.config_file.exists());

        state
            .run_confirmed(ConfirmAction::OverwriteInvalidConfig)
            .unwrap();
        assert!(state.paths.config_file.exists());
    }

    #[test]
    fn arrow_keys_match_sidebar_and_field_navigation() {
        let mut state = state_for_test();
        assert_eq!(state.pane, Pane::Overview);
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Down, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Connection);
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Right, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Connection);
        assert_eq!(state.active, 1);

        state
            .handle_key(KeyEvent::new(KeyCode::Left, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Up, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Overview);
        assert_eq!(state.active, 0);

        state.pane = Pane::Ssh;
        state.active = 1;
        state.ssh_hosts = vec!["alpha".to_string(), "beta".to_string()];
        state
            .handle_key(KeyEvent::new(KeyCode::PageDown, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.selected_ssh, 1);
        state
            .handle_key(KeyEvent::new(KeyCode::Right, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.active, 2);
        assert_eq!(state.selected_ssh, 1);
    }

    #[test]
    fn parses_auth_groups_like_gui_and_shell() {
        let mut state = state_for_test();
        state.parse_and_set_auth_groups_for_test(
            "GROUP: [staff|vpn-users]\n  3) contractors\n<option>faculty</option>",
        );
        assert_eq!(state.config.vpn_auth_group, "staff");
        assert_eq!(
            parse_auth_groups(
                "GROUP: [staff|vpn-users]\n  3) contractors\n<option>faculty</option>"
            ),
            ["staff", "vpn-users", "contractors", "faculty"]
        );
    }
}
