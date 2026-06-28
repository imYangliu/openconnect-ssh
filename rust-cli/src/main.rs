use clap::{CommandFactory, Parser, Subcommand};
use std::ffi::OsString;
use std::path::PathBuf;
use std::process::Command;

mod config;
mod doctor;
mod i18n;
mod platform;
mod service;
mod setup;
mod tui;
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
    /// Open the full terminal user interface.
    #[command(visible_alias = "t")]
    Tui,
    /// Diagnose local OCH configuration and runtime prerequisites.
    #[command(visible_alias = "d")]
    Doctor,
    /// SSH ProxyCommand entrypoint: ensure VPN, then connect stdio to host:port.
    ProxyCommand { host: String, port: String },
    /// Manage the VPN connection.
    #[command(visible_alias = "v")]
    Vpn {
        #[command(subcommand)]
        command: Option<VpnCommand>,
    },
    /// Manage the macOS privileged service.
    #[command(visible_alias = "svc")]
    Service {
        #[command(subcommand)]
        command: Option<ServiceCommand>,
    },
    /// Upgrade OCH by running the official installer again.
    Update,
    /// Show help.
    Help,
}

#[derive(Subcommand, Debug, Clone, Copy)]
enum VpnCommand {
    #[command(visible_alias = "c")]
    Connect,
    #[command(visible_alias = "x")]
    Disconnect,
    #[command(visible_alias = "s")]
    Status,
    #[command(visible_alias = "v")]
    Verify,
    Ssh,
    #[command(visible_alias = "l")]
    Logs,
    Help,
}

#[derive(Subcommand, Debug, Clone, Copy)]
enum ServiceCommand {
    #[command(visible_alias = "i")]
    Install,
    #[command(visible_alias = "u")]
    Uninstall,
    #[command(visible_alias = "st")]
    Status,
    #[command(hide = true)]
    Exec,
    #[command(hide = true)]
    Run,
    Help,
}

fn main() {
    if let Err(error) = run() {
        if error != doctor::FAILURE_EXIT {
            eprintln!("Error: {error}");
        }
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
        Commands::Setup => setup::run(),
        Commands::Tui => tui::run(),
        Commands::Doctor => doctor::run(),
        Commands::ProxyCommand { host, port } => proxy_command(&host, &port),
        Commands::Vpn { command } => run_vpn_command(command.unwrap_or(VpnCommand::Help)),
        Commands::Service { command } => {
            run_service_command(command.unwrap_or(ServiceCommand::Help))
        }
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

fn run_service_command(command: ServiceCommand) -> Result<(), String> {
    match command {
        ServiceCommand::Install => service::install(),
        ServiceCommand::Uninstall => service::uninstall(),
        ServiceCommand::Status => service::status(),
        ServiceCommand::Exec => service::exec_from_stdin(),
        ServiceCommand::Run => service::run_daemon(),
        ServiceCommand::Help => {
            print_service_help();
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
    let command_text =
        update_command_text(find_tool("curl").is_some(), find_tool("wget").is_some())?;

    exec_command(
        Command::new("bash")
            .arg("-c")
            .arg(command_text)
            .arg("och-update")
            .arg(install_url),
    )
}

fn update_command_text(has_curl: bool, has_wget: bool) -> Result<&'static str, String> {
    if has_curl {
        Ok(r#"curl -fsSL "$1" | bash -s -- --update"#)
    } else if has_wget {
        Ok(r#"wget -qO- "$1" | bash -s -- --update"#)
    } else {
        Err("och update 需要 curl 或 wget".into())
    }
}

fn proxy_command(host: &str, port: &str) -> Result<(), String> {
    if host.is_empty() {
        return Err("proxy-command 缺少 host".into());
    }
    if port.is_empty() {
        return Err("proxy-command 缺少 port".into());
    }

    let mut runtime = load_runtime(false).map_err(|error| error.to_string())?;
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

    let runtime = load_runtime(false).map_err(|error| error.to_string())?;
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
    print!("{}", vpn_help_text());
}

fn vpn_help_text() -> &'static str {
    "\
OCH VPN commands

Common:
  och vpn connect
  och vpn status
  och vpn verify
  och vpn logs

Aliases:
  och v c        # och vpn connect
  och vpn s      # och vpn status
  och vpn x      # och vpn disconnect
  och vpn l      # och vpn logs

Examples:
  och vpn connect
  och vpn status
  och vpn disconnect
"
}

fn print_service_help() {
    print!("{}", service_help_text());
}

fn service_help_text() -> &'static str {
    "\
OCH service commands

Common:
  och service install
  och service status

Aliases:
  och svc st       # och service status
  och service i    # och service install
  och service u    # och service uninstall

Examples:
  och service status
  och service install
  och service uninstall
"
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
    use super::{
        normalize_legacy_args, service_help_text, update_command_text, vpn_help_text, Cli,
        Commands, ServiceCommand, VpnCommand, DEFAULT_INSTALL_URL,
    };
    use clap::Parser;
    use std::ffi::OsString;

    fn parse_command(args: &[&str]) -> Commands {
        Cli::try_parse_from(args).unwrap().command.unwrap()
    }

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

    #[test]
    fn update_prefers_curl() {
        let command = update_command_text(true, true).expect("curl update command");
        assert!(command.starts_with("curl -fsSL"));
        assert!(command.ends_with("bash -s -- --update"));
    }

    #[test]
    fn update_uses_wget_when_curl_is_missing() {
        let command = update_command_text(false, true).expect("wget update command");
        assert!(command.starts_with("wget -qO-"));
        assert!(command.ends_with("bash -s -- --update"));
    }

    #[test]
    fn update_requires_a_downloader() {
        let error = update_command_text(false, false).expect_err("missing downloader");
        assert!(error.contains("curl 或 wget"));
    }

    #[test]
    fn default_update_installer_points_to_github_raw_script() {
        assert_eq!(
            DEFAULT_INSTALL_URL,
            "https://raw.githubusercontent.com/imyangliu/openconnect-ssh/main/install.sh"
        );
    }

    #[test]
    fn top_level_aliases_parse_to_their_commands() {
        assert!(matches!(parse_command(&["och", "t"]), Commands::Tui));
        assert!(matches!(parse_command(&["och", "d"]), Commands::Doctor));
        assert!(matches!(
            parse_command(&["och", "v", "s"]),
            Commands::Vpn {
                command: Some(VpnCommand::Status)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "svc", "st"]),
            Commands::Service {
                command: Some(ServiceCommand::Status)
            }
        ));
    }

    #[test]
    fn nested_command_aliases_parse_to_their_commands() {
        assert!(matches!(
            parse_command(&["och", "vpn", "c"]),
            Commands::Vpn {
                command: Some(VpnCommand::Connect)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "vpn", "x"]),
            Commands::Vpn {
                command: Some(VpnCommand::Disconnect)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "vpn", "v"]),
            Commands::Vpn {
                command: Some(VpnCommand::Verify)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "vpn", "l"]),
            Commands::Vpn {
                command: Some(VpnCommand::Logs)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "service", "i"]),
            Commands::Service {
                command: Some(ServiceCommand::Install)
            }
        ));
        assert!(matches!(
            parse_command(&["och", "service", "u"]),
            Commands::Service {
                command: Some(ServiceCommand::Uninstall)
            }
        ));
    }

    #[test]
    fn manual_help_text_lists_aliases_and_examples() {
        let vpn_help = vpn_help_text();
        assert!(vpn_help.contains("Common"));
        assert!(vpn_help.contains("Aliases"));
        assert!(vpn_help.contains("och v c"));
        assert!(vpn_help.contains("och vpn s"));

        let service_help = service_help_text();
        assert!(service_help.contains("Common"));
        assert!(service_help.contains("Aliases"));
        assert!(service_help.contains("och svc st"));
        assert!(service_help.contains("och service i"));
    }
}
