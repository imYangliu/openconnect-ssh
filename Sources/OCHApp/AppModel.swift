import Foundation

@MainActor
final class AppModel: ObservableObject {
    @Published var config = AppConfig()
    @Published var vpnPassword = ""
    @Published var savePassword = true
    @Published var logText = ""
    @Published var isBusy = false
    @Published var includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()

    init() {
        do {
            config = try EnvFile.load(from: ConfigPaths.guiEnv)
            if let password = try KeychainStore.readPassword(account: config.vpnUser) {
                vpnPassword = password
            }
        } catch {
            append("Load failed: \(error.localizedDescription)")
        }
    }

    func saveConfiguration() {
        do {
            try EnvFile.save(config, to: ConfigPaths.guiEnv)
            try SSHConfigManager.writeManagedHost(config: config)
            if savePassword, !vpnPassword.isEmpty {
                try KeychainStore.savePassword(vpnPassword, account: config.vpnUser)
            }
            append("Saved config: \(ConfigPaths.guiEnv.path)")
            append("Updated SSH host: \(ConfigPaths.managedSSHConfig.path)")
        } catch {
            append("Save failed: \(error.localizedDescription)")
        }
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

    private func append(_ message: String) {
        guard !message.isEmpty else { return }
        if !logText.isEmpty {
            logText += "\n"
        }
        logText += message
    }
}
