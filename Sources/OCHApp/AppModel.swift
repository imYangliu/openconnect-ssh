import Foundation

@MainActor
final class AppModel: ObservableObject {
    @Published var config = AppConfig()
    @Published var vpnPassword = ""
    @Published var savePassword = true
    @Published var configText = ""
    @Published var logText = ""
    @Published var isBusy = false
    @Published var includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()

    private var lastSyncedConfigText = ""

    init() {
        loadConfiguration()
    }

    func loadConfiguration() {
        do {
            if FileManager.default.fileExists(atPath: ConfigPaths.configTOML.path) {
                config = try TOMLConfigFile.load(from: ConfigPaths.configTOML)
                append(L10n.tr("log.loaded_config", ConfigPaths.configTOML.path))
            }
            refreshConfigTextFromSettings(log: false)
            if let password = try KeychainStore.readPassword(account: config.vpnUser) {
                vpnPassword = password
            }
        } catch {
            append(L10n.tr("log.load_failed", error.localizedDescription))
            refreshConfigTextFromSettings(log: false)
        }
    }

    func saveConfiguration() {
        do {
            try synchronizeSettingsForSave()
            try TOMLConfigFile.save(config, to: ConfigPaths.configTOML)
            try SSHConfigManager.writeManagedHost(config: config)
            if savePassword, !vpnPassword.isEmpty {
                try KeychainStore.savePassword(vpnPassword, account: config.vpnUser)
            }
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append(L10n.tr("log.saved_config", ConfigPaths.configTOML.path))
            append(L10n.tr("log.updated_ssh_host", ConfigPaths.managedSSHConfig.path))
        } catch {
            append(L10n.tr("log.save_failed", error.localizedDescription))
        }
    }

    func applyConfigTextToSettings() {
        do {
            config = try TOMLConfigFile.parse(configText)
            refreshConfigTextFromSettings(log: false)
            append(L10n.tr("log.applied_toml"))
        } catch {
            append(L10n.tr("log.toml_apply_failed", error.localizedDescription))
        }
    }

    func refreshConfigTextFromSettings(log: Bool = true) {
        configText = TOMLConfigFile.render(config)
        lastSyncedConfigText = configText
        if log {
            append(L10n.tr("log.refreshed_toml"))
        }
    }

    func syncConfigTextAfterSettingsChange() {
        guard configText == lastSyncedConfigText else {
            return
        }
        refreshConfigTextFromSettings(log: false)
    }

    func deleteSavedPassword() {
        do {
            try KeychainStore.deletePassword(account: config.vpnUser)
            vpnPassword = ""
            append(L10n.tr("log.removed_keychain_password"))
        } catch {
            append(L10n.tr("log.keychain_delete_failed", error.localizedDescription))
        }
    }

    func installSSHInclude() {
        do {
            try SSHConfigManager.writeManagedHost(config: config)
            try SSHConfigManager.ensureIncludeLine()
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append(L10n.tr("log.installed_ssh_include", SSHConfigManager.includeLine))
        } catch {
            append(L10n.tr("log.ssh_config_update_failed", error.localizedDescription))
        }
    }

    func connect() {
        saveConfiguration()
        guard !config.vpnUser.isEmpty else {
            append(L10n.tr("log.vpn_user_required"))
            return
        }

        let password = vpnPassword
        guard !password.isEmpty else {
            append(L10n.tr("log.vpn_password_required"))
            return
        }

        runVPNCommand(["connect"], stdin: "\(password)\n")
    }

    func disconnect() {
        runVPNCommand(["disconnect"])
    }

    func refreshStatus() {
        runVPNCommand(["status"])
    }

    private func runVPNCommand(_ arguments: [String], stdin: String? = nil) {
        isBusy = true
        let executable = config.ochVpnPath
        let environment = commandEnvironment()
        append("$ \(executable) \(arguments.joined(separator: " "))")

        Task {
            let result: Result<CommandResult, Error> = await Task.detached {
                do {
                    return .success(try CommandRunner.run(
                        executable: executable,
                        arguments: arguments,
                        environment: environment,
                        stdin: stdin
                    ))
                } catch {
                    return .failure(error)
                }
            }.value

            switch result {
            case .success(let commandResult):
                append(commandResult.output.trimmingCharacters(in: .whitespacesAndNewlines))
                append(L10n.tr("log.exit_status", commandResult.status))
            case .failure(let error):
                append(L10n.tr("log.command_failed", error.localizedDescription))
            }
            isBusy = false
        }
    }

    private func commandEnvironment() -> [String: String] {
        [
            "OCH_CONFIG_FILE": ConfigPaths.configTOML.path,
            "SUDO_ASKPASS": config.askpassPath
        ]
    }

    private func synchronizeSettingsForSave() throws {
        if configText != lastSyncedConfigText {
            config = try TOMLConfigFile.parse(configText)
        }
        refreshConfigTextFromSettings(log: false)
    }

    private func append(_ message: String) {
        guard !message.isEmpty else { return }
        if !logText.isEmpty {
            logText += "\n"
        }
        logText += message
    }
}
