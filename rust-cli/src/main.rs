use clap::{CommandFactory, Parser, Subcommand};
use std::ffi::OsString;
use std::path::PathBuf;
use std::process::Command;

mod config;
mod platform;
mod vpn;

use config::load_runtime;
use platform::{exec_command, find_tool, require_tool};
use vpn::Vpn;

const DEFAULT_INSTALL_URL: &str =
    "https://raw.githubusercontent.com/imyangliu/openconnect-ssh/main/install.sh";

#[derive(Parser, Debug)]
#[command(name = "och", disable_help_subcommand = true)]
#[command(about = "OpenConnect + SSH helper")]
struct Cli {
    #[command(subcommand)]
    command: Option<Commands>,
}

#[derive(Subcommand, Debug)]
enum Commands {
    /// Run the interactive setup helper.
    Setup,
    /// SSH ProxyCommand entrypoint: ensure VPN, then connect stdio to host:port.
    ProxyCommand { host: String, port: String },
    /// Manage the VPN connection.
    Vpn {
        #[command(subcommand)]
        command: Option<VpnCommand>,
    },
    /// Upgrade OCH by running the official installer again.
    Update,
    /// Show help.
    Help,
}

#[derive(Subcommand, Debug, Clone, Copy)]
enum VpnCommand {
    Connect,
    Disconnect,
    Status,
    Verify,
    Ssh,
    Logs,
    Help,
}

fn main() {
    if let Err(error) = run() {
        eprintln!("Error: {error}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), String> {
    let args = normalize_legacy_args(std::env::args_os());
    let cli = match Cli::try_parse_from(args) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            error
                .print()
                .map_err(|print_error| print_error.to_string())?;
            return Ok(());
        }
        Err(error) => return Err(error.to_string()),
    };

    match cli.command.unwrap_or(Commands::Help) {
        Commands::Setup => exec_setup(),
        Commands::ProxyCommand { host, port } => proxy_command(&host, &port),
        Commands::Vpn { command } => run_vpn_command(command.unwrap_or(VpnCommand::Help)),
        Commands::Update => run_update(),
        Commands::Help => {
            Cli::command()
                .print_help()
                .map_err(|error| error.to_string())?;
            println!();
            Ok(())
        }
    }
}

fn normalize_legacy_args<I>(args: I) -> Vec<OsString>
where
    I: IntoIterator<Item = OsString>,
{
    let mut args: Vec<OsString> = args.into_iter().collect();
    if args.get(1).is_some_and(|arg| arg == "--proxy-command") {
        args[1] = OsString::from("proxy-command");
    }
    args
}

fn run_update() -> Result<(), String> {
    require_tool("bash")?;

    let install_url =
        std::env::var("OCH_INSTALL_URL").unwrap_or_else(|_| DEFAULT_INSTALL_URL.to_string());

    let command_text = if find_tool("curl").is_some() {
        r#"curl -fsSL "$1" | bash -s -- --update"#
    } else if find_tool("wget").is_some() {
        r#"wget -qO- "$1" | bash -s -- --update"#
    } else {
        return Err("och update 需要 curl 或 wget".into());
    };

    exec_command(
        Command::new("bash")
            .arg("-c")
            .arg(command_text)
            .arg("och-update")
            .arg(install_url),
    )
}

fn proxy_command(host: &str, port: &str) -> Result<(), String> {
    if host.is_empty() {
        return Err("proxy-command 缺少 host".into());
    }
    if port.is_empty() {
        return Err("proxy-command 缺少 port".into());
    }

    let mut runtime = load_runtime(true).map_err(|error| error.to_string())?;
    runtime.target.host = Some(host.to_string());
    runtime.target.port = Some(port.to_string());
    runtime.target.user = None;

    let vpn = Vpn::new(runtime);
    vpn.ensure()?;

    require_tool("nc")?;
    exec_command(Command::new("nc").arg(host).arg(port))
}

fn run_vpn_command(command: VpnCommand) -> Result<(), String> {
    if matches!(command, VpnCommand::Help) {
        print_vpn_help();
        return Ok(());
    }

    let runtime = load_runtime(true).map_err(|error| error.to_string())?;
    let vpn = Vpn::new(runtime);

    match command {
        VpnCommand::Connect => vpn.connect(),
        VpnCommand::Disconnect => vpn.disconnect(),
        VpnCommand::Status => vpn.status(),
        VpnCommand::Verify => vpn.verify(),
        VpnCommand::Ssh => vpn.ssh(),
        VpnCommand::Logs => vpn.logs(),
        VpnCommand::Help => unreachable!(),
    }
}

fn print_vpn_help() {
    println!(
        "\
OCH VPN commands

用法:
  och vpn connect
  och vpn disconnect
  och vpn status
  och vpn verify
  och vpn ssh
  och vpn logs
"
    );
}

fn exec_setup() -> Result<(), String> {
    let helper = helper_path("och-setup.sh")?;
    require_tool_path(&helper)?;
    exec_command(&mut Command::new(helper))
}

fn require_tool_path(path: &PathBuf) -> Result<(), String> {
    if path.is_file() {
        Ok(())
    } else {
        Err(format!("缺少可执行文件: {}", path.display()))
    }
}

fn helper_path(name: &str) -> Result<PathBuf, String> {
    let mut candidates = Vec::new();
    if let Ok(libexec) = std::env::var("OCH_LIBEXEC_DIR") {
        candidates.push(PathBuf::from(libexec).join(name));
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(bin_dir) = exe.parent() {
            candidates.push(bin_dir.join("../libexec/och").join(name));
        }
    }
    if let Ok(cwd) = std::env::current_dir() {
        candidates.push(cwd.join("src").join(name));
    }

    candidates
        .into_iter()
        .find(|candidate| candidate.is_file())
        .ok_or_else(|| format!("cannot find OCH helper {name}"))
}

pub(crate) fn route_wrapper_path() -> Option<PathBuf> {
    helper_path("macos-vpnc-route-wrapper.sh").ok()
}

#[cfg(test)]
mod tests {
    use super::normalize_legacy_args;
    use std::ffi::OsString;

    #[test]
    fn rewrites_legacy_proxy_command_flag() {
        let args = normalize_legacy_args([
            OsString::from("och"),
            OsString::from("--proxy-command"),
            OsString::from("host"),
            OsString::from("22"),
        ]);
        assert_eq!(args[1], "proxy-command");
    }
}
