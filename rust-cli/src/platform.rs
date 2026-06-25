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

pub fn tail_lines(path: &Path, count: usize) -> io::Result<String> {
    let content = fs::read_to_string(path)?;
    let lines: Vec<&str> = content.lines().collect();
    let start = lines.len().saturating_sub(count);
    Ok(lines[start..].join("\n"))
}
