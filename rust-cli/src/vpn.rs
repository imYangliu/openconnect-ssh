use crate::config::Runtime;
use crate::platform::{
    command_output, exec_command, find_tool, is_macos, read_trimmed, require_tool, tail_lines,
};
use crate::route_wrapper_path;
use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::process::{Command, Stdio};
use std::thread;
use std::time::Duration;

pub struct Vpn {
    runtime: Runtime,
}

impl Vpn {
    pub fn new(runtime: Runtime) -> Self {
        Self { runtime }
    }

    pub fn ensure(&self) -> Result<(), String> {
        if self.verify_quiet() {
            eprintln!("[och] VPN 连通性正常");
            return Ok(());
        }
        eprintln!("[och] VPN 不可达，正在尝试连接");
        self.connect()?;
        if self.verify_quiet() {
            eprintln!("[och] VPN 已恢复");
            Ok(())
        } else {
            Err(format!("重连后仍无法访问目标，检查日志：och vpn logs"))
        }
    }

    pub fn connect(&self) -> Result<(), String> {
        require_tool("sudo")?;
        require_tool(&self.openconnect_bin())?;
        if self.is_macos() {
            require_tool("route")?;
            require_tool("nc")?;
        } else {
            require_tool("ip")?;
        }
        if self.runtime.config.vpn_host.is_empty() {
            return Err(format!(
                "未设置 [vpn].host，请在 {} 中配置",
                self.runtime.config_file.display()
            ));
        }
        if self.runtime.config.vpn_user.is_empty() {
            return Err(format!(
                "未设置 [vpn].user，请在 {} 中配置",
                self.runtime.config_file.display()
            ));
        }

        if self.is_connected() {
            println!("VPN 已连接，无需重复连接");
            self.status()?;
            return Ok(());
        }

        let password = self.read_vpn_password()?;
        self.prepare_log_file()?;

        let mut openconnect_args = vec![
            self.runtime.config.vpn_host.clone(),
            "-u".to_string(),
            self.runtime.config.vpn_user.clone(),
            "--os=win".to_string(),
            "--useragent=AnyConnect".to_string(),
            "--passwd-on-stdin".to_string(),
            "--background".to_string(),
            format!("--pid-file={}", self.runtime.pid_file.display()),
        ];

        if let Some(script) = self.resolve_vpn_script() {
            openconnect_args.push("--script".to_string());
            openconnect_args.push(script.display().to_string());
        }
        if !self.runtime.config.vpn_auth_group.is_empty() {
            openconnect_args.push(format!(
                "--authgroup={}",
                self.runtime.config.vpn_auth_group
            ));
        }

        let log_stdout = OpenOptions::new()
            .append(true)
            .open(&self.runtime.log_file)
            .map_err(|error| error.to_string())?;
        let log_stderr = log_stdout.try_clone().map_err(|error| error.to_string())?;

        let mut command = Command::new("sudo");
        if self.runtime.sudo_askpass.is_some() {
            command.arg("-A");
        }
        command
            .arg("env")
            .arg(format!(
                "OCH_ROUTES_EXTRA={}",
                self.runtime.config.routes_extra.join(" ")
            ))
            .arg(self.openconnect_bin())
            .args(&openconnect_args)
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
            eprintln!("VPN 连接失败，日志见: {}", self.runtime.log_file.display());
            self.print_log_tail();
            return Err("VPN 连接失败".into());
        }

        thread::sleep(Duration::from_secs(2));
        if self.is_connected() {
            println!("VPN 已连接，日志: {}", self.runtime.log_file.display());
            if self.wait_for_target_route(15) {
                if !self.target_host().is_empty() {
                    let _ = self.verify();
                }
            } else {
                eprintln!("提示: VPN 进程已建立，但目标路由暂未就绪；可稍后手动执行 verify 再检查");
            }
            Ok(())
        } else {
            eprintln!(
                "VPN 连接未建立，日志见: {}",
                self.runtime.log_file.display()
            );
            self.print_log_tail();
            Err("VPN 连接未建立".into())
        }
    }

    pub fn disconnect(&self) -> Result<(), String> {
        require_tool("sudo")?;
        if !self.runtime.pid_file.is_file() {
            println!("未找到 PID 文件，视为已断开");
            return Ok(());
        }
        let pid = read_trimmed(&self.runtime.pid_file).map_err(|error| error.to_string())?;
        if self.sudo_kill(&pid, "0") {
            self.sudo_command(["kill", &pid])?;
            thread::sleep(Duration::from_secs(1));
            println!("VPN 已断开");
        } else {
            println!("发现陈旧 PID 文件，已清理");
        }
        self.sudo_command(["rm", "-f", &self.runtime.pid_file.display().to_string()])?;
        Ok(())
    }

    pub fn status(&self) -> Result<(), String> {
        if self.is_connected() {
            let pid = read_trimmed(&self.runtime.pid_file).unwrap_or_default();
            println!("VPN 已连接，PID: {pid}");
        } else {
            println!("VPN 未连接");
        }

        println!("默认路由:");
        let _ = self.default_route_line().map(|line| println!("{line}"));

        let host = self.target_host();
        if host.is_empty() {
            println!("目标路由: 未配置 [ssh].target_host");
        } else {
            println!("目标路由:");
            let _ = self
                .route_line_for_host(&host)
                .map(|line| println!("{line}"));
        }
        Ok(())
    }

    pub fn verify(&self) -> Result<(), String> {
        if self.is_macos() {
            require_tool("route")?;
            require_tool("nc")?;
        } else {
            require_tool("ip")?;
        }

        let host = self.target_host();
        let port = self.target_port();
        if host.is_empty() {
            return Err("未设置 [ssh].target_host，无法验证目标连通性".into());
        }

        let default_iface = self.default_route_iface().unwrap_or_default();
        let target_iface = self.route_iface_for_host(&host).unwrap_or_default();

        println!("默认路由:");
        if let Ok(line) = self.default_route_line() {
            println!("{line}");
        }
        println!("目标路由:");
        if let Ok(line) = self.route_line_for_host(&host) {
            println!("{line}");
        }

        if !default_iface.is_empty() && !target_iface.is_empty() && default_iface != target_iface {
            println!("路由检查: 目标主机走 {target_iface}，默认流量仍走 {default_iface}");
        } else if !target_iface.is_empty() {
            println!("路由检查: 目标主机走 {target_iface}，与默认路由相同；这可能是全隧道或服务端未下发分流路由");
        } else {
            println!("路由检查: 未能解析目标路由，请确认 VPN 已连接");
        }

        if self.check_tcp_port(&host, &port) {
            println!("端口检查: {host}:{port} 可达");
            Ok(())
        } else {
            eprintln!("端口检查: {host}:{port} 不可达");
            Err("端口不可达".into())
        }
    }

    pub fn ssh(&self) -> Result<(), String> {
        require_tool("ssh")?;
        let host = self.target_host();
        if host.is_empty() {
            return Err("未设置 [ssh].target_host，无法发起 SSH 连接".into());
        }
        if !self.is_connected() {
            return Err("VPN 未连接，请先执行 connect".into());
        }
        let port = self.target_port();
        let user = self.target_user();
        exec_command(
            Command::new("ssh")
                .arg("-p")
                .arg(port)
                .arg(format!("{user}@{host}")),
        )
    }

    pub fn logs(&self) -> Result<(), String> {
        if self.runtime.log_file.is_file() {
            let tail = tail_lines(&self.runtime.log_file, 40).map_err(|error| error.to_string())?;
            println!("{tail}");
        } else {
            println!("日志文件不存在: {}", self.runtime.log_file.display());
        }
        Ok(())
    }

    fn verify_quiet(&self) -> bool {
        let host = self.target_host();
        if host.is_empty() {
            return false;
        }
        self.route_iface_for_host(&host).is_ok() && self.check_tcp_port(&host, &self.target_port())
    }

    fn is_connected(&self) -> bool {
        if !self.runtime.pid_file.is_file() {
            return false;
        }
        let Ok(pid) = read_trimmed(&self.runtime.pid_file) else {
            return false;
        };
        if pid.parse::<u32>().is_err() {
            return false;
        }
        let output = Command::new("ps")
            .arg("-p")
            .arg(pid)
            .arg("-o")
            .arg("comm=")
            .output();
        let Ok(output) = output else {
            return false;
        };
        String::from_utf8_lossy(&output.stdout).trim() == "openconnect"
    }

    fn target_host(&self) -> String {
        self.runtime
            .target
            .host
            .clone()
            .unwrap_or_else(|| self.runtime.config.target_host.clone())
    }

    fn target_port(&self) -> String {
        self.runtime
            .target
            .port
            .clone()
            .unwrap_or_else(|| self.runtime.config.target_port.clone())
    }

    fn target_user(&self) -> String {
        self.runtime
            .target
            .user
            .clone()
            .unwrap_or_else(|| self.runtime.config.target_user.clone())
    }

    fn is_macos(&self) -> bool {
        is_macos(&self.runtime.os_name)
    }

    fn openconnect_bin(&self) -> String {
        find_tool("openconnect")
            .map(|path| path.display().to_string())
            .unwrap_or_else(|| "openconnect".to_string())
    }

    fn resolve_vpn_script(&self) -> Option<std::path::PathBuf> {
        if self.is_macos() && !self.runtime.config.routes_extra.is_empty() {
            route_wrapper_path()
        } else {
            None
        }
    }

    fn read_vpn_password(&self) -> Result<String, String> {
        if let Some(password) = &self.runtime.vpn_password {
            return Ok(password.clone());
        }
        if self.is_macos()
            && !self.runtime.config.vpn_user.is_empty()
            && Path::new("/usr/bin/security").is_file()
        {
            if let Ok(output) = Command::new("/usr/bin/security")
                .arg("find-generic-password")
                .arg("-s")
                .arg(&self.runtime.keychain_service)
                .arg("-a")
                .arg(&self.runtime.config.vpn_user)
                .arg("-w")
                .output()
            {
                let password = String::from_utf8_lossy(&output.stdout).trim().to_string();
                if output.status.success() && !password.is_empty() {
                    return Ok(password);
                }
            }
        }
        eprint!("VPN password: ");
        io::stderr().flush().map_err(|error| error.to_string())?;
        let mut password = String::new();
        io::stdin()
            .read_line(&mut password)
            .map_err(|error| error.to_string())?;
        Ok(password.trim_end_matches(['\r', '\n']).to_string())
    }

    fn prepare_log_file(&self) -> Result<(), String> {
        let file = File::create(&self.runtime.log_file).map_err(|error| error.to_string())?;
        file.set_permissions(fs::Permissions::from_mode(0o600))
            .map_err(|error| error.to_string())?;
        Ok(())
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

        if find_tool("timeout").is_some()
            && Command::new("timeout")
                .arg("5")
                .arg("bash")
                .arg("-lc")
                .arg("exec 3<>/dev/tcp/$1/$2")
                .arg("_")
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
            .arg("-G")
            .arg("5")
            .arg("-z")
            .arg(host)
            .arg(port)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .is_ok_and(|status| status.success())
            || Command::new("nc")
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

    fn sudo_kill(&self, pid: &str, signal: &str) -> bool {
        let signal_arg = format!("-{signal}");
        self.sudo_command_vec(vec!["kill".to_string(), signal_arg, pid.to_string()])
            .is_ok()
    }

    fn sudo_command<const N: usize>(&self, args: [&str; N]) -> Result<(), String> {
        self.sudo_command_vec(args.into_iter().map(str::to_string).collect())
    }

    fn sudo_command_vec(&self, args: Vec<String>) -> Result<(), String> {
        let mut command = Command::new("sudo");
        if self.runtime.sudo_askpass.is_some() {
            command.arg("-A");
        }
        command.args(args);
        let status = command.status().map_err(|error| error.to_string())?;
        if status.success() {
            Ok(())
        } else {
            Err(format!("sudo command failed: {status}"))
        }
    }

    fn print_log_tail(&self) {
        if let Ok(tail) = tail_lines(&self.runtime.log_file, 40) {
            eprintln!("{tail}");
        }
    }
}

fn first_line(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes)
        .lines()
        .next()
        .unwrap_or_default()
        .to_string()
}

pub fn parse_macos_route_line(output: &str) -> String {
    let mut parts = Vec::new();
    for line in output.lines() {
        let Some((key, value)) = line.split_once(':') else {
            continue;
        };
        let key = key.trim();
        if matches!(key, "route to" | "destination" | "gateway" | "interface") {
            parts.push(format!("{key}={}", value.trim()));
        }
    }
    if parts.is_empty() {
        String::new()
    } else {
        format!("{} ", parts.join(" "))
    }
}

pub fn parse_macos_iface(output: &str) -> String {
    output
        .lines()
        .find_map(|line| {
            let (key, value) = line.split_once(':')?;
            (key.trim() == "interface").then(|| value.trim().to_string())
        })
        .unwrap_or_default()
}

pub fn parse_linux_iface(output: &str) -> String {
    let tokens: Vec<&str> = output.split_whitespace().collect();
    tokens
        .windows(2)
        .find_map(|pair| (pair[0] == "dev").then(|| pair[1].to_string()))
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::{parse_linux_iface, parse_macos_iface, parse_macos_route_line};

    #[test]
    fn parses_macos_route_output() {
        let output = "\
   route to: default
destination: default
    gateway: 10.0.0.1
  interface: en0
";
        let line = parse_macos_route_line(output);
        assert!(line.contains("gateway=10.0.0.1"));
        assert!(line.contains("interface=en0"));
        assert_eq!(parse_macos_iface(output), "en0");
    }

    #[test]
    fn parses_linux_route_iface() {
        assert_eq!(
            parse_linux_iface("1.2.3.4 via 10.0.0.1 dev eth0 src 10.0.0.5"),
            "eth0"
        );
    }
}
