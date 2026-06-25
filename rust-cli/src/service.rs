use crate::config::{OchConfig, Runtime, RuntimeTarget};
use crate::platform::{
    command_output, discover_openconnect_pid, find_tool, is_macos, process_looks_like_openconnect,
    require_tool, tail_lines,
};
use crate::route_wrapper_path;
use crate::vpn::{parse_linux_iface, parse_macos_iface, parse_macos_route_line};
use serde::{Deserialize, Serialize};
use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::net::Shutdown;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::thread;
use std::time::Duration;

pub const ACTION_CONNECT: &str = "connect";
pub const ACTION_DISCONNECT: &str = "disconnect";
pub const ACTION_STATUS: &str = "status";
pub const ACTION_LOGS: &str = "logs";

const LAUNCHD_LABEL: &str = "io.github.imyangliu.och.service";
const PLIST_PATH: &str = "/Library/LaunchDaemons/io.github.imyangliu.och.service.plist";
const DEFAULT_SOCKET_PATH: &str = "/var/run/och/daemon.sock";
const SERVICE_RUN_DIR: &str = "/var/run/och";
const SERVICE_LOG_DIR: &str = "/var/log/och";
const SERVICE_PID_FILE: &str = "/var/run/och/openconnect.pid";
const SERVICE_OPENCONNECT_LOG: &str = "/var/log/och/openconnect.log";
const SERVICE_LOG: &str = "/var/log/och/service.log";
const LAUNCHD_PATH: &str = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ServiceRequest {
    pub action: String,
    pub config: OchConfig,
    pub target: RuntimeTarget,
    pub vpn_password: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ServiceResponse {
    pub ok: bool,
    pub output: String,
    pub error: Option<String>,
}

impl ServiceResponse {
    fn ok(output: impl Into<String>) -> Self {
        Self {
            ok: true,
            output: output.into(),
            error: None,
        }
    }

    fn err(error: impl Into<String>) -> Self {
        Self {
            ok: false,
            output: String::new(),
            error: Some(error.into()),
        }
    }
}

pub fn service_should_be_used(os_name: &str) -> bool {
    is_macos(os_name) && std::env::var("OCH_DISABLE_SERVICE").unwrap_or_default() != "1"
}

pub fn socket_path() -> PathBuf {
    std::env::var("OCH_SERVICE_SOCKET")
        .map(PathBuf::from)
        .unwrap_or_else(|_| PathBuf::from(DEFAULT_SOCKET_PATH))
}

pub fn service_socket_exists() -> bool {
    socket_path().exists()
}

pub fn try_vpn_action(
    runtime: &Runtime,
    action: &str,
    vpn_password: Option<String>,
) -> Option<ServiceResponse> {
    if !service_should_be_used(&runtime.os_name) {
        return None;
    }

    let request = ServiceRequest {
        action: action.to_string(),
        config: runtime.config.clone(),
        target: runtime.target.clone(),
        vpn_password,
    };
    send_request_to_path(&socket_path(), &request).ok()
}

pub fn response_to_result(response: ServiceResponse) -> Result<(), String> {
    if !response.output.trim().is_empty() {
        println!("{}", response.output.trim_end());
    }
    if response.ok {
        Ok(())
    } else {
        Err(response
            .error
            .unwrap_or_else(|| "OCH service request failed".into()))
    }
}

pub fn send_request_to_path(
    path: &Path,
    request: &ServiceRequest,
) -> Result<ServiceResponse, String> {
    let mut stream = UnixStream::connect(path).map_err(|error| error.to_string())?;
    let payload = serde_json::to_vec(request).map_err(|error| error.to_string())?;
    stream
        .write_all(&payload)
        .map_err(|error| error.to_string())?;
    stream
        .shutdown(Shutdown::Write)
        .map_err(|error| error.to_string())?;

    let mut raw = String::new();
    stream
        .read_to_string(&mut raw)
        .map_err(|error| error.to_string())?;
    serde_json::from_str(&raw).map_err(|error| error.to_string())
}

pub fn exec_from_stdin() -> Result<(), String> {
    let mut raw = String::new();
    io::stdin()
        .read_to_string(&mut raw)
        .map_err(|error| error.to_string())?;
    println!("{}", handle_request_json(&raw));
    Ok(())
}

pub fn install() -> Result<(), String> {
    require_macos()?;
    require_root()?;

    let exe = std::env::current_exe().map_err(|error| error.to_string())?;
    fs::create_dir_all(SERVICE_RUN_DIR).map_err(|error| error.to_string())?;
    fs::create_dir_all(SERVICE_LOG_DIR).map_err(|error| error.to_string())?;
    fs::create_dir_all("/Library/LaunchDaemons").map_err(|error| error.to_string())?;

    set_mode(SERVICE_RUN_DIR, 0o775)?;
    set_mode(SERVICE_LOG_DIR, 0o775)?;
    chgrp_admin(SERVICE_RUN_DIR);
    chgrp_admin(SERVICE_LOG_DIR);

    let plist = launchd_plist(&exe);
    fs::write(PLIST_PATH, plist).map_err(|error| error.to_string())?;
    set_mode(PLIST_PATH, 0o644)?;

    let _ = Command::new("launchctl")
        .arg("bootout")
        .arg(format!("system/{LAUNCHD_LABEL}"))
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status();

    let status = Command::new("launchctl")
        .arg("bootstrap")
        .arg("system")
        .arg(PLIST_PATH)
        .status()
        .map_err(|error| error.to_string())?;
    if !status.success() {
        return Err(format!("launchctl bootstrap failed: {status}"));
    }

    let _ = Command::new("launchctl")
        .arg("enable")
        .arg(format!("system/{LAUNCHD_LABEL}"))
        .status();

    println!("OCH service installed: {PLIST_PATH}");
    println!("Socket: {}", socket_path().display());
    println!("Logs: {SERVICE_LOG}");
    Ok(())
}

pub fn uninstall() -> Result<(), String> {
    require_macos()?;
    require_root()?;

    let _ = Command::new("launchctl")
        .arg("bootout")
        .arg(format!("system/{LAUNCHD_LABEL}"))
        .status();
    let _ = fs::remove_file(PLIST_PATH);
    let _ = fs::remove_file(socket_path());

    println!("OCH service uninstalled");
    Ok(())
}

pub fn status() -> Result<(), String> {
    require_macos()?;
    let installed = Path::new(PLIST_PATH).is_file();
    let running = Command::new("launchctl")
        .arg("print")
        .arg(format!("system/{LAUNCHD_LABEL}"))
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|status| status.success());
    let socket = socket_path();
    let socket_exists = socket.exists();
    let socket_reachable = UnixStream::connect(&socket).is_ok();

    println!("Installed: {}", yes_no(installed));
    println!("Running: {}", yes_no(running));
    println!("Socket: {}", socket.display());
    println!("Socket exists: {}", yes_no(socket_exists));
    println!("Socket reachable: {}", yes_no(socket_reachable));
    Ok(())
}

pub fn run_daemon() -> Result<(), String> {
    fs::create_dir_all(SERVICE_RUN_DIR).map_err(|error| error.to_string())?;
    fs::create_dir_all(SERVICE_LOG_DIR).map_err(|error| error.to_string())?;
    set_mode(SERVICE_RUN_DIR, 0o775)?;
    set_mode(SERVICE_LOG_DIR, 0o775)?;
    chgrp_admin(SERVICE_RUN_DIR);
    chgrp_admin(SERVICE_LOG_DIR);

    let socket = socket_path();
    if socket.exists() {
        fs::remove_file(&socket).map_err(|error| error.to_string())?;
    }
    if let Some(parent) = socket.parent() {
        fs::create_dir_all(parent).map_err(|error| error.to_string())?;
    }

    let listener = UnixListener::bind(&socket).map_err(|error| error.to_string())?;
    set_mode(&socket, 0o660)?;
    if socket == Path::new(DEFAULT_SOCKET_PATH) {
        chgrp_admin(&socket);
    }

    service_log(&format!("service listening on {}", socket.display()));
    for stream in listener.incoming() {
        match stream {
            Ok(stream) => handle_stream(stream),
            Err(error) => service_log(&format!("accept failed: {error}")),
        }
    }
    Ok(())
}

fn handle_stream(mut stream: UnixStream) {
    let mut raw = String::new();
    let response = match stream.read_to_string(&mut raw) {
        Ok(_) => handle_request_json(&raw),
        Err(error) => response_json(ServiceResponse::err(error.to_string())),
    };
    let _ = stream.write_all(response.as_bytes());
}

pub fn handle_request_json(raw: &str) -> String {
    let response = match serde_json::from_str::<ServiceRequest>(raw) {
        Ok(request) => handle_request(request),
        Err(error) => ServiceResponse::err(format!("invalid service request: {error}")),
    };
    response_json(response)
}

fn response_json(response: ServiceResponse) -> String {
    serde_json::to_string(&response).unwrap_or_else(|_| {
        "{\"ok\":false,\"output\":\"\",\"error\":\"failed to encode service response\"}".into()
    })
}

fn handle_request(request: ServiceRequest) -> ServiceResponse {
    let vpn = ServiceVpn::new(request.config, request.target);
    let result = match request.action.as_str() {
        ACTION_CONNECT => vpn.connect(request.vpn_password.unwrap_or_default()),
        ACTION_DISCONNECT => vpn.disconnect(),
        ACTION_STATUS => vpn.status(),
        ACTION_LOGS => vpn.logs(),
        action => Err(format!("unknown service action: {action}")),
    };

    match result {
        Ok(output) => ServiceResponse::ok(output),
        Err(error) => ServiceResponse::err(error),
    }
}

struct ServiceVpn {
    config: OchConfig,
    target: RuntimeTarget,
    os_name: String,
}

impl ServiceVpn {
    fn new(config: OchConfig, target: RuntimeTarget) -> Self {
        Self {
            config,
            target,
            os_name: std::env::var("OS_NAME").unwrap_or_else(|_| std::env::consts::OS.to_string()),
        }
    }

    fn connect(&self, password: String) -> Result<String, String> {
        require_tool(&self.openconnect_bin())?;
        if self.is_macos() {
            require_tool("route")?;
            require_tool("nc")?;
        } else {
            require_tool("ip")?;
        }
        self.validate_connect_config()?;
        if password.is_empty() {
            return Err("VPN password is required".into());
        }

        if self.is_connected() {
            return self.status_with_prefix("VPN 已连接，无需重复连接\n");
        }

        self.prepare_log_file()?;
        let log_stdout = OpenOptions::new()
            .append(true)
            .open(SERVICE_OPENCONNECT_LOG)
            .map_err(|error| error.to_string())?;
        let log_stderr = log_stdout.try_clone().map_err(|error| error.to_string())?;

        let mut command = Command::new(self.openconnect_bin());
        command
            .env("OCH_ROUTES_EXTRA", self.config.routes_extra.join(" "))
            .args(openconnect_args_for_config(
                &self.config,
                Path::new(SERVICE_PID_FILE),
                self.resolve_vpn_script().as_deref(),
            ))
            .stdin(Stdio::piped())
            .stdout(Stdio::from(log_stdout))
            .stderr(Stdio::from(log_stderr));

        let mut child = command.spawn().map_err(|error| error.to_string())?;
        if let Some(stdin) = child.stdin.as_mut() {
            writeln!(stdin, "{password}").map_err(|error| error.to_string())?;
        }
        let status = child.wait().map_err(|error| error.to_string())?;
        drop(password);

        if !status.success() {
            return Err(format!(
                "VPN 连接失败，日志见: {}\n{}",
                SERVICE_OPENCONNECT_LOG,
                self.log_tail()
            ));
        }

        thread::sleep(Duration::from_secs(2));
        if !self.is_connected() {
            return Err(format!(
                "VPN 连接未建立，日志见: {}\n{}",
                SERVICE_OPENCONNECT_LOG,
                self.log_tail()
            ));
        }

        let mut output = format!("VPN 已连接，日志: {SERVICE_OPENCONNECT_LOG}\n");
        if self.wait_for_target_route(15) {
            if !self.target_host().is_empty() {
                match self.verify() {
                    Ok(verify) => output.push_str(&verify),
                    Err(error) => output.push_str(&format!("{error}\n")),
                }
            }
        } else {
            output.push_str(
                "提示: VPN 进程已建立，但目标路由暂未就绪；可稍后手动执行 verify 再检查\n",
            );
        }
        Ok(output)
    }

    fn disconnect(&self) -> Result<String, String> {
        let Some(pid) = self.connected_pid() else {
            if Path::new(SERVICE_PID_FILE).is_file() {
                let _ = fs::remove_file(SERVICE_PID_FILE);
                return Ok("发现陈旧 PID 文件，已清理\n".into());
            }
            return Ok("未找到 PID 文件，视为已断开\n".into());
        };

        if Command::new("kill")
            .arg("-0")
            .arg(&pid)
            .status()
            .is_ok_and(|status| status.success())
        {
            let status = Command::new("kill")
                .arg(&pid)
                .status()
                .map_err(|error| error.to_string())?;
            if !status.success() {
                return Err(format!("kill failed: {status}"));
            }
            thread::sleep(Duration::from_secs(1));
            let _ = fs::remove_file(SERVICE_PID_FILE);
            Ok("VPN 已断开\n".into())
        } else {
            let _ = fs::remove_file(SERVICE_PID_FILE);
            Ok("发现陈旧 PID 文件，已清理\n".into())
        }
    }

    fn status(&self) -> Result<String, String> {
        self.status_with_prefix("")
    }

    fn status_with_prefix(&self, prefix: &str) -> Result<String, String> {
        let mut output = String::from(prefix);
        if let Some(pid) = self.connected_pid() {
            output.push_str(&format!("VPN 已连接，PID: {pid}\n"));
        } else {
            output.push_str("VPN 未连接\n");
        }

        output.push_str("默认路由:\n");
        if let Ok(line) = self.default_route_line() {
            output.push_str(&format!("{line}\n"));
        }

        let host = self.target_host();
        if host.is_empty() {
            output.push_str("目标路由: 未配置 [ssh].target_host\n");
        } else {
            output.push_str("目标路由:\n");
            if let Ok(line) = self.route_line_for_host(&host) {
                output.push_str(&format!("{line}\n"));
            }
        }
        Ok(output)
    }

    fn logs(&self) -> Result<String, String> {
        if Path::new(SERVICE_OPENCONNECT_LOG).is_file() {
            tail_lines(Path::new(SERVICE_OPENCONNECT_LOG), 40).map_err(|error| error.to_string())
        } else {
            Ok(format!("日志文件不存在: {SERVICE_OPENCONNECT_LOG}"))
        }
    }

    fn verify(&self) -> Result<String, String> {
        let host = self.target_host();
        let port = self.target_port();
        if host.is_empty() {
            return Err("未设置 [ssh].target_host，无法验证目标连通性".into());
        }

        let default_iface = self.default_route_iface().unwrap_or_default();
        let target_iface = self.route_iface_for_host(&host).unwrap_or_default();
        let mut output = String::new();

        output.push_str("默认路由:\n");
        if let Ok(line) = self.default_route_line() {
            output.push_str(&format!("{line}\n"));
        }
        output.push_str("目标路由:\n");
        if let Ok(line) = self.route_line_for_host(&host) {
            output.push_str(&format!("{line}\n"));
        }

        if !default_iface.is_empty() && !target_iface.is_empty() && default_iface != target_iface {
            output.push_str(&format!(
                "路由检查: 目标主机走 {target_iface}，默认流量仍走 {default_iface}\n"
            ));
        } else if !target_iface.is_empty() {
            output.push_str(&format!(
                "路由检查: 目标主机走 {target_iface}，与默认路由相同；这可能是全隧道或服务端未下发分流路由\n"
            ));
        } else {
            output.push_str("路由检查: 未能解析目标路由，请确认 VPN 已连接\n");
        }

        if self.check_tcp_port(&host, &port) {
            output.push_str(&format!("端口检查: {host}:{port} 可达\n"));
        } else {
            output.push_str(&format!("端口检查: {host}:{port} 不可达\n"));
        }
        Ok(output)
    }

    fn validate_connect_config(&self) -> Result<(), String> {
        if self.config.vpn_host.is_empty() {
            return Err("未设置 [vpn].host，请先配置 VPN gateway".into());
        }
        if self.config.vpn_user.is_empty() {
            return Err("未设置 [vpn].user，请先配置 VPN user".into());
        }
        Ok(())
    }

    fn is_connected(&self) -> bool {
        self.connected_pid().is_some()
    }

    fn connected_pid(&self) -> Option<String> {
        if let Ok(pid) = fs::read_to_string(SERVICE_PID_FILE) {
            let pid = pid.trim().to_string();
            if process_looks_like_openconnect(&pid) {
                return Some(pid);
            }
        }
        discover_openconnect_pid(&self.config.vpn_host, Path::new(SERVICE_PID_FILE))
    }

    fn prepare_log_file(&self) -> Result<(), String> {
        let file = File::create(SERVICE_OPENCONNECT_LOG).map_err(|error| error.to_string())?;
        file.set_permissions(fs::Permissions::from_mode(0o600))
            .map_err(|error| error.to_string())
    }

    fn log_tail(&self) -> String {
        tail_lines(Path::new(SERVICE_OPENCONNECT_LOG), 40).unwrap_or_default()
    }

    fn is_macos(&self) -> bool {
        is_macos(&self.os_name)
    }

    fn openconnect_bin(&self) -> String {
        find_tool("openconnect")
            .or_else(|| {
                [
                    "/opt/homebrew/bin/openconnect",
                    "/usr/local/bin/openconnect",
                ]
                .iter()
                .map(PathBuf::from)
                .find(|path| path.is_file())
            })
            .map(|path| path.display().to_string())
            .unwrap_or_else(|| "openconnect".to_string())
    }

    fn resolve_vpn_script(&self) -> Option<PathBuf> {
        if self.is_macos()
            && self.config.routes_mode == "extra"
            && !self.config.routes_extra.is_empty()
        {
            route_wrapper_path()
        } else {
            None
        }
    }

    fn target_host(&self) -> String {
        self.target
            .host
            .clone()
            .unwrap_or_else(|| self.config.target_host.clone())
    }

    fn target_port(&self) -> String {
        self.target
            .port
            .clone()
            .unwrap_or_else(|| self.config.target_port.clone())
    }

    fn default_route_line(&self) -> Result<String, String> {
        if self.is_macos() {
            let output = command_output(Command::new("route").arg("-n").arg("get").arg("default"))
                .map_err(|error| error.to_string())?;
            Ok(parse_macos_route_line(&String::from_utf8_lossy(
                &output.stdout,
            )))
        } else {
            let output = command_output(Command::new("ip").arg("route").arg("show").arg("default"))
                .map_err(|error| error.to_string())?;
            Ok(first_line(&output.stdout))
        }
    }

    fn route_line_for_host(&self, host: &str) -> Result<String, String> {
        if self.is_macos() {
            let output = command_output(Command::new("route").arg("-n").arg("get").arg(host))
                .map_err(|error| error.to_string())?;
            Ok(parse_macos_route_line(&String::from_utf8_lossy(
                &output.stdout,
            )))
        } else {
            let output = command_output(Command::new("ip").arg("route").arg("get").arg(host))
                .map_err(|error| error.to_string())?;
            Ok(first_line(&output.stdout))
        }
    }

    fn default_route_iface(&self) -> Result<String, String> {
        if self.is_macos() {
            let output = command_output(Command::new("route").arg("-n").arg("get").arg("default"))
                .map_err(|error| error.to_string())?;
            Ok(parse_macos_iface(&String::from_utf8_lossy(&output.stdout)))
        } else {
            let output = command_output(Command::new("ip").arg("route").arg("show").arg("default"))
                .map_err(|error| error.to_string())?;
            Ok(parse_linux_iface(&String::from_utf8_lossy(&output.stdout)))
        }
    }

    fn route_iface_for_host(&self, host: &str) -> Result<String, String> {
        if self.is_macos() {
            let output = command_output(Command::new("route").arg("-n").arg("get").arg(host))
                .map_err(|error| error.to_string())?;
            Ok(parse_macos_iface(&String::from_utf8_lossy(&output.stdout)))
        } else {
            let output = command_output(Command::new("ip").arg("route").arg("get").arg(host))
                .map_err(|error| error.to_string())?;
            Ok(parse_linux_iface(&String::from_utf8_lossy(&output.stdout)))
        }
    }

    fn wait_for_target_route(&self, timeout_seconds: u64) -> bool {
        let host = self.target_host();
        if host.is_empty() {
            return true;
        }
        for _ in 0..timeout_seconds {
            if self
                .route_iface_for_host(&host)
                .is_ok_and(|iface| !iface.is_empty())
            {
                return true;
            }
            thread::sleep(Duration::from_secs(1));
        }
        false
    }

    fn check_tcp_port(&self, host: &str, port: &str) -> bool {
        if self.is_macos()
            && Command::new("nc")
                .arg("-G")
                .arg("5")
                .arg("-z")
                .arg(host)
                .arg(port)
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status()
                .is_ok_and(|status| status.success())
        {
            return true;
        }

        Command::new("nc")
            .arg("-w")
            .arg("5")
            .arg("-z")
            .arg(host)
            .arg(port)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .is_ok_and(|status| status.success())
    }
}

pub fn openconnect_args_for_config(
    config: &OchConfig,
    pid_file: &Path,
    script: Option<&Path>,
) -> Vec<String> {
    let mut args = vec![
        config.vpn_host.clone(),
        "-u".to_string(),
        config.vpn_user.clone(),
        "--os=win".to_string(),
        "--useragent=AnyConnect".to_string(),
        "--passwd-on-stdin".to_string(),
        "--background".to_string(),
        format!("--pid-file={}", pid_file.display()),
    ];
    if let Some(script) = script {
        args.push("--script".to_string());
        args.push(script.display().to_string());
    }
    if !config.vpn_auth_group.is_empty() {
        args.push(format!("--authgroup={}", config.vpn_auth_group));
    }
    args
}

fn first_line(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes)
        .lines()
        .next()
        .unwrap_or_default()
        .to_string()
}

fn service_log(message: &str) {
    let Ok(mut file) = OpenOptions::new()
        .create(true)
        .append(true)
        .open(SERVICE_LOG)
    else {
        return;
    };
    let _ = writeln!(file, "{message}");
}

fn require_macos() -> Result<(), String> {
    if is_macos(std::env::consts::OS)
        || std::env::var("OS_NAME").is_ok_and(|value| is_macos(&value))
    {
        Ok(())
    } else {
        Err("OCH service mode is only supported on macOS".into())
    }
}

fn require_root() -> Result<(), String> {
    let output = Command::new("id")
        .arg("-u")
        .output()
        .map_err(|error| error.to_string())?;
    if String::from_utf8_lossy(&output.stdout).trim() == "0" {
        Ok(())
    } else {
        Err("请使用 sudo 运行此命令，例如：sudo och service install".into())
    }
}

fn set_mode(path: impl AsRef<Path>, mode: u32) -> Result<(), String> {
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).map_err(|error| error.to_string())
}

fn chgrp_admin(path: impl AsRef<Path>) {
    let _ = Command::new("chgrp")
        .arg("admin")
        .arg(path.as_ref())
        .status();
}

fn launchd_plist(exe: &Path) -> String {
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{LAUNCHD_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{}</string>
    <string>service</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>{LAUNCHD_PATH}</string>
  </dict>
  <key>StandardOutPath</key>
  <string>{SERVICE_LOG}</string>
  <key>StandardErrorPath</key>
  <string>{SERVICE_LOG}</string>
</dict>
</plist>
"#,
        xml_escape(&exe.display().to_string())
    )
}

fn xml_escape(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn yes_no(value: bool) -> &'static str {
    if value {
        "yes"
    } else {
        "no"
    }
}

#[cfg(test)]
mod tests {
    use super::{
        handle_request_json, openconnect_args_for_config, service_should_be_used, ServiceRequest,
        ServiceResponse, ACTION_CONNECT, ACTION_STATUS,
    };
    use crate::config::{OchConfig, RuntimeTarget};
    use std::path::Path;

    fn config() -> OchConfig {
        OchConfig {
            vpn_host: "vpn.example.com".into(),
            vpn_user: "alice".into(),
            vpn_auth_group: "staff".into(),
            target_host: "10.0.0.10".into(),
            routes_extra: vec!["10.0.0.0/8".into()],
            ..OchConfig::default()
        }
    }

    #[test]
    fn service_request_round_trips_json() {
        let request = ServiceRequest {
            action: ACTION_STATUS.into(),
            config: config(),
            target: RuntimeTarget::default(),
            vpn_password: None,
        };
        let raw = serde_json::to_string(&request).expect("encode request");
        let decoded: ServiceRequest = serde_json::from_str(&raw).expect("decode request");
        assert_eq!(decoded.action, ACTION_STATUS);
        assert_eq!(decoded.config.vpn_host, "vpn.example.com");
    }

    #[test]
    fn service_rejects_unknown_action() {
        let request = ServiceRequest {
            action: "shell".into(),
            config: config(),
            target: RuntimeTarget::default(),
            vpn_password: None,
        };
        let raw = serde_json::to_string(&request).expect("encode request");
        let response: ServiceResponse =
            serde_json::from_str(&handle_request_json(&raw)).expect("decode response");
        assert!(!response.ok);
        assert!(response.error.unwrap().contains("unknown service action"));
    }

    #[test]
    fn service_rejects_empty_vpn_config_before_running_openconnect() {
        let request = ServiceRequest {
            action: ACTION_CONNECT.into(),
            config: OchConfig::default(),
            target: RuntimeTarget::default(),
            vpn_password: Some("secret".into()),
        };
        let raw = serde_json::to_string(&request).expect("encode request");
        let response: ServiceResponse =
            serde_json::from_str(&handle_request_json(&raw)).expect("decode response");
        assert!(!response.ok);
        assert!(response.error.unwrap().contains("[vpn].host"));
    }

    #[test]
    fn openconnect_args_include_background_pid_script_and_authgroup() {
        let args = openconnect_args_for_config(
            &config(),
            Path::new("/var/run/och/openconnect.pid"),
            Some(Path::new(
                "/opt/homebrew/libexec/och/macos-vpnc-route-wrapper.sh",
            )),
        );
        assert!(args.contains(&"--passwd-on-stdin".to_string()));
        assert!(args.contains(&"--background".to_string()));
        assert!(args.contains(&"--pid-file=/var/run/och/openconnect.pid".to_string()));
        assert!(args.contains(&"--script".to_string()));
        assert!(args.contains(&"/opt/homebrew/libexec/och/macos-vpnc-route-wrapper.sh".to_string()));
        assert!(args.contains(&"--authgroup=staff".to_string()));
    }

    #[test]
    fn service_mode_is_macos_only_and_can_be_disabled() {
        std::env::remove_var("OCH_DISABLE_SERVICE");
        assert!(service_should_be_used("Darwin"));
        assert!(!service_should_be_used("Linux"));
        std::env::set_var("OCH_DISABLE_SERVICE", "1");
        assert!(!service_should_be_used("Darwin"));
        std::env::remove_var("OCH_DISABLE_SERVICE");
    }
}
