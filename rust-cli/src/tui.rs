use crate::config::{load_secret_password, parse_config_str, OchConfig};
use crate::setup;
use crossterm::event::{
    self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode, KeyEvent, KeyModifiers,
    MouseButton, MouseEvent, MouseEventKind,
};
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Constraint, Direction, Layout, Rect, Size};
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, List, ListItem, Paragraph, Tabs, Wrap};
use ratatui::{Frame, Terminal};
use std::fs;
use std::io::{self, IsTerminal};
use std::path::PathBuf;
use std::process::Command;
use std::time::{Duration, Instant};

const INCLUDE_LINE: &str = "Include ~/.ssh/och.config";
const EVENT_POLL_INTERVAL: Duration = Duration::from_millis(250);
const VPN_REFRESH_INTERVAL: Duration = Duration::from_secs(5);
const SERVICE_REFRESH_INTERVAL: Duration = Duration::from_secs(15);
const LOG_REFRESH_INTERVAL: Duration = Duration::from_secs(3);
const LOGS_PANE_REFRESH_INTERVAL: Duration = Duration::from_secs(1);

mod theme {
    use ratatui::style::{Color, Modifier, Style};

    pub fn panel_border() -> Style {
        Style::default().fg(Color::DarkGray)
    }

    pub fn panel_title() -> Style {
        Style::default()
            .fg(Color::LightCyan)
            .add_modifier(Modifier::BOLD)
    }

    pub fn body() -> Style {
        Style::default().fg(Color::Gray)
    }

    pub fn text() -> Style {
        Style::default().fg(Color::White)
    }

    pub fn muted() -> Style {
        Style::default().fg(Color::DarkGray)
    }

    pub fn active_label() -> Style {
        Style::default()
            .fg(Color::LightYellow)
            .add_modifier(Modifier::BOLD)
    }

    pub fn label() -> Style {
        Style::default().fg(Color::LightCyan)
    }

    pub fn active_value() -> Style {
        Style::default()
            .fg(Color::White)
            .add_modifier(Modifier::BOLD)
    }

    pub fn success() -> Style {
        Style::default().fg(Color::LightGreen)
    }

    pub fn warning() -> Style {
        Style::default().fg(Color::LightYellow)
    }

    pub fn danger() -> Style {
        Style::default().fg(Color::LightRed)
    }

    pub fn tab() -> Style {
        Style::default().fg(Color::Gray)
    }

    pub fn selected_tab() -> Style {
        Style::default()
            .fg(Color::Black)
            .bg(Color::LightCyan)
            .add_modifier(Modifier::BOLD)
    }

    pub fn log() -> Style {
        Style::default().fg(Color::LightGreen)
    }
}

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

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum RefreshTarget {
    VpnStatus,
    ServiceStatus,
    LogTail,
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

    fn number(self) -> usize {
        Pane::ALL
            .iter()
            .position(|pane| *pane == self)
            .map(|index| index + 1)
            .unwrap_or(1)
    }

    fn tab_title(self) -> String {
        format!("{} {}", self.number(), self.title())
    }

    fn from_number_key(ch: char) -> Option<Self> {
        let index = ch.to_digit(10)? as usize;
        if (1..=Pane::ALL.len()).contains(&index) {
            Some(Pane::ALL[index - 1])
        } else {
            None
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
    auto_refresh: bool,
    last_vpn_refresh: Option<Instant>,
    last_service_refresh: Option<Instant>,
    last_log_refresh: Option<Instant>,
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
        if event::poll(EVENT_POLL_INTERVAL).map_err(|error| error.to_string())? {
            let result = match event::read().map_err(|error| error.to_string())? {
                Event::Key(key) => state.handle_key(key),
                Event::Mouse(mouse) => state.handle_mouse(mouse, terminal.size()?),
                _ => Ok(()),
            };
            if let Err(error) = result {
                state.status = error;
            }
        } else if let Err(error) = state.handle_tick(Instant::now()) {
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
        execute!(stdout, EnterAlternateScreen, EnableMouseCapture)
            .map_err(|error| error.to_string())?;
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

    fn size(&self) -> Result<Size, String> {
        self.terminal.size().map_err(|error| error.to_string())
    }
}

impl Drop for TerminalSession {
    fn drop(&mut self) {
        let _ = disable_raw_mode();
        let _ = execute!(
            self.terminal.backend_mut(),
            DisableMouseCapture,
            LeaveAlternateScreen
        );
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
                "←/→ 切页面，↑/↓ 或 Tab 切字段，也可鼠标点击；Enter 执行，Ctrl-S 保存，Esc 退出"
                    .to_string()
            }),
            paths,
            ssh_enabled,
            ssh_filter: String::new(),
            ssh_hosts,
            selected_ssh: 0,
            connection_summary: "未知".to_string(),
            service_summary: "未刷新".to_string(),
            auto_refresh: true,
            last_vpn_refresh: None,
            last_service_refresh: None,
            last_log_refresh: None,
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
            KeyCode::Left => {
                self.previous_pane();
                Ok(())
            }
            KeyCode::Right => {
                self.next_pane();
                Ok(())
            }
            KeyCode::Up => self.previous_list_item(),
            KeyCode::Down => self.next_list_item(),
            KeyCode::PageUp => self.previous_list_item(),
            KeyCode::PageDown => self.next_list_item(),
            KeyCode::Tab => self.next_field(),
            KeyCode::BackTab => self.previous_field(),
            KeyCode::Enter => self.enter(),
            KeyCode::Char('a') if !self.is_text_field_active() => {
                self.auto_refresh = !self.auto_refresh;
                self.status = if self.auto_refresh {
                    "自动刷新已开启".to_string()
                } else {
                    "自动刷新已关闭，按 r 手动刷新当前页".to_string()
                };
                Ok(())
            }
            KeyCode::Char('r') if !self.is_text_field_active() => {
                self.refresh_current_pane(true, Instant::now())
            }
            KeyCode::Char(ch) if !self.is_text_field_active() => {
                if let Some(pane) = Pane::from_number_key(ch) {
                    self.pane = pane;
                    self.active = 0;
                    self.status = self.pane_hint();
                }
                Ok(())
            }
            KeyCode::Backspace => self.backspace(),
            KeyCode::Char(ch) => self.push_char(ch),
            _ => Ok(()),
        }
    }

    fn handle_mouse(&mut self, mouse: MouseEvent, size: Size) -> Result<(), String> {
        match mouse.kind {
            MouseEventKind::Down(MouseButton::Left) => {
                self.handle_left_click(mouse.column, mouse.row, screen_rect(size))
            }
            MouseEventKind::ScrollDown if self.pane == Pane::Ssh && self.active == 1 => {
                self.next_list_item()
            }
            MouseEventKind::ScrollUp if self.pane == Pane::Ssh && self.active == 1 => {
                self.previous_list_item()
            }
            _ => Ok(()),
        }
    }

    fn handle_left_click(&mut self, x: u16, y: u16, area: Rect) -> Result<(), String> {
        if self.confirm.is_some() {
            self.status = "当前有待确认操作：Enter 确认，Esc 取消".to_string();
            return Ok(());
        }

        let layout = root_layout(area);
        if let Some(pane) = tab_at(layout.tabs, x, y) {
            self.pane = pane;
            self.active = 0;
            self.status = self.pane_hint();
            return Ok(());
        }

        match self.pane {
            Pane::Overview => self.click_overview(layout.body, x, y),
            Pane::Connection => self.click_connection(layout.body, x, y),
            Pane::Ssh => self.click_ssh(layout.body, x, y),
            Pane::Routes => self.click_routes(layout.body, x, y),
            Pane::Service => self.click_service(layout.body, x, y),
            Pane::Config => self.click_config(layout.body, x, y),
            Pane::Logs => self.click_logs(layout.body, x, y),
        }
    }

    fn click_overview(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Length(9), Constraint::Min(8)])
            .split(area);
        let lower = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(48), Constraint::Percentage(52)])
            .split(chunks[1]);
        if let Some(row) = content_row(lower[0], x, y).filter(|row| *row < 4) {
            self.active = row;
            return self.enter();
        }
        Ok(())
    }

    fn click_connection(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(58), Constraint::Percentage(42)])
            .split(area);
        let Some(row) = content_row(chunks[0], x, y) else {
            return Ok(());
        };
        let Some(active) = (match row {
            0..=3 => Some(row),
            5..=7 => Some(row - 1),
            _ => None,
        }) else {
            return Ok(());
        };
        self.active = active;
        if active >= 4 {
            self.enter()
        } else {
            Ok(())
        }
    }

    fn click_ssh(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(38), Constraint::Percentage(62)])
            .split(area);
        if let Some(row) = content_row(chunks[0], x, y) {
            let host_count = self.filtered_ssh_hosts().len().min(16);
            if row < host_count {
                self.active = 1;
                self.selected_ssh = row;
                return self.apply_selected_ssh_host();
            }
            self.active = 1;
            return Ok(());
        }
        let Some(row) = content_row(chunks[1], x, y) else {
            return Ok(());
        };
        let Some(active) = (match row {
            0 => Some(0),
            3..=6 => Some(row - 1),
            _ => None,
        }) else {
            return Ok(());
        };
        self.active = active;
        if active == 0 {
            self.enter()
        } else {
            Ok(())
        }
    }

    fn click_routes(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(58), Constraint::Percentage(42)])
            .split(area);
        let Some(row) = content_row(chunks[0], x, y).filter(|row| *row < 7) else {
            return Ok(());
        };
        self.active = row;
        if matches!(row, 0 | 2 | 6) {
            self.enter()
        } else {
            Ok(())
        }
    }

    fn click_service(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(48), Constraint::Percentage(52)])
            .split(area);
        if let Some(row) = content_row(chunks[0], x, y).filter(|row| *row < 4) {
            self.active = row;
            return self.enter();
        }
        Ok(())
    }

    fn click_config(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Length(4), Constraint::Min(8)])
            .split(area);
        if let Some(row) = content_row(chunks[0], x, y).filter(|row| *row < 3) {
            self.active = row;
            if row < 2 {
                return self.enter();
            }
            return Ok(());
        }
        if rect_contains(chunks[1], x, y) {
            self.active = 2;
        }
        Ok(())
    }

    fn click_logs(&mut self, area: Rect, x: u16, y: u16) -> Result<(), String> {
        let chunks = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Length(4), Constraint::Min(8)])
            .split(area);
        if content_row(chunks[0], x, y) == Some(0) {
            self.active = 0;
            return self.enter();
        }
        Ok(())
    }

    fn handle_tick(&mut self, now: Instant) -> Result<(), String> {
        if self.auto_refresh {
            self.refresh_current_pane(false, now)?;
        }
        Ok(())
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

    fn is_text_field_active(&self) -> bool {
        matches!(
            (self.pane, self.active),
            (Pane::Connection, 0..=3)
                | (Pane::Ssh, 1..=5)
                | (Pane::Routes, 1 | 3..=5)
                | (Pane::Config, 2)
        )
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
            Pane::Routes => 7,
            Pane::Service => 4,
            Pane::Config => 3,
            Pane::Logs => 1,
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
                6 => {
                    self.config.dns_mode = if self.config.dns_mode == "ignore" {
                        "openconnect".to_string()
                    } else {
                        "ignore".to_string()
                    };
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
        self.config.routes_extra = route_entries(&self.extra_routes_text)
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
            for route in route_entries(&self.extra_routes_text) {
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
        let (success, text) = self.run_och_capture(args)?;
        let joined = args.join(" ");
        let summary = if success {
            format!("命令成功: och {joined}")
        } else {
            format!("命令失败: och {joined}")
        };
        self.apply_command_output(&joined, &text);
        self.status = summary;
        Ok(())
    }

    fn run_och_capture<const N: usize>(&self, args: [&str; N]) -> Result<(bool, String), String> {
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
        Ok((output.status.success(), text))
    }

    fn apply_command_output(&mut self, joined: &str, text: &str) {
        if joined == "vpn status" {
            self.connection_summary = summarize(text);
        } else if joined == "service status" {
            self.service_summary = summarize(text);
        } else if joined == "vpn logs" {
            self.logs = text.to_string();
        }
        if !text.trim().is_empty() {
            self.logs.push_str("\n$ och ");
            self.logs.push_str(joined);
            self.logs.push('\n');
            self.logs.push_str(text.trim_end());
            self.logs.push('\n');
        }
    }

    fn refresh_current_pane(&mut self, forced: bool, now: Instant) -> Result<(), String> {
        let mut refreshed = Vec::new();
        for target in refresh_targets_for_pane(self.pane) {
            if !forced && !self.refresh_due(*target, now) {
                continue;
            }
            self.refresh_target(*target, now)?;
            refreshed.push(target.title());
        }
        if forced {
            if refreshed.is_empty() {
                self.status = format!("{} 无可刷新的运行态数据", self.pane.title());
            } else {
                self.status = format!("已刷新: {}", refreshed.join(", "));
            }
        } else if !refreshed.is_empty() {
            self.status = format!("自动刷新: {}", refreshed.join(", "));
        }
        Ok(())
    }

    fn refresh_due(&self, target: RefreshTarget, now: Instant) -> bool {
        let last = match target {
            RefreshTarget::VpnStatus => self.last_vpn_refresh,
            RefreshTarget::ServiceStatus => self.last_service_refresh,
            RefreshTarget::LogTail => self.last_log_refresh,
        };
        last.is_none_or(|last| now.duration_since(last) >= refresh_interval(self.pane, target))
    }

    fn refresh_target(&mut self, target: RefreshTarget, now: Instant) -> Result<(), String> {
        match target {
            RefreshTarget::VpnStatus => {
                let (_, text) = self.run_och_capture(["vpn", "status"])?;
                self.apply_command_output("vpn status", &text);
                self.last_vpn_refresh = Some(now);
            }
            RefreshTarget::ServiceStatus => {
                let (_, text) = self.run_och_capture(["service", "status"])?;
                self.apply_command_output("service status", &text);
                self.last_service_refresh = Some(now);
            }
            RefreshTarget::LogTail => {
                self.refresh_logs();
                self.last_log_refresh = Some(now);
            }
        }
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
            Pane::Overview => "Overview: ←/→ 切页面，↑/↓ 切操作，Enter 执行".to_string(),
            Pane::Connection => {
                "Connection: ←/→ 切页面，↑/↓ 或 Tab 切字段，Enter 连接/断开/探测认证组，Ctrl-S 保存"
                    .to_string()
            }
            Pane::Ssh => {
                "SSH: ←/→ 切页面，↑/↓ 或 Tab 切字段，Host 列表用 PageUp/PageDown，Enter 导入"
                    .to_string()
            }
            Pane::Routes => {
                "Routes & Proxy: ←/→ 切页面，↑/↓ 或 Tab 切字段，Enter 切换 mode/proxy，Ctrl-S 保存"
                    .to_string()
            }
            Pane::Service => {
                "Service: ←/→ 切页面，↑/↓ 切操作，Enter 执行 status/install/uninstall/logs"
                    .to_string()
            }
            Pane::Config => {
                "Config: ←/→ 切页面，↑/↓ 或 Tab 切操作/编辑器，Enter 插入换行，Ctrl-S 保存"
                    .to_string()
            }
            Pane::Logs => "Logs: ←/→ 切页面，Enter 刷新日志，a 自动刷新，r 立即刷新".to_string(),
        }
    }
}

impl RefreshTarget {
    fn title(self) -> &'static str {
        match self {
            RefreshTarget::VpnStatus => "VPN",
            RefreshTarget::ServiceStatus => "Service",
            RefreshTarget::LogTail => "Logs",
        }
    }
}

fn refresh_targets_for_pane(pane: Pane) -> &'static [RefreshTarget] {
    match pane {
        Pane::Overview => &[
            RefreshTarget::VpnStatus,
            RefreshTarget::ServiceStatus,
            RefreshTarget::LogTail,
        ],
        Pane::Connection => &[RefreshTarget::VpnStatus],
        Pane::Service => &[RefreshTarget::ServiceStatus, RefreshTarget::LogTail],
        Pane::Logs => &[RefreshTarget::VpnStatus, RefreshTarget::LogTail],
        Pane::Ssh | Pane::Routes | Pane::Config => &[],
    }
}

fn refresh_interval(pane: Pane, target: RefreshTarget) -> Duration {
    match target {
        RefreshTarget::VpnStatus => VPN_REFRESH_INTERVAL,
        RefreshTarget::ServiceStatus => SERVICE_REFRESH_INTERVAL,
        RefreshTarget::LogTail if pane == Pane::Logs => LOGS_PANE_REFRESH_INTERVAL,
        RefreshTarget::LogTail => LOG_REFRESH_INTERVAL,
    }
}

fn render(frame: &mut Frame, state: &TuiState) {
    let root = root_layout(frame.area());

    render_tabs(frame, root.tabs, state);
    match state.pane {
        Pane::Overview => render_overview(frame, root.body, state),
        Pane::Connection => render_connection(frame, root.body, state),
        Pane::Ssh => render_ssh(frame, root.body, state),
        Pane::Routes => render_routes(frame, root.body, state),
        Pane::Service => render_service(frame, root.body, state),
        Pane::Config => render_config(frame, root.body, state),
        Pane::Logs => render_logs(frame, root.body, state),
    }
    let footer = Paragraph::new(Line::from(vec![
        Span::styled(state.status.clone(), status_style(&state.status)),
        Span::styled(
            " | 1-7 jump | ←/→ tabs | ↑/↓ fields | Tab next | Ctrl-S save | Auto: ",
            theme::muted(),
        ),
        Span::styled(
            if state.auto_refresh { "on" } else { "off" },
            if state.auto_refresh {
                theme::success()
            } else {
                theme::warning()
            },
        ),
        Span::styled(" | r refresh | a auto", theme::muted()),
    ]))
    .block(panel_block("Status").borders(Borders::TOP))
    .wrap(Wrap { trim: true });
    frame.render_widget(footer, root.footer);
}

#[derive(Debug, Clone, Copy)]
struct RootLayout {
    tabs: Rect,
    body: Rect,
    footer: Rect,
}

fn root_layout(area: Rect) -> RootLayout {
    let root = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3),
            Constraint::Min(12),
            Constraint::Length(3),
        ])
        .split(area);
    RootLayout {
        tabs: root[0],
        body: root[1],
        footer: root[2],
    }
}

fn render_tabs(frame: &mut Frame, area: Rect, state: &TuiState) {
    let selected = Pane::ALL
        .iter()
        .position(|pane| *pane == state.pane)
        .unwrap_or(0);
    let titles = Pane::ALL
        .iter()
        .map(|pane| pane.tab_title())
        .collect::<Vec<_>>();
    let tabs = Tabs::new(titles)
        .block(panel_block("OCH"))
        .select(selected)
        .style(theme::tab())
        .highlight_style(theme::selected_tab())
        .divider(Span::styled("  ", theme::muted()));
    frame.render_widget(tabs, area);
}

fn screen_rect(size: Size) -> Rect {
    Rect::new(0, 0, size.width, size.height)
}

fn rect_contains(area: Rect, x: u16, y: u16) -> bool {
    x >= area.x
        && y >= area.y
        && x < area.x.saturating_add(area.width)
        && y < area.y.saturating_add(area.height)
}

fn content_row(area: Rect, x: u16, y: u16) -> Option<usize> {
    if area.width < 2 || area.height < 2 || !rect_contains(area, x, y) {
        return None;
    }
    let first_row = area.y.saturating_add(1);
    let last_row = area.y.saturating_add(area.height.saturating_sub(1));
    let inside_x = x > area.x && x < area.x.saturating_add(area.width.saturating_sub(1));
    if inside_x && y >= first_row && y < last_row {
        Some(usize::from(y - first_row))
    } else {
        None
    }
}

fn tab_at(area: Rect, x: u16, y: u16) -> Option<Pane> {
    if y != area.y.saturating_add(1) || !rect_contains(area, x, y) {
        return None;
    }
    let mut cursor = area.x.saturating_add(1);
    for pane in Pane::ALL {
        let width = pane.tab_title().chars().count() as u16;
        if x >= cursor && x < cursor.saturating_add(width) {
            return Some(pane);
        }
        cursor = cursor.saturating_add(width).saturating_add(2);
    }
    None
}

fn render_overview(frame: &mut Frame, area: Rect, state: &TuiState) {
    let include = main_config_includes_managed(&state.paths.main_ssh_config);
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(9), Constraint::Min(8)])
        .split(area);
    let cards = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage(34),
            Constraint::Percentage(33),
            Constraint::Percentage(33),
        ])
        .split(chunks[0]);
    render_lines(
        frame,
        cards[0],
        "VPN",
        vec![
            Line::from(state.connection_summary.clone()),
            Line::from(format!("Gateway: {}", empty_dash(&state.config.vpn_host))),
            Line::from(format!("User: {}", empty_dash(&state.config.vpn_user))),
            Line::from(format!("Target: {}", empty_dash(&state.config.target_host))),
        ],
    );
    render_lines(
        frame,
        cards[1],
        "Service",
        vec![
            Line::from(state.service_summary.clone()),
            Line::from(format!(
                "Auto refresh: {}",
                if state.auto_refresh { "on" } else { "off" }
            )),
            Line::from("Manual: r refresh current page"),
        ],
    );
    render_lines(
        frame,
        cards[2],
        "Config / SSH",
        vec![
            Line::from(format!(
                "Include: {}",
                if include { "installed" } else { "missing" }
            )),
            Line::from(format!(
                "SSH: {}",
                if state.ssh_enabled {
                    format!("{} -> {}", state.config.ssh_host, state.config.target_host)
                } else {
                    "disabled".to_string()
                }
            )),
            Line::from(format!("Routes: {}", state.config.routes_mode)),
            Line::from(format!("DNS: {}", state.config.dns_mode)),
            Line::from(format!("Config: {}", state.paths.config_file.display())),
        ],
    );

    let lower = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(48), Constraint::Percentage(52)])
        .split(chunks[1]);
    render_lines(
        frame,
        lower[0],
        "Actions",
        vec![
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
        ],
    );
    render_text(frame, lower[1], "Recent Logs", logs_or_placeholder(state));
}

fn render_connection(frame: &mut Frame, area: Rect, state: &TuiState) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(58), Constraint::Percentage(42)])
        .split(area);
    render_lines(
        frame,
        chunks[0],
        "Connection",
        vec![
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
            Line::raw(""),
            action_line(4, state.active, "连接 VPN", "och vpn connect"),
            action_line(5, state.active, "断开 VPN", "och vpn disconnect"),
            action_line(6, state.active, "探测认证组", "openconnect --authenticate"),
        ],
    );
    let side = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(8), Constraint::Min(8)])
        .split(chunks[1]);
    render_lines(
        frame,
        side[0],
        "Status",
        vec![
            Line::from(state.connection_summary.clone()),
            Line::from(format!(
                "Target host: {}",
                empty_dash(&state.config.target_host)
            )),
            Line::from(format!(
                "Target port: {}",
                empty_dash(&state.config.target_port)
            )),
            Line::from("Press r to refresh now"),
        ],
    );
    render_text(frame, side[1], "Recent Logs", logs_or_placeholder(state));
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
                let selected = state.active == 1 && state.selected_ssh == index;
                let marker = if selected { "> " } else { "  " };
                ListItem::new(Line::from(Span::styled(
                    format!("{marker}{host}"),
                    if selected {
                        theme::active_label()
                    } else {
                        theme::text()
                    },
                )))
            })
            .collect()
    };
    frame.render_widget(
        List::new(items)
            .style(theme::body())
            .block(panel_block(&format!("Import: {}", state.ssh_filter))),
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
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(58), Constraint::Percentage(42)])
        .split(area);
    render_lines(
        frame,
        chunks[0],
        "Routes & Proxy",
        vec![
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
            field_line(6, state.active, "DNS mode", &state.config.dns_mode, false),
        ],
    );
    render_lines(
        frame,
        chunks[1],
        "Route Summary",
        vec![
            Line::from(format!("Mode: {}", state.config.routes_mode)),
            Line::from(format!("DNS: {}", state.config.dns_mode)),
            Line::from(format!("Extra route count: {}", route_count(state))),
            Line::from(format!("Proxy: {}", yes_no(state.config.proxy_enabled))),
            Line::from(format!(
                "Local: {}:{}",
                state.config.proxy_local_host, state.config.proxy_local_port
            )),
            Line::from(format!("Remote port: {}", state.config.proxy_remote_port)),
            Line::raw(""),
            Line::from("Enter toggles route/proxy/DNS mode."),
            Line::from("Auto refresh skips this page while editing."),
        ],
    );
}

fn render_service(frame: &mut Frame, area: Rect, state: &TuiState) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(48), Constraint::Percentage(52)])
        .split(area);
    render_lines(
        frame,
        chunks[0],
        "Service",
        vec![
            action_line(0, state.active, "刷新状态", "och service status"),
            action_line(1, state.active, "安装服务", "och service install"),
            action_line(2, state.active, "卸载服务", "och service uninstall"),
            action_line(3, state.active, "刷新日志", "och vpn logs"),
            Line::raw(""),
            Line::from(format!("最近服务状态: {}", state.service_summary)),
            Line::from("非 macOS 或未 root 时，服务命令会直接显示不可用/权限错误。"),
        ],
    );
    render_text(frame, chunks[1], "Recent Logs", logs_or_placeholder(state));
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
        .style(theme::text())
        .block(panel_block("TOML"))
        .wrap(Wrap { trim: false });
    frame.render_widget(editor, chunks[1]);
}

fn render_logs(frame: &mut Frame, area: Rect, state: &TuiState) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(4), Constraint::Min(8)])
        .split(area);
    render_lines(
        frame,
        chunks[0],
        "Logs",
        vec![
            action_line(0, state.active, "刷新日志", "och vpn logs"),
            Line::from(format!(
                "Auto refresh: {} | r 刷新当前页 | a 开关自动刷新",
                if state.auto_refresh { "on" } else { "off" }
            )),
        ],
    );
    render_text(
        frame,
        chunks[1],
        "Runtime Log Tail",
        logs_or_placeholder(state),
    );
}

fn render_lines(frame: &mut Frame, area: Rect, title: &str, rows: Vec<Line<'static>>) {
    let widget = Paragraph::new(rows)
        .style(theme::body())
        .block(panel_block(title))
        .wrap(Wrap { trim: false });
    frame.render_widget(widget, area);
}

fn render_text(frame: &mut Frame, area: Rect, title: &str, text: String) {
    let widget = Paragraph::new(text)
        .style(text_style_for_title(title))
        .block(panel_block(title))
        .wrap(Wrap { trim: false });
    frame.render_widget(widget, area);
}

fn panel_block(title: &str) -> Block<'static> {
    Block::default()
        .title(Span::styled(title.to_string(), theme::panel_title()))
        .borders(Borders::ALL)
        .border_style(theme::panel_border())
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
    let label_style = if index == active {
        theme::active_label()
    } else {
        theme::label()
    };
    let value_style = if index == active {
        theme::active_value()
    } else {
        status_style(detail)
    };
    let prefix_style = if index == active {
        theme::active_label()
    } else {
        theme::muted()
    };
    let prefix = if index == active { "> " } else { "  " };
    Line::from(vec![
        Span::styled(prefix.to_string(), prefix_style),
        Span::styled(format!("{label}: "), label_style),
        Span::styled(detail.to_string(), value_style),
    ])
}

fn status_style(text: &str) -> Style {
    let lower = text.to_lowercase();
    if lower.contains("error")
        || lower.contains("fail")
        || text.contains("失败")
        || text.contains("错误")
        || text.contains("无效")
    {
        theme::danger()
    } else if lower.contains("missing")
        || lower.contains("disabled")
        || lower.contains("off")
        || lower == "no"
        || text.contains("确认")
        || text.contains("未")
        || text.contains("不可用")
    {
        theme::warning()
    } else if lower.contains("connected")
        || lower.contains("installed")
        || lower.contains("on")
        || lower == "yes"
        || text.contains("已")
        || text.contains("正常")
        || text.contains("成功")
    {
        theme::success()
    } else if text.trim().is_empty() || text == "-" || text == "<empty>" {
        theme::muted()
    } else {
        theme::text()
    }
}

fn text_style_for_title(title: &str) -> Style {
    if title.contains("Log") {
        theme::log()
    } else {
        theme::text()
    }
}

fn require_non_empty(label: &str, value: &str) -> Result<(), String> {
    if value.trim().is_empty() {
        Err(format!("{label}不能为空"))
    } else {
        Ok(())
    }
}

fn route_entries(text: &str) -> impl Iterator<Item = &str> {
    text.split(['\n', ' ', '\t', ','])
        .map(str::trim)
        .filter(|route| !route.is_empty())
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

fn logs_or_placeholder(state: &TuiState) -> String {
    if state.logs.trim().is_empty() {
        "暂无日志；按 r 或进入 Logs 页等待自动刷新。".to_string()
    } else {
        state.logs.clone()
    }
}

fn route_count(state: &TuiState) -> usize {
    route_entries(&state.extra_routes_text).count()
}

#[cfg(test)]
mod tests {
    use super::*;
    use ratatui::backend::TestBackend;
    use ratatui::style::Color;

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
            dns_mode: "openconnect".to_string(),
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
            auto_refresh: true,
            last_vpn_refresh: None,
            last_service_refresh: None,
            last_log_refresh: None,
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
        assert!(text.contains("1 Overview"));
        assert!(text.contains("Connection"));
        assert!(text.contains("VPN"));
        assert!(text.contains("Service"));
        assert!(text.contains("Config / SSH"));
        assert!(text.contains("Recent Logs"));
        assert!(text.contains("1-7 jump"));
        assert!(text.contains("Ctrl-S save"));
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
    fn horizontal_tabs_and_field_navigation_are_distinct() {
        let mut state = state_for_test();
        assert_eq!(state.pane, Pane::Overview);
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Right, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Connection);
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Down, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Connection);
        assert_eq!(state.active, 1);

        state
            .handle_key(KeyEvent::new(KeyCode::Up, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.active, 0);

        state
            .handle_key(KeyEvent::new(KeyCode::Left, KeyModifiers::NONE))
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
            .handle_key(KeyEvent::new(KeyCode::Tab, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.active, 2);
        assert_eq!(state.selected_ssh, 1);
    }

    #[test]
    fn number_keys_jump_between_panes_without_stealing_text_input() {
        let mut state = state_for_test();

        state
            .handle_key(KeyEvent::new(KeyCode::Char('4'), KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Routes);
        assert_eq!(state.active, 0);
        assert!(state.status.contains("Routes & Proxy"));

        state.pane = Pane::Connection;
        state.active = 0;
        state.config.vpn_host.clear();
        state
            .handle_key(KeyEvent::new(KeyCode::Char('7'), KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Connection);
        assert_eq!(state.config.vpn_host, "7");

        state.active = 4;
        state
            .handle_key(KeyEvent::new(KeyCode::Char('7'), KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.pane, Pane::Logs);
        assert_eq!(state.active, 0);
        assert!(state.status.contains("Logs"));
    }

    #[test]
    fn auto_refresh_shortcuts_do_not_steal_text_input() {
        let mut state = state_for_test();
        state.pane = Pane::Connection;
        state.active = 0;
        state.config.vpn_host.clear();

        state
            .handle_key(KeyEvent::new(KeyCode::Char('a'), KeyModifiers::NONE))
            .unwrap();
        state
            .handle_key(KeyEvent::new(KeyCode::Char('r'), KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.config.vpn_host, "ar");
        assert!(state.auto_refresh);

        state.active = 4;
        state
            .handle_key(KeyEvent::new(KeyCode::Char('a'), KeyModifiers::NONE))
            .unwrap();
        assert!(!state.auto_refresh);
    }

    #[test]
    fn mouse_clicks_switch_tabs_focus_fields_and_toggle_rows() {
        let mut state = state_for_test();
        let size = Size::new(100, 28);

        state
            .handle_mouse(left_click(36, 1), size)
            .expect("tab click should succeed");
        assert_eq!(state.pane, Pane::Routes);
        assert_eq!(state.active, 0);

        state
            .handle_mouse(left_click(2, 4), size)
            .expect("route mode click should succeed");
        assert_eq!(state.active, 0);
        assert_eq!(state.config.routes_mode, "extra");

        state
            .handle_mouse(left_click(2, 5), size)
            .expect("extra routes click should succeed");
        assert_eq!(state.active, 1);
        assert_eq!(state.config.routes_mode, "extra");

        state.pane = Pane::Connection;
        state.active = 4;
        state
            .handle_mouse(left_click(2, 6), size)
            .expect("field click should succeed");
        assert_eq!(state.active, 2);
    }

    #[test]
    fn mouse_click_imports_ssh_host_from_list() {
        let mut state = state_for_test();
        let size = Size::new(100, 28);
        state.pane = Pane::Ssh;
        state.ssh_hosts = vec!["alpha".to_string(), "beta".to_string()];

        state
            .handle_mouse(left_click(2, 5), size)
            .expect("host list click should succeed");

        assert_eq!(state.active, 1);
        assert_eq!(state.selected_ssh, 1);
        assert!(state.ssh_enabled);
        assert_eq!(state.config.ssh_host, setup::managed_alias("beta"));
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

    #[test]
    fn page_aware_refresh_targets_and_intervals_are_stable() {
        assert_eq!(
            refresh_targets_for_pane(Pane::Overview),
            [
                RefreshTarget::VpnStatus,
                RefreshTarget::ServiceStatus,
                RefreshTarget::LogTail
            ]
        );
        assert_eq!(
            refresh_targets_for_pane(Pane::Connection),
            [RefreshTarget::VpnStatus]
        );
        assert_eq!(
            refresh_targets_for_pane(Pane::Service),
            [RefreshTarget::ServiceStatus, RefreshTarget::LogTail]
        );
        assert_eq!(
            refresh_targets_for_pane(Pane::Logs),
            [RefreshTarget::VpnStatus, RefreshTarget::LogTail]
        );
        assert!(refresh_targets_for_pane(Pane::Config).is_empty());
        assert_eq!(
            refresh_interval(Pane::Logs, RefreshTarget::LogTail),
            LOGS_PANE_REFRESH_INTERVAL
        );
        assert_eq!(
            refresh_interval(Pane::Overview, RefreshTarget::LogTail),
            LOG_REFRESH_INTERVAL
        );
    }

    #[test]
    fn auto_refresh_toggle_updates_footer() {
        let mut state = state_for_test();
        state
            .handle_key(KeyEvent::new(KeyCode::Char('a'), KeyModifiers::NONE))
            .unwrap();
        assert!(!state.auto_refresh);

        let backend = TestBackend::new(100, 28);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let text = format!("{:?}", terminal.backend().buffer());
        assert!(text.contains("Auto: off"));
        assert!(text.contains("r refresh"));
    }

    #[test]
    fn rendered_tui_uses_color_theme() {
        let mut state = state_for_test();
        state.status = "VPN 已连接".to_string();
        state.connection_summary = "connected".to_string();

        let backend = TestBackend::new(100, 28);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let buffer = terminal.backend().buffer();

        assert!(buffer
            .content
            .iter()
            .any(|cell| cell.fg == Color::LightCyan));
        assert!(buffer
            .content
            .iter()
            .any(|cell| cell.fg == Color::LightGreen));
        assert!(buffer
            .content
            .iter()
            .any(|cell| cell.bg == Color::LightCyan));
    }

    #[test]
    fn logs_pane_uses_single_full_height_log_tail() {
        let mut state = state_for_test();
        state.pane = Pane::Logs;
        state.logs = "line one\nline two".to_string();

        state
            .handle_key(KeyEvent::new(KeyCode::Down, KeyModifiers::NONE))
            .unwrap();
        assert_eq!(state.active, 0);
        assert_eq!(state.pane, Pane::Logs);

        let backend = TestBackend::new(100, 28);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let text = format!("{:?}", terminal.backend().buffer());
        assert!(text.contains("Runtime Log Tail"));
        assert!(text.contains("line one"));
        assert!(text.contains("Auto refresh: on"));
        assert!(!text.contains("同上"));
    }

    fn left_click(column: u16, row: u16) -> MouseEvent {
        MouseEvent {
            kind: MouseEventKind::Down(MouseButton::Left),
            column,
            row,
            modifiers: KeyModifiers::NONE,
        }
    }
}
