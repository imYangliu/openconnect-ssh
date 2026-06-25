import SwiftUI

struct SetupWizardView: View {
    @ObservedObject var model: AppModel
    @Environment(\.dismiss) private var dismiss

    @State private var step = 0
    @State private var draft: AppConfig
    @State private var password: String
    @State private var routeCIDR = ""
    @State private var discoveredHosts: [SSHHostChoice] = []
    @State private var selectedHost = ""
    @State private var authGroups: [String] = []
    @State private var selectedAuthGroup = ""
    @State private var statusText = ""
    @State private var isWorking = false

    init(model: AppModel) {
        self.model = model
        self._draft = State(initialValue: model.config)
        self._password = State(initialValue: model.vpnPassword)
        self._routeCIDR = State(initialValue: SetupCIDRHelper.defaultCIDR(for: model.config.targetHost))
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if step == 0 {
                vpnStep
            } else {
                sshStep
            }
            Divider()
            footer
        }
        .frame(width: 680, height: 560)
        .onAppear {
            discoveredHosts = SetupSSHDiscovery.discoverHosts()
        }
    }

    private func tr(_ key: String) -> String {
        L10n.tr(key, language: draft.appLanguage)
    }

    private var header: some View {
        HStack(spacing: 12) {
            Image(systemName: step == 0 ? "lock.shield" : "terminal")
                .foregroundStyle(Color.accentColor)
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: tr("setup.title"))
                    .font(.headline)
                Text(verbatim: step == 0 ? tr("setup.step.vpn") : tr("setup.step.ssh"))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(18)
    }

    private var vpnStep: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                WizardSection(title: tr("section.vpn"), systemImage: "lock.shield") {
                    WizardFieldRow(
                        tr("field.gateway"),
                        text: $draft.vpnHost,
                        placeholder: tr("placeholder.gateway"),
                        help: tr("help.gateway"),
                        error: requiredError(draft.vpnHost, key: "validation.vpn_gateway_required")
                    )
                    WizardFieldRow(
                        tr("field.user"),
                        text: $draft.vpnUser,
                        placeholder: tr("placeholder.vpn_user"),
                        error: requiredError(draft.vpnUser, key: "validation.vpn_user_required")
                    )
                    WizardSecureFieldRow(
                        tr("field.password"),
                        text: $password,
                        help: tr("help.password"),
                        error: requiredError(password, key: "validation.vpn_password_required")
                    )

                    HStack(alignment: .firstTextBaseline, spacing: 12) {
                        Text(verbatim: tr("field.auth_group"))
                            .foregroundStyle(.secondary)
                            .frame(width: UILayout.labelWidth, alignment: .trailing)
                        VStack(alignment: .leading, spacing: 8) {
                            if authGroups.isEmpty {
                                TextField(tr("placeholder.auth_group"), text: $draft.vpnAuthGroup)
                                    .textFieldStyle(.roundedBorder)
                                    .accessibilityLabel(Text(verbatim: tr("field.auth_group")))
                            } else {
                                Picker(tr("field.auth_group"), selection: $selectedAuthGroup) {
                                    Text(verbatim: tr("setup.auth_group.manual")).tag("")
                                    ForEach(authGroups, id: \.self) { group in
                                        Text(verbatim: group).tag(group)
                                    }
                                }
                                .pickerStyle(.menu)
                                .onChange(of: selectedAuthGroup) { value in
                                    if !value.isEmpty {
                                        draft.vpnAuthGroup = value
                                    }
                                }
                                TextField(tr("field.auth_group"), text: $draft.vpnAuthGroup)
                                    .textFieldStyle(.roundedBorder)
                                    .accessibilityLabel(Text(verbatim: tr("field.auth_group")))
                            }
                            HelpText(tr("help.auth_group"))
                        }
                    }

                    Button {
                        probeAuthGroups()
                    } label: {
                        Label {
                            Text(verbatim: tr("button.probe_auth_groups"))
                        } icon: {
                            Image(systemName: "magnifyingglass")
                        }
                    }
                    .disabled(isWorking || draft.vpnHost.isEmpty || draft.vpnUser.isEmpty)
                }

                if !statusText.isEmpty {
                    Text(verbatim: statusText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(22)
            .frame(maxWidth: .infinity, alignment: .topLeading)
        }
    }

    private var sshStep: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                WizardSection(title: tr("setup.section.choose_host"), systemImage: "terminal") {
                    if discoveredHosts.isEmpty {
                        Text(verbatim: tr("setup.no_ssh_hosts"))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        HStack(spacing: 10) {
                            Picker(tr("setup.existing_host"), selection: $selectedHost) {
                                Text(verbatim: tr("setup.manual_host")).tag("")
                                ForEach(discoveredHosts) { host in
                                    Text(verbatim: host.name).tag(host.name)
                                }
                            }
                            .pickerStyle(.menu)
                            .onChange(of: selectedHost) { value in
                                if value.isEmpty {
                                    statusText = ""
                                } else {
                                    resolveSelectedHost()
                                }
                            }
                        }
                    }
                }

                WizardSection(title: tr("section.managed_ssh_host"), systemImage: "server.rack") {
                    WizardFieldRow(
                        tr("field.host"),
                        text: $draft.defaultHost,
                        placeholder: tr("placeholder.host"),
                        help: tr("help.host"),
                        error: requiredError(draft.defaultHost, key: "validation.ssh_host_required")
                    )
                    WizardFieldRow(
                        tr("field.hostname"),
                        text: $draft.targetHost,
                        placeholder: tr("placeholder.hostname"),
                        help: tr("help.hostname"),
                        error: requiredError(draft.targetHost, key: "validation.hostname_required")
                    )
                    WizardFieldRow(
                        tr("field.user"),
                        text: $draft.targetUser,
                        placeholder: tr("placeholder.ssh_user"),
                        error: requiredError(draft.targetUser, key: "validation.ssh_user_required")
                    )
                    WizardFieldRow(
                        tr("field.port"),
                        text: $draft.targetPort,
                        placeholder: tr("placeholder.port"),
                        error: portError(draft.targetPort, requiredKey: "validation.port_required")
                    )
                    WizardFieldRow(
                        tr("field.route_cidr"),
                        text: $routeCIDR,
                        placeholder: tr("placeholder.route_cidr"),
                        help: tr("help.route_cidr"),
                        error: routeCIDRError
                    )
                }

                if !statusText.isEmpty {
                    Text(verbatim: statusText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(22)
            .frame(maxWidth: .infinity, alignment: .topLeading)
        }
    }

    private var footer: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let blockingReason {
                InlineIssue(blockingReason)
            }

            HStack {
                Button {
                    dismiss()
                } label: {
                    Text(verbatim: tr("button.cancel"))
                }

                Spacer()

                if isWorking {
                    ProgressView()
                        .controlSize(.small)
                }

                if step == 1 {
                    Button {
                        step = 0
                    } label: {
                        Text(verbatim: tr("button.back"))
                    }
                }

                Button {
                    if step == 0 {
                        step = 1
                        statusText = ""
                    } else {
                        finish()
                    }
                } label: {
                    Text(verbatim: step == 0 ? tr("button.next") : tr("button.finish_setup"))
                }
                .buttonStyle(.borderedProminent)
                .disabled(!canContinue)
            }
        }
        .padding(18)
    }

    private var canContinue: Bool {
        blockingReason == nil
    }

    private var blockingReason: String? {
        if isWorking {
            return tr("setup.working")
        }
        if step == 0 {
            return requiredError(draft.vpnHost, key: "validation.vpn_gateway_required")
                ?? requiredError(draft.vpnUser, key: "validation.vpn_user_required")
                ?? requiredError(password, key: "validation.vpn_password_required")
        }
        return requiredError(draft.defaultHost, key: "validation.ssh_host_required")
            ?? requiredError(draft.targetHost, key: "validation.hostname_required")
            ?? requiredError(draft.targetUser, key: "validation.ssh_user_required")
            ?? portError(draft.targetPort, requiredKey: "validation.port_required")
            ?? routeCIDRError
    }

    private var routeCIDRIsInvalid: Bool {
        !routeCIDR.isEmpty && !SetupCIDRHelper.isValidCIDR(routeCIDR)
    }

    private var routeCIDRError: String? {
        routeCIDRIsInvalid ? L10n.tr("validation.route_cidr_invalid", language: draft.appLanguage, routeCIDR) : nil
    }

    private func requiredError(_ value: String, key: String) -> String? {
        value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? tr(key) : nil
    }

    private func portError(_ value: String, requiredKey: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return tr(requiredKey)
        }
        guard let port = Int(trimmed), (1...65535).contains(port) else {
            return tr("validation.port_invalid")
        }
        return nil
    }

    private func probeAuthGroups() {
        isWorking = true
        statusText = ""
        let openconnectPath = ""
        let host = draft.vpnHost
        let user = draft.vpnUser

        Task {
            let result: Result<[String], Error> = await Task.detached {
                do {
                    return .success(try SetupAuthGroupProbe.probe(
                        openconnectPath: openconnectPath,
                        host: host,
                        user: user
                    ))
                } catch {
                    return .failure(error)
                }
            }.value

            switch result {
            case .success(let groups):
                authGroups = groups
                statusText = groups.isEmpty
                    ? tr("setup.auth_group.none")
                    : L10n.tr("setup.auth_group.found", language: draft.appLanguage, groups.count)
            case .failure(let error):
                statusText = L10n.tr("setup.auth_group.failed", language: draft.appLanguage, error.localizedDescription)
                    + " "
                    + tr("setup.auth_group.recovery")
            }
            isWorking = false
        }
    }

    private func resolveSelectedHost() {
        guard !selectedHost.isEmpty else { return }
        isWorking = true
        statusText = ""
        let host = selectedHost

        Task {
            let result: Result<ResolvedSSHHost, Error> = await Task.detached {
                do {
                    return .success(try SetupSSHDiscovery.resolve(host: host))
                } catch {
                    return .failure(error)
                }
            }.value

            switch result {
            case .success(let resolved):
                draft.defaultHost = resolved.alias
                draft.targetHost = resolved.hostName
                draft.targetUser = resolved.user
                draft.targetPort = resolved.port
                routeCIDR = SetupCIDRHelper.defaultCIDR(for: resolved.hostName)
                statusText = L10n.tr(
                    "setup.ssh.resolved",
                    language: draft.appLanguage,
                    host,
                    resolved.hostName,
                    resolved.user,
                    resolved.port
                )
            case .failure(let error):
                statusText = L10n.tr("setup.ssh.resolve_failed", language: draft.appLanguage, error.localizedDescription)
            }
            isWorking = false
        }
    }

    private func finish() {
        if !routeCIDR.isEmpty, !SetupCIDRHelper.isValidCIDR(routeCIDR) {
            statusText = L10n.tr("setup.route.invalid", language: draft.appLanguage, routeCIDR)
            return
        }

        var finalConfig = draft
        finalConfig.extraRoutesText = SetupCIDRHelper.append(route: routeCIDR, to: finalConfig.extraRoutesText)
        if model.completeSetup(config: finalConfig, password: password) {
            dismiss()
        } else {
            statusText = tr("setup.save_failed_inline")
        }
    }
}

private struct WizardSection<Content: View>: View {
    let title: String
    let systemImage: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label {
                Text(verbatim: title)
            } icon: {
                Image(systemName: systemImage)
            }
                .font(.headline)
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct WizardFieldRow: View {
    let title: String
    let placeholder: String
    let help: String?
    let error: String?
    @Binding var text: String

    init(
        _ title: String,
        text: Binding<String>,
        placeholder: String = "",
        help: String? = nil,
        error: String? = nil
    ) {
        self.title = title
        self.placeholder = placeholder
        self.help = help
        self.error = error
        self._text = text
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(verbatim: title)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth, alignment: .trailing)
            VStack(alignment: .leading, spacing: 4) {
                TextField(placeholder.isEmpty ? title : placeholder, text: $text)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: .infinity)
                    .accessibilityLabel(Text(verbatim: title))
                if let help, error == nil {
                    HelpText(help)
                }
                if let error {
                    InlineIssue(error)
                }
            }
        }
    }
}

private struct WizardSecureFieldRow: View {
    let title: String
    let help: String?
    let error: String?
    @Binding var text: String

    init(_ title: String, text: Binding<String>, help: String? = nil, error: String? = nil) {
        self.title = title
        self.help = help
        self.error = error
        self._text = text
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(verbatim: title)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth, alignment: .trailing)
            VStack(alignment: .leading, spacing: 4) {
                SecureField(title, text: $text)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: .infinity)
                    .accessibilityLabel(Text(verbatim: title))
                if let help, error == nil {
                    HelpText(help)
                }
                if let error {
                    InlineIssue(error)
                }
            }
        }
    }
}
