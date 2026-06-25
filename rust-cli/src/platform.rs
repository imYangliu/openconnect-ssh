use std::fs;
use std::io;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

pub fn is_macos(os_name: &str) -> bool {
    os_name == "Darwin" || os_name == "macos"
}

pub fn require_tool(tool: &str) -> Result<(), String> {
    if tool.contains('/') {
        let path = Path::new(tool);
        if path.is_file() {
            return Ok(());
        }
        return Err(format!("缺少可执行文件: {tool}"));
    }
    find_tool(tool)
        .map(|_| ())
        .ok_or_else(|| format!("缺少依赖命令: {tool}"))
}

pub fn find_tool(tool: &str) -> Option<PathBuf> {
    let path = std::env::var_os("PATH")?;
    std::env::split_paths(&path)
        .map(|dir| dir.join(tool))
        .find(|candidate| candidate.is_file())
}

pub fn command_output(command: &mut Command) -> io::Result<Output> {
    command.output()
}

pub fn exec_command(command: &mut Command) -> Result<(), String> {
    let error = command.exec();
    Err(error.to_string())
}

pub fn read_trimmed(path: &Path) -> io::Result<String> {
    Ok(fs::read_to_string(path)?.trim().to_string())
}

pub fn process_looks_like_openconnect(pid: &str) -> bool {
    if pid.parse::<u32>().is_err() {
        return false;
    }
    Command::new("ps")
        .arg("-p")
        .arg(pid)
        .arg("-o")
        .arg("comm=")
        .arg("-o")
        .arg("command=")
        .output()
        .ok()
        .and_then(|output| {
            output.status.success().then(|| {
                let text = String::from_utf8_lossy(&output.stdout);
                text.contains("openconnect")
            })
        })
        .unwrap_or(false)
}

pub fn discover_openconnect_pid(vpn_host: &str, pid_file: &Path) -> Option<String> {
    if vpn_host.is_empty() {
        return None;
    }
    let output = Command::new("ps")
        .arg("axww")
        .arg("-o")
        .arg("pid=")
        .arg("-o")
        .arg("comm=")
        .arg("-o")
        .arg("command=")
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    openconnect_pid_from_ps_output(&String::from_utf8_lossy(&output.stdout), vpn_host, pid_file)
}

pub fn openconnect_pid_from_ps_output(
    output: &str,
    vpn_host: &str,
    pid_file: &Path,
) -> Option<String> {
    let pid_file = pid_file.display().to_string();
    let pid_file_eq = format!("--pid-file={pid_file}");
    let pid_file_space = format!("--pid-file {pid_file}");

    output.lines().find_map(|line| {
        let trimmed = line.trim_start();
        let (pid, command) = trimmed.split_once(char::is_whitespace)?;
        if pid.parse::<u32>().is_err() {
            return None;
        }
        let matches_process = command.contains("openconnect")
            && command.contains(vpn_host)
            && (command.contains(&pid_file_eq) || command.contains(&pid_file_space));
        matches_process.then(|| pid.to_string())
    })
}

pub fn tail_lines(path: &Path, count: usize) -> io::Result<String> {
    let content = fs::read_to_string(path)?;
    let lines: Vec<&str> = content.lines().collect();
    let start = lines.len().saturating_sub(count);
    Ok(lines[start..].join("\n"))
}

#[cfg(test)]
mod tests {
    use super::openconnect_pid_from_ps_output;
    use std::path::Path;

    #[test]
    fn discovers_openconnect_pid_when_pid_file_is_missing() {
        let output = "\
  91638 /opt/homebrew/bi /opt/homebrew/bin/openconnect https://vpn.example.edu/ -u alice --background --pid-file=/tmp/och-openconnect-alice.pid
  91639 /bin/zsh zsh
";

        let pid = openconnect_pid_from_ps_output(
            output,
            "https://vpn.example.edu/",
            Path::new("/tmp/och-openconnect-alice.pid"),
        );

        assert_eq!(pid.as_deref(), Some("91638"));
    }

    #[test]
    fn ignores_other_openconnect_processes() {
        let output = "\
  91638 /opt/homebrew/bi /opt/homebrew/bin/openconnect https://other.example.edu/ --pid-file=/tmp/och-openconnect-alice.pid
  91639 /opt/homebrew/bi /opt/homebrew/bin/openconnect https://vpn.example.edu/ --pid-file=/tmp/other.pid
";

        let pid = openconnect_pid_from_ps_output(
            output,
            "https://vpn.example.edu/",
            Path::new("/tmp/och-openconnect-alice.pid"),
        );

        assert_eq!(pid, None);
    }
}
