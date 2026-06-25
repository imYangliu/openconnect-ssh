import Foundation
import Security

@MainActor
final class AppModel: ObservableObject {
    @Published var config = AppConfig()
    @Published var vpnPassword = ""
    @Published var savePassword = true
    @Published var configText = ""
    @Published var logText = ""
    @Published var isBusy = false
    @Published var includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
    @Published var showingSetupWizard = false
    @Published var connectionStatusText = ""
    @Published var connectionStatusIsError = false
    @Published var sshStatusText = ""
    @Published var sshStatusIsError = false
    @Published var configStatusText = ""
    @Published var configStatusIsError = false
    @Published var advancedStatusText = ""
    @Published var advancedStatusIsError = false

    private var lastSyncedConfigText = ""

    init() {
        loadConfiguration()
        showingSetupWizard = needsSetup
    }

    var needsSetup: Bool {
        config.vpnHost.isEmpty
            || config.vpnUser.isEmpty
            || config.defaultHost.isEmpty
            || config.targetHost.isEmpty
            || config.targetUser.isEmpty
            || config.targetPort.isEmpty
    }

    var hasUnsavedConfigTextChanges: Bool {
        configText != lastSyncedConfigText
    }

    func loadConfiguration(reportToConfigPane: Bool = false) {
        do {
            if FileManager.default.fileExists(atPath: ConfigPaths.configTOML.path) {
                config = try TOMLConfigFile.load(from: ConfigPaths.configTOML)
                append(L10n.tr("log.loaded_config", language: config.appLanguage, ConfigPaths.configTOML.path))
            }
            refreshConfigTextFromSettings(log: false)
            if reportToConfigPane {
                setConfigStatus(L10n.tr("log.loaded_config", language: config.appLanguage, ConfigPaths.configTOML.path))
            }
        } catch {
            let message = L10n.tr("log.load_failed", language: config.appLanguage, localizedDescription(error))
            append(message)
            if reportToConfigPane {
                setConfigStatus(message, isError: true)
            }
            refreshConfigTextFromSettings(log: false)
        }

        do {
            if let password = try KeychainStore.readPassword(account: config.vpnUser) {
                vpnPassword = password
            }
        } catch {
            append(L10n.tr("log.keychain_read_failed", language: config.appLanguage, keychainReadDescription(error)))
        }
    }

    @discardableResult
    func saveConfiguration(reportToConfigPane: Bool = true) -> Bool {
        do {
            try synchronizeSettingsForSave()
            let helperPaths = try HelperPathResolver.resolveAll()
            try TOMLConfigFile.save(config, to: ConfigPaths.configTOML)
            try SSHConfigManager.writeManagedHost(config: config, ochPath: helperPaths.och.path)
            if savePassword, !vpnPassword.isEmpty {
                try KeychainStore.savePassword(vpnPassword, account: config.vpnUser)
            }
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append(L10n.tr("log.saved_config", language: config.appLanguage, ConfigPaths.configTOML.path))
            append(L10n.tr("log.updated_ssh_host", language: config.appLanguage, ConfigPaths.managedSSHConfig.path))
            if reportToConfigPane {
                setConfigStatus(L10n.tr("status.saved_config", language: config.appLanguage, ConfigPaths.configTOML.path))
            }
            return true
        } catch {
            let message = L10n.tr("log.save_failed", language: config.appLanguage, localizedDescription(error))
            append(message)
            if reportToConfigPane {
                setConfigStatus(message, isError: true)
            }
            return false
        }
    }

    @discardableResult
    func completeSetup(config newConfig: AppConfig, password: String) -> Bool {
        do {
            config = newConfig
            vpnPassword = password
            refreshConfigTextFromSettings(log: false)
            let helperPaths = try HelperPathResolver.resolveAll()
            try TOMLConfigFile.save(config, to: ConfigPaths.configTOML)
            try SSHConfigManager.writeManagedHost(config: config, ochPath: helperPaths.och.path)
            try SSHConfigManager.ensureIncludeLine()
            if savePassword, !vpnPassword.isEmpty {
                try KeychainStore.savePassword(vpnPassword, account: config.vpnUser)
            }
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append(L10n.tr("log.setup_completed", language: config.appLanguage, ConfigPaths.configTOML.path))
            return true
        } catch {
            append(L10n.tr("log.setup_failed", language: config.appLanguage, localizedDescription(error)))
            return false
        }
    }

    func applyConfigTextToSettings() {
        do {
            config = try TOMLConfigFile.parse(configText)
            refreshConfigTextFromSettings(log: false)
            append(L10n.tr("log.applied_toml", language: config.appLanguage))
            setConfigStatus(L10n.tr("log.applied_toml", language: config.appLanguage))
        } catch {
            let message = L10n.tr("log.toml_apply_failed", language: config.appLanguage, localizedDescription(error))
            append(message)
            setConfigStatus(message, isError: true)
        }
    }

    func refreshConfigTextFromSettings(log: Bool = true) {
        configText = TOMLConfigFile.render(config)
        lastSyncedConfigText = configText
        if log {
            append(L10n.tr("log.refreshed_toml", language: config.appLanguage))
            setConfigStatus(L10n.tr("log.refreshed_toml", language: config.appLanguage))
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
            append(L10n.tr("log.removed_keychain_password", language: config.appLanguage))
            setAdvancedStatus(L10n.tr("log.removed_keychain_password", language: config.appLanguage))
        } catch {
            let message = L10n.tr("log.keychain_delete_failed", language: config.appLanguage, localizedDescription(error))
            append(message)
            setAdvancedStatus(message, isError: true)
        }
    }

    func installSSHInclude() {
        do {
            let helperPaths = try HelperPathResolver.resolveAll()
            try SSHConfigManager.writeManagedHost(config: config, ochPath: helperPaths.och.path)
            try SSHConfigManager.ensureIncludeLine()
            includeInstalled = SSHConfigManager.mainConfigIncludesManagedFile()
            append(L10n.tr("log.installed_ssh_include", language: config.appLanguage, SSHConfigManager.includeLine))
            setSSHStatus(L10n.tr("status.ssh_include.install_success", language: config.appLanguage, SSHConfigManager.includeLine))
        } catch {
            let message = L10n.tr("log.ssh_config_update_failed", language: config.appLanguage, localizedDescription(error))
            append(message)
            setSSHStatus(message, isError: true)
        }
    }

    func connect() {
        setConnectionStatus(L10n.tr("status.connection.saving", language: config.appLanguage))
        guard !config.vpnHost.isEmpty else {
            let message = L10n.tr("validation.vpn_gateway_required", language: config.appLanguage)
            append(message)
            setConnectionStatus(message, isError: true)
            return
        }
        guard !config.vpnUser.isEmpty else {
            let message = L10n.tr("validation.vpn_user_required", language: config.appLanguage)
            append(message)
            setConnectionStatus(message, isError: true)
            return
        }

        let password = vpnPassword
        guard !password.isEmpty else {
            let message = L10n.tr("validation.vpn_password_required", language: config.appLanguage)
            append(message)
            setConnectionStatus(message, isError: true)
            return
        }

        guard saveConfiguration(reportToConfigPane: false) else {
            setConnectionStatus(L10n.tr("status.connection.save_failed", language: config.appLanguage), isError: true)
            return
        }

        runVPNCommand(["connect"], vpnPassword: password)
    }

    func disconnect() {
        runVPNCommand(["disconnect"])
    }

    func refreshStatus() {
        runVPNCommand(["status"])
    }

    private func runVPNCommand(_ arguments: [String], vpnPassword: String? = nil) {
        let helperPaths: ResolvedHelperPaths
        do {
            helperPaths = try HelperPathResolver.resolveAll()
        } catch {
            let message = localizedDescription(error)
            append(message)
            setConnectionStatus(message, isError: true)
            return
        }

        isBusy = true
        let executable = helperPaths.och.path
        let commandArguments = ["vpn"] + arguments
        let environment = commandEnvironment(askpassPath: helperPaths.askpass.path, vpnPassword: vpnPassword)
        append("$ \(executable) \(commandArguments.joined(separator: " "))")
        setConnectionStatus(L10n.tr("status.connection.running", language: config.appLanguage, arguments.joined(separator: " ")))

        Task {
            let result: Result<CommandResult, Error> = await Task.detached {
                do {
                    return .success(try CommandRunner.run(
                        executable: executable,
                        arguments: commandArguments,
                        environment: environment,
                        stdin: nil
                    ))
                } catch {
                    return .failure(error)
                }
            }.value

            switch result {
            case .success(let commandResult):
                append(commandResult.output.trimmingCharacters(in: .whitespacesAndNewlines))
                append(L10n.tr("log.exit_status", language: config.appLanguage, commandResult.status))
                setConnectionStatus(
                    L10n.tr("status.connection.completed", language: config.appLanguage, commandResult.status),
                    isError: commandResult.status != 0
                )
            case .failure(let error):
                let message = L10n.tr("log.command_failed", language: config.appLanguage, localizedDescription(error))
                append(message)
                setConnectionStatus(message, isError: true)
            }
            isBusy = false
        }
    }

    private func commandEnvironment(askpassPath: String, vpnPassword: String? = nil) -> [String: String] {
        var environment = [
            "OCH_CONFIG_FILE": ConfigPaths.configTOML.path,
            "SUDO_ASKPASS": askpassPath
        ]
        if let vpnPassword {
            environment["VPN_PASSWORD"] = vpnPassword
        }
        return environment
    }

    private func synchronizeSettingsForSave() throws {
        if configText != lastSyncedConfigText {
            config = try TOMLConfigFile.parse(configText)
        }
        refreshConfigTextFromSettings(log: false)
    }

    private func localizedDescription(_ error: Error) -> String {
        if let tomlError = error as? TOMLConfigError {
            return tomlError.localizedDescription(language: config.appLanguage)
        }
        if let helperError = error as? HelperPathError {
            return L10n.tr("error.helper_not_found", language: config.appLanguage, helperError.kind.displayName)
        }
        return error.localizedDescription
    }

    private func keychainReadDescription(_ error: Error) -> String {
        let nsError = error as NSError
        if nsError.domain == NSOSStatusErrorDomain, nsError.code == Int(errSecUserCanceled) {
            return L10n.tr("error.keychain.user_canceled", language: config.appLanguage)
        }
        return localizedDescription(error)
    }

    private func append(_ message: String) {
        guard !message.isEmpty else { return }
        if !logText.isEmpty {
            logText += "\n"
        }
        logText += message
    }

    private func setConnectionStatus(_ message: String, isError: Bool = false) {
        connectionStatusText = message
        connectionStatusIsError = isError
    }

    private func setSSHStatus(_ message: String, isError: Bool = false) {
        sshStatusText = message
        sshStatusIsError = isError
    }

    private func setConfigStatus(_ message: String, isError: Bool = false) {
        configStatusText = message
        configStatusIsError = isError
    }

    private func setAdvancedStatus(_ message: String, isError: Bool = false) {
        advancedStatusText = message
        advancedStatusIsError = isError
    }
}
