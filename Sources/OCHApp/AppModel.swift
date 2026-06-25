import Foundation

@MainActor
final class AppModel: ObservableObject {
    @Published var config = AppConfig()
    @Published var vpnPassword = ""
    @Published var savePassword = true
    @Published var yamlText = ""
    @Published var logText = ""
    @Published var isBusy = false
    @Published var includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()

    private var lastSyncedYAML = ""

    init() {
        loadConfiguration()
    }

    func loadConfiguration() {
        do {
            if FileManager.default.fileExists(atPath: ConfigPaths.guiYAML.path) {
                config = try YAMLConfigFile.load(from: ConfigPaths.guiYAML)
                append("Loaded config: \(ConfigPaths.guiYAML.path)")
            } else {
                config = try EnvFile.load(from: ConfigPaths.guiEnv)
                append("Loaded config: \(ConfigPaths.guiEnv.path)")
            }
            refreshYAMLFromSettings(log: false)
            if let password = try KeychainStore.readPassword(account: config.vpnUser) {
                vpnPassword = password
            }
        } catch {
            append("Load failed: \(error.localizedDescription)")
            refreshYAMLFromSettings(log: false)
        }
    }

    func saveConfiguration() {
        do {
            try synchronizeSettingsForSave()
            try YAMLConfigFile.save(config, to: ConfigPaths.guiYAML)
            try EnvFile.save(config, to: ConfigPaths.guiEnv)
            try SSHConfigManager.writeManagedHost(config: config)
            if savePassword, !vpnPassword.isEmpty {
                try KeychainStore.savePassword(vpnPassword, account: config.vpnUser)
            }
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append("Saved config: \(ConfigPaths.guiYAML.path)")
            append("Updated env compatibility file: \(ConfigPaths.guiEnv.path)")
            append("Updated SSH host: \(ConfigPaths.managedSSHConfig.path)")
        } catch {
            append("Save failed: \(error.localizedDescription)")
        }
    }

    func applyYAMLToSettings() {
        do {
            config = try YAMLConfigFile.parse(yamlText)
            refreshYAMLFromSettings(log: false)
            append("Applied YAML to settings")
        } catch {
            append("YAML apply failed: \(error.localizedDescription)")
        }
    }

    func refreshYAMLFromSettings(log: Bool = true) {
        yamlText = YAMLConfigFile.render(config)
        lastSyncedYAML = yamlText
        if log {
            append("Refreshed YAML from settings")
        }
    }

    func syncYAMLAfterSettingsChange() {
        guard yamlText == lastSyncedYAML else {
            return
        }
        refreshYAMLFromSettings(log: false)
    }

    func deleteSavedPassword() {
        do {
            try KeychainStore.deletePassword(account: config.vpnUser)
            vpnPassword = ""
            append("Removed VPN password from Keychain")
        } catch {
            append("Keychain delete failed: \(error.localizedDescription)")
        }
    }

    func installSSHInclude() {
        do {
            try SSHConfigManager.writeManagedHost(config: config)
            try SSHConfigManager.ensureIncludeLine()
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append("Installed SSH Include: \(SSHConfigManager.includeLine)")
        } catch {
            append("SSH config update failed: \(error.localizedDescription)")
        }
    }

    func connect() {
        saveConfiguration()
        guard !config.vpnUser.isEmpty else {
            append("VPN_USER is required")
            return
        }

        let password = vpnPassword
        guard !password.isEmpty else {
            append("VPN password is required. Enter it in the password field or save it to Keychain.")
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
                append("Exit status: \(commandResult.status)")
            case .failure(let error):
                append("Command failed: \(error.localizedDescription)")
            }
            isBusy = false
        }
    }

    private func commandEnvironment() -> [String: String] {
        [
            "ENV_FILE": ConfigPaths.guiEnv.path,
            "CONFIG_FILE": ConfigPaths.guiEnv.path,
            "SUDO_ASKPASS": config.askpassPath
        ]
    }

    private func synchronizeSettingsForSave() throws {
        if yamlText != lastSyncedYAML {
            config = try YAMLConfigFile.parse(yamlText)
        }
        refreshYAMLFromSettings(log: false)
    }

    private func append(_ message: String) {
        guard !message.isEmpty else { return }
        if !logText.isEmpty {
            logText += "\n"
        }
        logText += message
    }
}
