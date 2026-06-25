use crate::config::{load_secret_password, parse_config_file, OchConfig};
use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyModifiers};
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Clear, List, ListItem, Paragraph, Wrap};
use ratatui::{Frame, Terminal};
use std::collections::BTreeSet;
use std::fs;
use std::io::{self, IsTerminal};
use std::net::{SocketAddr, ToSocketAddrs};
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Command;

const INCLUDE_LINE: &str = "Include ~/.ssh/och.config";

#[derive(Debug, Clone)]
pub(crate) struct SetupPaths {
    pub(crate) config_file: PathBuf,
    pub(crate) secrets_file: PathBuf,
    pub(crate) managed_ssh_config: PathBuf,
    pub(crate) main_ssh_config: PathBuf,
    pub(crate) och_bin: PathBuf,
}

#[derive(Debug, Clone)]
pub(crate) struct SetupDocument {
    pub(crate) config: OchConfig,
    pub(crate) vpn_password: String,
    pub(crate) paths: SetupPaths,
    pub(crate) write_ssh: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Step {
    Vpn,
    Ssh,
    Routes,
    Review,
}

#[derive(Debug, Clone)]
struct SetupState {
    config: OchConfig,
    vpn_password: String,
    route_cidr: String,
    paths: SetupPaths,
    step: Step,
    active: usize,
    ssh_filter: String,
    ssh_hosts: Vec<String>,
    selected_ssh: usize,
    ssh_enabled: bool,
    status: String,
    done: bool,
    canceled: bool,
}

pub(crate) fn run() -> Result<(), String> {
    if !io::stdin().is_terminal() || !io::stdout().is_terminal() {
        return Err("och setup 需要交互式终端。请手动编辑 ~/.config/och/config.toml 和 ~/.config/och/secrets.env，或在真实 TTY 中运行。".into());
    }

    let mut state = SetupState::load()?;
    let mut terminal = TerminalSession::enter()?;
    while !state.done && !state.canceled {
        terminal.draw(|frame| render(frame, &state))?;
        let Event::Key(key) = event::read().map_err(|error| error.to_string())? else {
            continue;
        };
        if let Err(error) = state.handle_key(key) {
            state.status = error;
        }
    }
    drop(terminal);

    if state.canceled {
        return Err("setup canceled".into());
    }

    let document = state.into_document()?;
    write_setup(&document)?;
    println!("已写入配置: {}", document.paths.config_file.display());
    println!("已写入 secret: {}", document.paths.secrets_file.display());
    if document.write_ssh {
        println!(
            "已写入 SSH Host: {}",
            document.paths.managed_ssh_config.display()
        );
        println!("默认连接: ssh {}", document.config.ssh_host);
    } else {
        println!("已跳过 SSH 配置；未修改 managed SSH config");
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

impl SetupState {
    fn load() -> Result<Self, String> {
        let paths = setup_paths()?;
        let (mut config, warning) = load_existing_config(&paths.config_file);
        if config.ssh_host.is_empty() {
            config.ssh_host = "och-target".to_string();
        }
        if config.target_port.is_empty() {
            config.target_port = "22".to_string();
        }

        let vpn_password = load_secret_password(&paths.secrets_file)
            .map_err(|error| error.to_string())?
            .unwrap_or_default();
        let route_cidr = config
            .routes_extra
            .first()
            .cloned()
            .or_else(|| default_cidr_for_host(&config.target_host))
            .unwrap_or_default();
        let ssh_hosts = list_ssh_hosts(&paths.main_ssh_config, &paths.managed_ssh_config);
        let ssh_enabled = has_managed_ssh_config(&config);

        Ok(Self {
            config,
            vpn_password,
            route_cidr,
            paths,
            step: Step::Vpn,
            active: 0,
            ssh_filter: String::new(),
            ssh_hosts,
            selected_ssh: 0,
            ssh_enabled,
            status: warning
                .unwrap_or_else(|| "Tab/方向键切换字段，Enter 下一步，Esc 退出".to_string()),
            done: false,
            canceled: false,
        })
    }

    fn handle_key(&mut self, key: KeyEvent) -> Result<(), String> {
        if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('c') {
            self.canceled = true;
            return Ok(());
        }
        match key.code {
            KeyCode::Esc => self.canceled = true,
            KeyCode::Tab => self.next_field(),
            KeyCode::BackTab => self.previous_field(),
            KeyCode::Down if self.step == Step::Ssh && self.active == 1 => {
                self.next_ssh_selection()
            }
            KeyCode::Up if self.step == Step::Ssh && self.active == 1 => {
                self.previous_ssh_selection()
            }
            KeyCode::Down => self.next_field(),
            KeyCode::Up => self.previous_field(),
            KeyCode::Left => self.previous_step(),
            KeyCode::Right => self.next_step()?,
            KeyCode::Enter => self.enter()?,
            KeyCode::Backspace => self.backspace(),
            KeyCode::Char(ch) => self.push_char(ch),
            _ => {}
        }
        Ok(())
    }

    fn enter(&mut self) -> Result<(), String> {
        if self.step == Step::Ssh && self.active == 0 {
            self.ssh_enabled = !self.ssh_enabled;
            if self.ssh_enabled
                && self.route_cidr.trim().is_empty()
                && !self.config.target_host.trim().is_empty()
            {
                self.route_cidr =
                    default_cidr_for_host(&self.config.target_host).unwrap_or_default();
            }
            self.status = self.step_hint();
            return Ok(());
        }
        if self.step == Step::Ssh && self.active == 1 {
            self.apply_selected_ssh_host();
            if self.route_cidr.trim().is_empty() {
                self.route_cidr =
                    default_cidr_for_host(&self.config.target_host).unwrap_or_default();
            }
            return Ok(());
        }
        if self.step == Step::Review {
            self.validate_all()?;
            self.done = true;
            return Ok(());
        }
        self.next_step()
    }

    fn next_step(&mut self) -> Result<(), String> {
        match self.step {
            Step::Vpn => {
                self.validate_vpn()?;
                self.step = Step::Ssh;
            }
            Step::Ssh => {
                self.validate_ssh()?;
                self.step = if self.ssh_enabled {
                    Step::Routes
                } else {
                    Step::Review
                };
            }
            Step::Routes => {
                self.validate_routes()?;
                self.step = Step::Review;
            }
            Step::Review => {}
        }
        self.active = 0;
        self.status = self.step_hint();
        Ok(())
    }

    fn previous_step(&mut self) {
        self.step = match self.step {
            Step::Vpn => Step::Vpn,
            Step::Ssh => Step::Vpn,
            Step::Routes => Step::Ssh,
            Step::Review if self.ssh_enabled => Step::Routes,
            Step::Review => Step::Ssh,
        };
        self.active = 0;
        self.status = self.step_hint();
    }

    fn next_field(&mut self) {
        self.active = (self.active + 1) % self.field_count();
        self.status = self.step_hint();
    }

    fn previous_field(&mut self) {
        let count = self.field_count();
        self.active = if self.active == 0 {
            count - 1
        } else {
            self.active - 1
        };
        self.status = self.step_hint();
    }

    fn field_count(&self) -> usize {
        match self.step {
            Step::Vpn => 4,
            Step::Ssh => 6,
            Step::Routes => 1,
            Step::Review => 1,
        }
    }

    fn push_char(&mut self, ch: char) {
        match (self.step, self.active) {
            (Step::Vpn, 0) => self.config.vpn_host.push(ch),
            (Step::Vpn, 1) => self.config.vpn_user.push(ch),
            (Step::Vpn, 2) => self.vpn_password.push(ch),
            (Step::Vpn, 3) => self.config.vpn_auth_group.push(ch),
            (Step::Ssh, 1) => {
                self.ssh_filter.push(ch);
                self.selected_ssh = 0;
            }
            (Step::Ssh, 2) => self.config.ssh_host.push(ch),
            (Step::Ssh, 3) => self.config.target_host.push(ch),
            (Step::Ssh, 4) => self.config.target_user.push(ch),
            (Step::Ssh, 5) => self.config.target_port.push(ch),
            (Step::Routes, 0) => self.route_cidr.push(ch),
            _ => {}
        }
    }

    fn backspace(&mut self) {
        match (self.step, self.active) {
            (Step::Vpn, 0) => {
                self.config.vpn_host.pop();
            }
            (Step::Vpn, 1) => {
                self.config.vpn_user.pop();
            }
            (Step::Vpn, 2) => {
                self.vpn_password.pop();
            }
            (Step::Vpn, 3) => {
                self.config.vpn_auth_group.pop();
            }
            (Step::Ssh, 1) => {
                self.ssh_filter.pop();
                self.selected_ssh = 0;
            }
            (Step::Ssh, 2) => {
                self.config.ssh_host.pop();
            }
            (Step::Ssh, 3) => {
                self.config.target_host.pop();
            }
            (Step::Ssh, 4) => {
                self.config.target_user.pop();
            }
            (Step::Ssh, 5) => {
                self.config.target_port.pop();
            }
            (Step::Routes, 0) => {
                self.route_cidr.pop();
            }
            _ => {}
        }
    }

    fn filtered_ssh_hosts(&self) -> Vec<&str> {
        let filter = self.ssh_filter.to_lowercase();
        self.ssh_hosts
            .iter()
            .filter(|host| filter.is_empty() || host.to_lowercase().contains(&filter))
            .map(String::as_str)
            .collect()
    }

    fn apply_selected_ssh_host(&mut self) {
        let hosts = self.filtered_ssh_hosts();
        let Some(host) = hosts.get(self.selected_ssh).copied() else {
            self.status = "没有匹配的 SSH Host；可继续手动填写".to_string();
            return;
        };
        let host = host.to_string();
        let resolved = resolve_ssh_host(&self.paths.main_ssh_config, &host);
        self.config.ssh_host = managed_alias(&host);
        self.config.target_host = resolved.host;
        self.config.target_user = resolved.user;
        self.config.target_port = resolved.port;
        self.status = format!("已选择 SSH Host: {host}");
    }

    fn validate_vpn(&mut self) -> Result<(), String> {
        require_non_empty("VPN 网关", &self.config.vpn_host)?;
        require_non_empty("VPN 用户", &self.config.vpn_user)?;
        require_non_empty("VPN 密码", &self.vpn_password)?;
        Ok(())
    }

    fn validate_ssh(&mut self) -> Result<(), String> {
        if !self.ssh_enabled {
            return Ok(());
        }
        require_non_empty("托管 Host 别名", &self.config.ssh_host)?;
        require_non_empty("HostName/IP", &self.config.target_host)?;
        require_non_empty("SSH 用户", &self.config.target_user)?;
        validate_port(&self.config.target_port)?;
        Ok(())
    }

    fn validate_routes(&mut self) -> Result<(), String> {
        if !self.ssh_enabled {
            return Ok(());
        }
        if !self.route_cidr.trim().is_empty() && !valid_cidr(self.route_cidr.trim()) {
            return Err(format!("无效 CIDR: {}", self.route_cidr.trim()));
        }
        Ok(())
    }

    fn validate_all(&mut self) -> Result<(), String> {
        self.validate_vpn()?;
        self.validate_ssh()?;
        self.validate_routes()
    }

    fn into_document(mut self) -> Result<SetupDocument, String> {
        self.validate_all()?;
        self.config.vpn_host = self.config.vpn_host.trim().to_string();
        self.config.vpn_user = self.config.vpn_user.trim().to_string();
        self.config.vpn_auth_group = self.config.vpn_auth_group.trim().to_string();
        self.config.ssh_host = self.config.ssh_host.trim().to_string();
        self.config.target_host = self.config.target_host.trim().to_string();
        self.config.target_user = self.config.target_user.trim().to_string();
        self.config.target_port = self.config.target_port.trim().to_string();
        if self.ssh_enabled && !self.route_cidr.trim().is_empty() {
            self.config.routes_mode = "extra".to_string();
            self.config.routes_extra =
                append_route(&self.config.routes_extra, self.route_cidr.trim());
        } else if self.config.routes_extra.is_empty() {
            self.config.routes_mode = "openconnect".to_string();
        }
        Ok(SetupDocument {
            config: self.config,
            vpn_password: self.vpn_password,
            paths: self.paths,
            write_ssh: self.ssh_enabled,
        })
    }

    fn step_hint(&self) -> String {
        match self.step {
            Step::Vpn => "填写 VPN 信息。密码会保存到 secrets.env，不写入 TOML。".to_string(),
            Step::Ssh => {
                if self.ssh_enabled {
                    "SSH 已启用；Enter 可切换启用状态或回填选中 Host。".to_string()
                } else {
                    "SSH 已跳过；按 Enter 可重新启用，Right 直接进入确认。".to_string()
                }
            }
            Step::Routes => "目标 CIDR 可留空；填写后会作为额外 VPN 路由写入。".to_string(),
            Step::Review => "确认摘要无误后按 Enter 写入；Left 返回修改。".to_string(),
        }
    }

    fn next_ssh_selection(&mut self) {
        let count = self.filtered_ssh_hosts().len();
        if count == 0 {
            return;
        }
        self.selected_ssh = (self.selected_ssh + 1).min(count - 1);
    }

    fn previous_ssh_selection(&mut self) {
        self.selected_ssh = self.selected_ssh.saturating_sub(1);
    }
}

pub(crate) fn load_existing_config(path: &Path) -> (OchConfig, Option<String>) {
    if !path.is_file() {
        return (OchConfig::default(), None);
    }
    match parse_config_file(&path.to_path_buf(), false) {
        Ok(config) => (config, None),
        Err(error) => (
            OchConfig::default(),
            Some(format!("现有配置无法解析，将使用默认值重新生成: {error}")),
        ),
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct ResolvedSshHost {
    pub(crate) host: String,
    pub(crate) user: String,
    pub(crate) port: String,
}

fn render(frame: &mut Frame, state: &SetupState) {
    let area = frame.area();
    let outer = Block::default()
        .title("OCH setup")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Blue));
    frame.render_widget(outer, area);

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3),
            Constraint::Min(10),
            Constraint::Length(3),
        ])
        .margin(1)
        .split(area);

    render_steps(frame, chunks[0], state.step);
    match state.step {
        Step::Vpn => render_vpn(frame, chunks[1], state),
        Step::Ssh => render_ssh(frame, chunks[1], state),
        Step::Routes => render_routes(frame, chunks[1], state),
        Step::Review => render_review(frame, chunks[1], state),
    }
    let footer = Paragraph::new(state.status.as_str())
        .block(Block::default().borders(Borders::TOP))
        .wrap(Wrap { trim: true });
    frame.render_widget(footer, chunks[2]);
}

fn render_steps(frame: &mut Frame, area: Rect, step: Step) {
    let steps = [
        (Step::Vpn, "1 VPN"),
        (Step::Ssh, "2 SSH"),
        (Step::Routes, "3 Routes"),
        (Step::Review, "4 Review"),
    ];
    let line = Line::from(
        steps
            .iter()
            .flat_map(|(item, label)| {
                let style = if *item == step {
                    Style::default()
                        .fg(Color::Yellow)
                        .add_modifier(Modifier::BOLD)
                } else {
                    Style::default().fg(Color::Gray)
                };
                [Span::styled(*label, style), Span::raw("   ")]
            })
            .collect::<Vec<_>>(),
    );
    frame.render_widget(Paragraph::new(line), area);
}

fn render_vpn(frame: &mut Frame, area: Rect, state: &SetupState) {
    let rows = vec![
        field_line("VPN 网关", &state.config.vpn_host, state.active == 0, false),
        field_line("VPN 用户", &state.config.vpn_user, state.active == 1, false),
        field_line("VPN 密码", &state.vpn_password, state.active == 2, true),
        field_line(
            "认证组",
            &state.config.vpn_auth_group,
            state.active == 3,
            false,
        ),
    ];
    let widget = Paragraph::new(rows)
        .block(Block::default().title("VPN").borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(widget, area);
}

fn render_ssh(frame: &mut Frame, area: Rect, state: &SetupState) {
    let chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(38), Constraint::Percentage(62)])
        .split(area);
    let hosts = state.filtered_ssh_hosts();
    let items = if hosts.is_empty() {
        vec![ListItem::new("无匹配；请手动填写")]
    } else {
        hosts
            .iter()
            .enumerate()
            .take(12)
            .map(|(index, host)| {
                let marker = if state.active == 1 && index == state.selected_ssh {
                    "> "
                } else {
                    "  "
                };
                ListItem::new(format!("{marker}{host}"))
            })
            .collect()
    };
    let list = List::new(items).block(
        Block::default()
            .title(format!("SSH Host 搜索: {}", state.ssh_filter))
            .borders(Borders::ALL),
    );
    frame.render_widget(list, chunks[0]);

    let rows = vec![
        field_line(
            "启用 SSH",
            if state.ssh_enabled { "yes" } else { "no" },
            state.active == 0,
            false,
        ),
        Line::from("左侧搜索框获得焦点时，Enter 回填选中 Host"),
        Line::raw(""),
        field_line(
            "托管 Host 别名",
            &state.config.ssh_host,
            state.active == 2,
            false,
        ),
        field_line(
            "HostName/IP",
            &state.config.target_host,
            state.active == 3,
            false,
        ),
        field_line(
            "SSH 用户",
            &state.config.target_user,
            state.active == 4,
            false,
        ),
        field_line(
            "SSH 端口",
            &state.config.target_port,
            state.active == 5,
            false,
        ),
    ];
    let form = Paragraph::new(rows)
        .block(Block::default().title("SSH").borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(form, chunks[1]);
}

fn render_routes(frame: &mut Frame, area: Rect, state: &SetupState) {
    let rows = vec![
        field_line("目标路由 CIDR", &state.route_cidr, state.active == 0, false),
        Line::raw(""),
        Line::from(format!("当前目标 Host: {}", state.config.target_host)),
        Line::from("留空则只使用 OpenConnect 原生路由。"),
    ];
    let widget = Paragraph::new(rows)
        .block(Block::default().title("Routes").borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(widget, area);
}

fn render_review(frame: &mut Frame, area: Rect, state: &SetupState) {
    let rows = vec![
        Line::from(format!("配置文件: {}", state.paths.config_file.display())),
        Line::from(format!("Secret: {}", state.paths.secrets_file.display())),
        Line::from(if state.ssh_enabled {
            format!("托管 SSH: {}", state.paths.managed_ssh_config.display())
        } else {
            "托管 SSH: 跳过，不修改旧文件".to_string()
        }),
        Line::raw(""),
        Line::from(format!(
            "VPN: {} / {}",
            state.config.vpn_host, state.config.vpn_user
        )),
        Line::from(if state.ssh_enabled {
            format!(
                "SSH: {} -> {}:{}",
                state.config.ssh_host, state.config.target_host, state.config.target_port
            )
        } else {
            "SSH: disabled".to_string()
        }),
        Line::from(if state.ssh_enabled {
            format!("SSH 用户: {}", state.config.target_user)
        } else {
            "SSH 用户: -".to_string()
        }),
        Line::from(format!(
            "路由: {}",
            if !state.ssh_enabled || state.route_cidr.trim().is_empty() {
                "openconnect".to_string()
            } else {
                state.route_cidr.trim().to_string()
            }
        )),
        Line::raw(""),
        Line::from("VPN 密码不会显示，也不会写入 config.toml。"),
        Line::from("按 Enter 写入，Left 返回。"),
    ];
    let widget = Paragraph::new(rows)
        .block(Block::default().title("Review").borders(Borders::ALL))
        .wrap(Wrap { trim: false });
    frame.render_widget(Clear, area);
    frame.render_widget(widget, area);
}

fn field_line(label: &str, value: &str, active: bool, secret: bool) -> Line<'static> {
    let prefix = if active { "> " } else { "  " };
    let shown = if secret {
        "*".repeat(value.chars().count())
    } else if value.is_empty() {
        "<empty>".to_string()
    } else {
        value.to_string()
    };
    let style = if active {
        Style::default()
            .fg(Color::Yellow)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default()
    };
    Line::from(vec![
        Span::styled(format!("{prefix}{label}: "), style),
        Span::raw(shown),
    ])
}

pub(crate) fn setup_paths() -> Result<SetupPaths, String> {
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
    let config_file = std::env::var("OCH_CONFIG_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".config/och/config.toml"));
    let secrets_file = std::env::var("OCH_SECRETS_FILE")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".config/och/secrets.env"));
    let managed_ssh_config = std::env::var("OCH_MANAGED_SSH_CONFIG")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".ssh/och.config"));
    let main_ssh_config = std::env::var("OCH_MAIN_SSH_CONFIG")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(&home).join(".ssh/config"));
    let och_bin = std::env::current_exe()
        .map_err(|error| error.to_string())
        .unwrap_or_else(|_| PathBuf::from("och"));

    Ok(SetupPaths {
        config_file,
        secrets_file,
        managed_ssh_config,
        main_ssh_config,
        och_bin,
    })
}

pub(crate) fn render_config_toml(config: &OchConfig) -> String {
    let mut routes = String::new();
    for (index, route) in config.routes_extra.iter().enumerate() {
        if index > 0 {
            routes.push_str(", ");
        }
        routes.push_str(&quote_toml(route));
    }
    let proxy_section = if config.proxy_enabled {
        format!(
            "\n[proxy]\nlocal_host = {}\nlocal_port = {}\nremote_port = {}\n",
            quote_toml(&config.proxy_local_host),
            quote_toml(&config.proxy_local_port),
            quote_toml(&config.proxy_remote_port)
        )
    } else {
        String::new()
    };

    format!(
        "\
# Generated by OCH. VPN password is stored separately, not in this file.

[vpn]
host = {}
user = {}
auth_group = {}

[ssh]
host = {}
target_host = {}
user = {}
port = {}

[routes]
mode = {}
extra = [{}]

[dns]
mode = {}
{}
[paths]
# Runtime helper paths are fixed by the installed app or CLI layout.

[app]
language = {}
",
        quote_toml(&config.vpn_host),
        quote_toml(&config.vpn_user),
        quote_toml(&config.vpn_auth_group),
        quote_toml(&config.ssh_host),
        quote_toml(&config.target_host),
        quote_toml(&config.target_user),
        quote_toml(&config.target_port),
        quote_toml(&config.routes_mode),
        routes,
        quote_toml(&config.dns_mode),
        proxy_section,
        quote_toml(&config.app_language)
    )
}

pub(crate) fn render_secrets(password: &str) -> String {
    format!("VPN_PASSWORD={}\n", quote_toml(password))
}

pub(crate) fn render_managed_ssh_config(config: &OchConfig, och_bin: &Path) -> String {
    format!(
        "\
# Generated by OCH. Edit this file from the OCH app or `och setup`.
Host {}
  HostName {}
  User {}
  Port {}
  ProxyCommand {} proxy-command %h %p
  ServerAliveInterval 30
  ServerAliveCountMax 3

",
        config.ssh_host,
        config.target_host,
        config.target_user,
        config.target_port,
        quote_ssh_config(&och_bin.display().to_string())
    )
}

pub(crate) fn write_setup(document: &SetupDocument) -> Result<(), String> {
    write_private_file(
        &document.paths.config_file,
        &render_config_toml(&document.config),
        0o600,
    )?;
    write_private_file(
        &document.paths.secrets_file,
        &render_secrets(&document.vpn_password),
        0o600,
    )?;
    if document.write_ssh {
        write_managed_ssh_config(&document.config, &document.paths)?;
        ensure_include_line(&document.paths.main_ssh_config, INCLUDE_LINE)?;
    }
    Ok(())
}

pub(crate) fn write_managed_ssh_config(
    config: &OchConfig,
    paths: &SetupPaths,
) -> Result<(), String> {
    let dir = paths
        .managed_ssh_config
        .parent()
        .ok_or_else(|| "invalid managed SSH config path".to_string())?;
    ensure_dir(dir, 0o700)?;
    let tmp = paths.managed_ssh_config.with_extension("config.tmp");
    fs::write(&tmp, render_managed_ssh_config(config, &paths.och_bin))
        .map_err(|error| error.to_string())?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(0o600))
        .map_err(|error| error.to_string())?;
    if let Err(error) = validate_ssh_config(&tmp, &config.ssh_host) {
        let _ = fs::remove_file(&tmp);
        return Err(error);
    }
    fs::rename(&tmp, &paths.managed_ssh_config).map_err(|error| error.to_string())?;
    fs::set_permissions(&paths.managed_ssh_config, fs::Permissions::from_mode(0o600))
        .map_err(|error| error.to_string())
}

pub(crate) fn ensure_include_line(path: &Path, include_line: &str) -> Result<(), String> {
    if path.is_file() {
        let current = fs::read_to_string(path).map_err(|error| error.to_string())?;
        if current.lines().any(|line| line.trim() == include_line) {
            return Ok(());
        }
    }

    let dir = path
        .parent()
        .ok_or_else(|| "invalid SSH config path".to_string())?;
    ensure_dir(dir, 0o700)?;
    let tmp = path.with_extension("config.include.tmp");
    fs::write(&tmp, format!("{include_line}\n")).map_err(|error| error.to_string())?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(0o600))
        .map_err(|error| error.to_string())?;
    if let Err(error) = validate_ssh_config(&tmp, "__och_validation_probe__") {
        let _ = fs::remove_file(&tmp);
        return Err(error);
    }
    let _ = fs::remove_file(&tmp);

    let mut next = String::new();
    if path.is_file() {
        next = fs::read_to_string(path).map_err(|error| error.to_string())?;
        if !next.is_empty() && !next.ends_with('\n') {
            next.push('\n');
        }
    }
    next.push_str(include_line);
    next.push('\n');
    write_private_file(path, &next, 0o600)
}

pub(crate) fn write_private_file(path: &Path, content: &str, mode: u32) -> Result<(), String> {
    let dir = path
        .parent()
        .ok_or_else(|| format!("invalid path: {}", path.display()))?;
    ensure_dir(dir, 0o700)?;
    fs::write(path, content).map_err(|error| error.to_string())?;
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).map_err(|error| error.to_string())
}

fn ensure_dir(path: &Path, mode: u32) -> Result<(), String> {
    fs::create_dir_all(path).map_err(|error| error.to_string())?;
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).map_err(|error| error.to_string())
}

fn validate_ssh_config(path: &Path, host: &str) -> Result<(), String> {
    let output = Command::new("ssh")
        .arg("-F")
        .arg(path)
        .arg("-G")
        .arg(host)
        .output()
        .map_err(|error| format!("cannot run ssh to validate generated config: {error}"))?;
    if output.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&output.stderr);
    Err(format!(
        "generated SSH config failed validation: {}",
        stderr.trim()
    ))
}

pub(crate) fn list_ssh_hosts(main_config: &Path, managed_config: &Path) -> Vec<String> {
    let mut seen = BTreeSet::new();
    collect_ssh_hosts(main_config, managed_config, 0, &mut seen);
    seen.into_iter().collect()
}

fn collect_ssh_hosts(
    path: &Path,
    managed_config: &Path,
    depth: usize,
    seen: &mut BTreeSet<String>,
) {
    if depth >= 5 || !path.is_file() || same_path(path, managed_config) {
        return;
    }
    let Ok(raw) = fs::read_to_string(path) else {
        return;
    };
    let base_dir = path.parent().unwrap_or_else(|| Path::new("."));
    for line in raw.lines() {
        let line = strip_ssh_comment(line).trim().to_string();
        if line.is_empty() {
            continue;
        }
        let mut parts = line.split_whitespace();
        let Some(keyword) = parts.next() else {
            continue;
        };
        match keyword.to_ascii_lowercase().as_str() {
            "host" => {
                for token in parts {
                    if token.contains('*') || token.contains('?') || token.starts_with('!') {
                        continue;
                    }
                    if token.starts_with("och-") {
                        continue;
                    }
                    seen.insert(strip_quotes(token));
                }
            }
            "include" => {
                for token in parts {
                    for candidate in include_candidates(&strip_quotes(token), base_dir) {
                        collect_ssh_hosts(&candidate, managed_config, depth + 1, seen);
                    }
                }
            }
            _ => {}
        }
    }
}

fn include_candidates(token: &str, base_dir: &Path) -> Vec<PathBuf> {
    let expanded = expand_ssh_path(token, base_dir);
    let pattern = expanded.display().to_string();
    if pattern.contains('*') || pattern.contains('?') || pattern.contains('[') {
        glob::glob(&pattern)
            .ok()
            .into_iter()
            .flatten()
            .filter_map(Result::ok)
            .filter(|path| path.is_file())
            .collect()
    } else if expanded.is_file() {
        vec![expanded]
    } else {
        Vec::new()
    }
}

fn expand_ssh_path(value: &str, base_dir: &Path) -> PathBuf {
    if value == "~" {
        return PathBuf::from(std::env::var("HOME").unwrap_or_else(|_| ".".to_string()));
    }
    if let Some(rest) = value.strip_prefix("~/") {
        return PathBuf::from(std::env::var("HOME").unwrap_or_else(|_| ".".to_string())).join(rest);
    }
    let path = PathBuf::from(value);
    if path.is_absolute() {
        path
    } else {
        base_dir.join(path)
    }
}

pub(crate) fn resolve_ssh_host(main_config: &Path, host: &str) -> ResolvedSshHost {
    let mut command = Command::new("ssh");
    if main_config.is_file() {
        command.arg("-F").arg(main_config);
    }
    let output = command.arg("-G").arg(host).output().ok();
    let text = output
        .filter(|output| output.status.success())
        .map(|output| String::from_utf8_lossy(&output.stdout).to_string())
        .unwrap_or_default();
    let mut resolved = ResolvedSshHost {
        host: host.to_string(),
        user: std::env::var("USER").unwrap_or_default(),
        port: "22".to_string(),
    };
    for line in text.lines() {
        let mut parts = line.split_whitespace();
        let Some(key) = parts.next() else {
            continue;
        };
        let Some(value) = parts.next() else {
            continue;
        };
        match key {
            "hostname" => resolved.host = value.to_string(),
            "user" => resolved.user = value.to_string(),
            "port" => resolved.port = value.to_string(),
            _ => {}
        }
    }
    resolved
}

pub(crate) fn managed_alias(host: &str) -> String {
    if host.starts_with("och-") {
        host.to_string()
    } else {
        format!("och-{host}")
    }
}

pub(crate) fn append_route(existing: &[String], route: &str) -> Vec<String> {
    let mut routes = existing.to_vec();
    if !route.is_empty() && !routes.iter().any(|item| item == route) {
        routes.push(route.to_string());
    }
    routes
}

pub(crate) fn default_cidr_for_host(host: &str) -> Option<String> {
    let ip = first_ipv4_for_host(host)?;
    Some(format!("{ip}/32"))
}

fn first_ipv4_for_host(host: &str) -> Option<String> {
    if valid_ipv4(host) {
        return Some(host.to_string());
    }
    let addrs = (host, 0).to_socket_addrs().ok()?;
    addrs
        .filter_map(|addr| match addr {
            SocketAddr::V4(v4) => Some(v4.ip().to_string()),
            SocketAddr::V6(_) => None,
        })
        .next()
}

pub(crate) fn valid_cidr(value: &str) -> bool {
    let Some((ip, prefix)) = value.split_once('/') else {
        return false;
    };
    valid_ipv4(ip)
        && prefix
            .parse::<u8>()
            .map(|prefix| prefix <= 32)
            .unwrap_or(false)
}

fn valid_ipv4(value: &str) -> bool {
    let parts: Vec<&str> = value.split('.').collect();
    parts.len() == 4 && parts.iter().all(|part| part.parse::<u8>().is_ok())
}

pub(crate) fn validate_port(value: &str) -> Result<(), String> {
    match value.trim().parse::<u16>() {
        Ok(port) if port > 0 => Ok(()),
        _ => Err(format!("无效 SSH 端口: {value}")),
    }
}

fn require_non_empty(label: &str, value: &str) -> Result<(), String> {
    if value.trim().is_empty() {
        Err(format!("{label}不能为空"))
    } else {
        Ok(())
    }
}

pub(crate) fn has_managed_ssh_config(config: &OchConfig) -> bool {
    !config.ssh_host.trim().is_empty()
        && !config.target_host.trim().is_empty()
        && !config.target_user.trim().is_empty()
        && !config.target_port.trim().is_empty()
}

fn quote_toml(value: &str) -> String {
    let escaped = value.replace('\\', "\\\\").replace('"', "\\\"");
    format!("\"{escaped}\"")
}

fn quote_ssh_config(value: &str) -> String {
    let escaped = value.replace('\\', "\\\\").replace('"', "\\\"");
    format!("\"{escaped}\"")
}

fn strip_quotes(value: &str) -> String {
    value
        .strip_prefix('"')
        .and_then(|value| value.strip_suffix('"'))
        .or_else(|| {
            value
                .strip_prefix('\'')
                .and_then(|value| value.strip_suffix('\''))
        })
        .unwrap_or(value)
        .to_string()
}

fn strip_ssh_comment(line: &str) -> String {
    let mut result = String::new();
    let mut in_single = false;
    let mut in_double = false;
    for ch in line.chars() {
        match ch {
            '\'' if !in_double => in_single = !in_single,
            '"' if !in_single => in_double = !in_double,
            '#' if !in_single && !in_double => break,
            _ => {}
        }
        result.push(ch);
    }
    result
}

fn same_path(left: &Path, right: &Path) -> bool {
    match (left.canonicalize(), right.canonicalize()) {
        (Ok(left), Ok(right)) => left == right,
        _ => left == right,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ratatui::backend::TestBackend;

    fn sample_config() -> OchConfig {
        OchConfig {
            vpn_host: "vpn.example.com".to_string(),
            vpn_user: "alice".to_string(),
            vpn_auth_group: "staff".to_string(),
            ssh_host: "och-app".to_string(),
            target_host: "10.2.3.4".to_string(),
            target_user: "alice".to_string(),
            target_port: "22".to_string(),
            routes_mode: "extra".to_string(),
            routes_extra: vec!["10.2.3.4/32".to_string()],
            dns_mode: "openconnect".to_string(),
            proxy_enabled: false,
            proxy_local_host: "127.0.0.1".to_string(),
            proxy_local_port: "7890".to_string(),
            proxy_remote_port: "7890".to_string(),
            app_language: "system".to_string(),
        }
    }

    #[test]
    fn renders_config_without_password() {
        let rendered = render_config_toml(&sample_config());
        assert!(rendered.contains("host = \"vpn.example.com\""));
        assert!(rendered.contains("mode = \"extra\""));
        assert!(!rendered.contains("secret-password"));
    }

    #[test]
    fn bad_existing_config_falls_back_to_defaults() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "[vpn]\nhost = \"vpn.example.com\"\n  1) ECNU\"\n").unwrap();

        let (config, warning) = load_existing_config(&path);

        assert_eq!(config, OchConfig::default());
        assert!(warning.unwrap().contains("现有配置无法解析"));
    }

    #[test]
    fn renders_secrets_with_0600_writer() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("secrets.env");
        write_private_file(&path, &render_secrets("secret"), 0o600).unwrap();
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            "VPN_PASSWORD=\"secret\"\n"
        );
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }

    #[test]
    fn vpn_only_setup_does_not_touch_managed_ssh_config() {
        let dir = tempfile::tempdir().unwrap();
        let managed = dir.path().join("och.config");
        fs::write(&managed, "keep-existing").unwrap();
        let document = SetupDocument {
            config: OchConfig {
                vpn_host: "vpn.example.com".to_string(),
                vpn_user: "alice".to_string(),
                ..OchConfig::default()
            },
            vpn_password: "secret".to_string(),
            paths: SetupPaths {
                config_file: dir.path().join("config.toml"),
                secrets_file: dir.path().join("secrets.env"),
                managed_ssh_config: managed.clone(),
                main_ssh_config: dir.path().join("ssh-config"),
                och_bin: PathBuf::from("och"),
            },
            write_ssh: false,
        };

        write_setup(&document).unwrap();

        assert!(document.paths.config_file.is_file());
        assert!(document.paths.secrets_file.is_file());
        assert_eq!(fs::read_to_string(managed).unwrap(), "keep-existing");
        assert!(!document.paths.main_ssh_config.exists());
    }

    #[test]
    fn validates_cidr_and_default_route() {
        assert_eq!(
            default_cidr_for_host("10.2.3.4").as_deref(),
            Some("10.2.3.4/32")
        );
        assert!(valid_cidr("10.2.3.4/32"));
        assert!(!valid_cidr("10.2.3.999/32"));
        assert!(!valid_cidr("10.2.3.4/33"));
        assert_eq!(
            append_route(&["10.0.0.0/8".to_string()], "10.2.3.4/32"),
            ["10.0.0.0/8", "10.2.3.4/32"]
        );
    }

    #[test]
    fn discovers_ssh_hosts_and_managed_alias() {
        let dir = tempfile::tempdir().unwrap();
        let ssh_dir = dir.path().join(".ssh");
        let conf_dir = ssh_dir.join("conf.d");
        fs::create_dir_all(&conf_dir).unwrap();
        fs::write(
            ssh_dir.join("config"),
            "Host app *.wild !blocked\n  HostName app.internal\n\nInclude conf.d/*.conf\nInclude ~/.ssh/och.config\n",
        )
        .unwrap();
        fs::write(
            conf_dir.join("extra.conf"),
            "Host db\n  HostName 10.0.0.20\n\nHost och-managed\n  HostName 10.0.0.99\n",
        )
        .unwrap();
        fs::write(ssh_dir.join("och.config"), "Host should-not-appear\n").unwrap();
        std::env::set_var("HOME", dir.path());

        let hosts = list_ssh_hosts(&ssh_dir.join("config"), &ssh_dir.join("och.config"));
        assert_eq!(hosts, ["app", "db"]);
        assert_eq!(managed_alias("app"), "och-app");
        assert_eq!(managed_alias("och-app"), "och-app");
    }

    #[test]
    fn managed_ssh_validation_failure_does_not_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let bin = dir.path().join("bin");
        fs::create_dir_all(&bin).unwrap();
        let ssh = bin.join("ssh");
        fs::write(
            &ssh,
            "#!/usr/bin/env bash\necho bad ssh config >&2\nexit 255\n",
        )
        .unwrap();
        fs::set_permissions(&ssh, fs::Permissions::from_mode(0o755)).unwrap();
        let old_path = std::env::var("PATH").unwrap_or_default();
        std::env::set_var("PATH", format!("{}:{old_path}", bin.display()));

        let managed = dir.path().join("och.config");
        fs::write(&managed, "keep-existing").unwrap();
        let paths = SetupPaths {
            config_file: dir.path().join("config.toml"),
            secrets_file: dir.path().join("secrets.env"),
            managed_ssh_config: managed.clone(),
            main_ssh_config: dir.path().join("config"),
            och_bin: PathBuf::from("/usr/local/bin/och"),
        };

        let error = write_managed_ssh_config(&sample_config(), &paths).unwrap_err();
        assert!(error.contains("generated SSH config failed validation"));
        assert_eq!(fs::read_to_string(managed).unwrap(), "keep-existing");
        std::env::set_var("PATH", old_path);
    }

    #[test]
    fn renders_initial_tui() {
        let dir = tempfile::tempdir().unwrap();
        let state = SetupState {
            config: sample_config(),
            vpn_password: "secret".to_string(),
            route_cidr: "10.2.3.4/32".to_string(),
            paths: SetupPaths {
                config_file: dir.path().join("config.toml"),
                secrets_file: dir.path().join("secrets.env"),
                managed_ssh_config: dir.path().join("och.config"),
                main_ssh_config: dir.path().join("ssh-config"),
                och_bin: PathBuf::from("/usr/local/bin/och"),
            },
            step: Step::Vpn,
            active: 0,
            ssh_filter: String::new(),
            ssh_hosts: vec![],
            selected_ssh: 0,
            ssh_enabled: true,
            status: "ready".to_string(),
            done: false,
            canceled: false,
        };
        let backend = TestBackend::new(80, 24);
        let mut terminal = Terminal::new(backend).unwrap();
        terminal.draw(|frame| render(frame, &state)).unwrap();
        let buffer = terminal.backend().buffer();
        let text = format!("{buffer:?}");
        assert!(text.contains("OCH setup"));
        assert!(text.contains("VPN 网关"));
    }

    #[test]
    fn empty_hostname_blocks_review() {
        let mut state = SetupState {
            config: sample_config(),
            vpn_password: "secret".to_string(),
            route_cidr: "10.2.3.4/32".to_string(),
            paths: setup_paths().unwrap_or_else(|_| SetupPaths {
                config_file: PathBuf::from("config.toml"),
                secrets_file: PathBuf::from("secrets.env"),
                managed_ssh_config: PathBuf::from("och.config"),
                main_ssh_config: PathBuf::from("ssh-config"),
                och_bin: PathBuf::from("och"),
            }),
            step: Step::Ssh,
            active: 0,
            ssh_filter: String::new(),
            ssh_hosts: vec![],
            selected_ssh: 0,
            ssh_enabled: true,
            status: String::new(),
            done: false,
            canceled: false,
        };
        state.config.target_host.clear();
        assert!(state.next_step().unwrap_err().contains("HostName/IP"));
        assert_eq!(state.step, Step::Ssh);
    }
}
