import SwiftUI

struct ContentView: View {
    @StateObject private var model = AppModel()
    @State private var selectedPane: AppPane = .connection
    @State private var confirmingDeleteSavedPassword = false
    @State private var confirmingReloadConfiguration = false
    @State private var confirmingSyncTOML = false

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            sidebar

            Divider()

            VStack(spacing: 0) {
                header
                Divider()
                paneContent
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .frame(
            minWidth: UILayout.windowMinWidth,
            minHeight: UILayout.windowMinHeight,
            alignment: .topLeading
        )
        .onChange(of: model.config) { _ in
            model.syncConfigTextAfterSettingsChange()
        }
        .sheet(isPresented: $model.showingSetupWizard) {
            SetupWizardView(model: model)
        }
        .alert(tr("confirm.delete_password.title"), isPresented: $confirmingDeleteSavedPassword) {
            Button(tr("button.cancel"), role: .cancel) {}
            Button(tr("button.remove_keychain_password"), role: .destructive) {
                model.deleteSavedPassword()
            }
        } message: {
            Text(verbatim: tr("confirm.delete_password.message"))
        }
        .alert(tr("confirm.reload.title"), isPresented: $confirmingReloadConfiguration) {
            Button(tr("button.cancel"), role: .cancel) {}
            Button(tr("button.reload"), role: .destructive) {
                model.loadConfiguration(reportToConfigPane: true)
            }
        } message: {
            Text(verbatim: tr("confirm.reload.message"))
        }
        .alert(tr("confirm.sync_toml.title"), isPresented: $confirmingSyncTOML) {
            Button(tr("button.cancel"), role: .cancel) {}
            Button(tr("button.sync_from_settings"), role: .destructive) {
                model.refreshConfigTextFromSettings()
            }
        } message: {
            Text(verbatim: tr("confirm.sync_toml.message"))
        }
        .environment(\.locale, model.config.appLanguage.locale)
    }

    private func tr(_ key: String) -> String {
        L10n.tr(key, language: model.config.appLanguage)
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("OCH")
                    .font(.title2)
                    .fontWeight(.semibold)
                Text(verbatim: ConfigPaths.configTOML.path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            VStack(alignment: .leading, spacing: 6) {
                ForEach(AppPane.allCases) { pane in
                    Button {
                        selectedPane = pane
                    } label: {
                        HStack {
                            Label {
                                Text(verbatim: tr(pane.titleKey))
                            } icon: {
                                Image(systemName: pane.systemImage)
                            }
                            Spacer()
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 8)
                    .background(selectedPane == pane ? Color.accentColor.opacity(0.16) : Color.clear)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                    .accessibilityLabel(Text(verbatim: tr(pane.titleKey)))
                    .accessibilityValue(Text(verbatim: selectedPane == pane ? tr("accessibility.selected") : ""))
                }
            }

            Spacer()

            VStack(alignment: .leading, spacing: 10) {
                includeStatusPanel
                Button {
                    model.showingSetupWizard = true
                } label: {
                    Label {
                        Text(verbatim: tr("button.setup_wizard"))
                    } icon: {
                        Image(systemName: "wand.and.stars")
                    }
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .disabled(model.isBusy)

                Button {
                    model.saveConfiguration()
                } label: {
                    Label {
                        Text(verbatim: tr("button.save"))
                    } icon: {
                        Image(systemName: "square.and.arrow.down")
                    }
                        .frame(maxWidth: .infinity)
                }
                .keyboardShortcut("s", modifiers: .command)
                .buttonStyle(.borderedProminent)
                .disabled(model.isBusy)
            }
        }
        .padding(18)
        .frame(width: UILayout.sidebarWidth)
        .frame(maxHeight: .infinity, alignment: .topLeading)
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private var header: some View {
        HStack(spacing: 12) {
            Label {
                Text(verbatim: tr(selectedPane.titleKey))
            } icon: {
                Image(systemName: selectedPane.systemImage)
            }
                .font(.headline)

            Spacer()

            if model.isBusy {
                ProgressView()
                    .controlSize(.small)
            }

            includeStatusLabel
        }
        .padding(.horizontal, 22)
        .padding(.vertical, 14)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var includeStatusLabel: some View {
        Label {
            Text(verbatim: tr(includeStatusTitleKey))
        } icon: {
            Image(systemName: model.includeInstalled ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
        }
            .font(.caption)
            .foregroundStyle(model.includeInstalled ? Color.green : Color.orange)
            .lineLimit(1)
    }

    private var includeStatusPanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            includeStatusLabel

            if !model.includeInstalled {
                Text(verbatim: tr("status.ssh_include.missing_help"))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                Button {
                    model.installSSHInclude()
                } label: {
                    Label {
                        Text(verbatim: tr("button.install_include"))
                    } icon: {
                        Image(systemName: "plus.square.on.square")
                    }
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .disabled(model.isBusy)
            }
        }
        .padding(10)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    private var includeStatusTitleKey: String {
        model.includeInstalled ? "status.ssh_include.installed" : "status.ssh_include.missing"
    }

    @ViewBuilder
    private var paneContent: some View {
        switch selectedPane {
        case .connection:
            scrollingPane {
                connectionPane
            }
        case .ssh:
            scrollingPane {
                sshPane
            }
        case .routes:
            scrollingPane {
                routesPane
            }
        case .advanced:
            scrollingPane {
                advancedPane
            }
        case .config:
            configPane
        case .logs:
            logsPane
        }
    }

    private func scrollingPane<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        ScrollView {
            content()
                .frame(maxWidth: .infinity, alignment: .topLeading)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var connectionPane: some View {
        SettingsStack {
            FormSection(tr("section.vpn"), systemImage: "lock.shield") {
                FieldStack {
                    FieldRow(
                        tr("field.gateway"),
                        text: $model.config.vpnHost,
                        placeholder: tr("placeholder.gateway"),
                        help: tr("help.gateway"),
                        error: requiredError(model.config.vpnHost, key: "validation.vpn_gateway_required")
                    )
                    FieldRow(
                        tr("field.user"),
                        text: $model.config.vpnUser,
                        placeholder: tr("placeholder.vpn_user"),
                        error: requiredError(model.config.vpnUser, key: "validation.vpn_user_required")
                    )
                    FieldRow(
                        tr("field.auth_group"),
                        text: $model.config.vpnAuthGroup,
                        placeholder: tr("placeholder.auth_group"),
                        help: tr("help.auth_group")
                    )
                    SecureFieldRow(
                        tr("field.password"),
                        text: $model.vpnPassword,
                        help: tr("help.password"),
                        error: requiredError(model.vpnPassword, key: "validation.vpn_password_required")
                    )
                }

                Toggle(isOn: $model.savePassword) {
                    Text(verbatim: tr("toggle.save_vpn_password"))
                }
            }

            if !model.connectionStatusText.isEmpty {
                NoticeView(message: model.connectionStatusText, isError: model.connectionStatusIsError)
            }

            HStack(spacing: 10) {
                Button {
                    model.connect()
                } label: {
                    Label {
                        Text(verbatim: tr("button.connect"))
                    } icon: {
                        Image(systemName: "bolt.horizontal.circle")
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.isBusy)

                Button {
                    model.disconnect()
                } label: {
                    Label {
                        Text(verbatim: tr("button.disconnect"))
                    } icon: {
                        Image(systemName: "xmark.circle")
                    }
                }
                .disabled(model.isBusy)

                Button {
                    model.refreshStatus()
                } label: {
                    Label {
                        Text(verbatim: tr("button.status"))
                    } icon: {
                        Image(systemName: "waveform.path.ecg")
                    }
                }
                .disabled(model.isBusy)
            }
        }
    }

    private var sshPane: some View {
        SettingsStack {
            FormSection(tr("section.managed_ssh_host"), systemImage: "terminal") {
                FieldStack {
                    FieldRow(
                        tr("field.host"),
                        text: $model.config.defaultHost,
                        placeholder: tr("placeholder.host"),
                        help: tr("help.host"),
                        error: requiredError(model.config.defaultHost, key: "validation.ssh_host_required")
                    )
                    FieldRow(
                        tr("field.hostname"),
                        text: $model.config.targetHost,
                        placeholder: tr("placeholder.hostname"),
                        help: tr("help.hostname"),
                        error: requiredError(model.config.targetHost, key: "validation.hostname_required")
                    )
                    FieldRow(
                        tr("field.user"),
                        text: $model.config.targetUser,
                        placeholder: tr("placeholder.ssh_user"),
                        error: requiredError(model.config.targetUser, key: "validation.ssh_user_required")
                    )
                    FieldRow(
                        tr("field.port"),
                        text: $model.config.targetPort,
                        placeholder: tr("placeholder.port"),
                        error: portError(model.config.targetPort, requiredKey: "validation.port_required")
                    )
                }
            }

            includeStatusPanel

            if !model.sshStatusText.isEmpty {
                NoticeView(message: model.sshStatusText, isError: model.sshStatusIsError)
            }
        }
    }

    private var routesPane: some View {
        SettingsStack {
            FormSection(tr("section.extra_routes"), systemImage: "point.3.connected.trianglepath.dotted") {
                TextEditor(text: $model.config.extraRoutesText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 130)
                    .accessibilityLabel(Text(verbatim: tr("section.extra_routes")))
                    .overlay {
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(.separator)
                            .allowsHitTesting(false)
                    }
                HelpText(tr("help.extra_routes"))
                if let error = extraRoutesError {
                    InlineIssue(error)
                }
            }

            FormSection(tr("section.proxy"), systemImage: "arrow.left.arrow.right") {
                FieldStack {
                    FieldRow(
                        tr("field.local_host"),
                        text: $model.config.proxyLocalHost,
                        placeholder: tr("placeholder.local_host"),
                        help: tr("help.local_host"),
                        error: requiredError(model.config.proxyLocalHost, key: "validation.local_host_required")
                    )
                    FieldRow(
                        tr("field.local_port"),
                        text: $model.config.proxyLocalPort,
                        placeholder: tr("placeholder.proxy_port"),
                        error: portError(model.config.proxyLocalPort, requiredKey: "validation.local_port_required")
                    )
                    FieldRow(
                        tr("field.remote_port"),
                        text: $model.config.proxyRemotePort,
                        placeholder: tr("placeholder.proxy_port"),
                        error: portError(model.config.proxyRemotePort, requiredKey: "validation.remote_port_required")
                    )
                }
            }
        }
    }

    private var advancedPane: some View {
        SettingsStack {
            FormSection(tr("section.language"), systemImage: "globe") {
                Picker(tr("field.language"), selection: $model.config.appLanguage) {
                    ForEach(AppLanguage.allCases) { language in
                        Text(verbatim: tr(language.titleKey)).tag(language)
                    }
                }
                .pickerStyle(.segmented)
            }

            HStack(spacing: 10) {
                Button {
                    confirmingDeleteSavedPassword = true
                } label: {
                    Label {
                        Text(verbatim: tr("button.remove_keychain_password"))
                    } icon: {
                        Image(systemName: "key.slash")
                    }
                }

                Button {
                    confirmingReloadConfiguration = true
                } label: {
                    Label {
                        Text(verbatim: tr("button.reload"))
                    } icon: {
                        Image(systemName: "arrow.clockwise")
                    }
                }
            }

            if !model.advancedStatusText.isEmpty {
                NoticeView(message: model.advancedStatusText, isError: model.advancedStatusIsError)
            }
        }
    }

    private var configPane: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 10) {
                Button {
                    model.applyConfigTextToSettings()
                } label: {
                    Label {
                        Text(verbatim: tr("button.apply_toml"))
                    } icon: {
                        Image(systemName: "checkmark.square")
                    }
                }

                Button {
                    if model.hasUnsavedConfigTextChanges {
                        confirmingSyncTOML = true
                    } else {
                        model.refreshConfigTextFromSettings()
                    }
                } label: {
                    Label {
                        Text(verbatim: tr("button.sync_from_settings"))
                    } icon: {
                        Image(systemName: "arrow.triangle.2.circlepath")
                    }
                }

                Spacer()

                Text(verbatim: ConfigPaths.configTOML.path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            if model.hasUnsavedConfigTextChanges {
                NoticeView(message: tr("status.toml.unsaved_changes"), isWarning: true)
            }

            if !model.configStatusText.isEmpty {
                NoticeView(message: model.configStatusText, isError: model.configStatusIsError)
            }

            TextEditor(text: $model.configText)
                .font(.system(.body, design: .monospaced))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityLabel(Text(verbatim: tr("pane.config")))
                .overlay {
                    RoundedRectangle(cornerRadius: 6)
                        .stroke(.separator)
                        .allowsHitTesting(false)
                }
        }
        .padding(22)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var logsPane: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(verbatim: tr("label.log"))
                    .font(.headline)
                Spacer()
                if model.isBusy {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            ReadOnlyTextBox(text: model.logText, accessibilityLabel: tr("label.log"))
        }
        .padding(22)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var extraRoutesError: String? {
        for route in model.config.extraRoutes where !SetupCIDRHelper.isValidCIDR(route) {
            return L10n.tr("validation.route_cidr_invalid", language: model.config.appLanguage, route)
        }
        return nil
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
}

private enum AppPane: CaseIterable, Identifiable {
    case connection
    case ssh
    case routes
    case advanced
    case config
    case logs

    var id: Self { self }

    var titleKey: String {
        switch self {
        case .connection:
            return "pane.connection"
        case .ssh:
            return "pane.ssh"
        case .routes:
            return "pane.routes"
        case .advanced:
            return "pane.advanced"
        case .config:
            return "pane.config"
        case .logs:
            return "pane.logs"
        }
    }

    var systemImage: String {
        switch self {
        case .connection:
            return "network"
        case .ssh:
            return "terminal"
        case .routes:
            return "point.3.connected.trianglepath.dotted"
        case .advanced:
            return "slider.horizontal.3"
        case .config:
            return "doc.plaintext"
        case .logs:
            return "list.bullet.rectangle"
        }
    }
}

private struct SettingsStack<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            content
        }
        .padding(24)
        .frame(maxWidth: UILayout.settingsMaxWidth, alignment: .topLeading)
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }
}

private struct FormSection<Content: View>: View {
    let title: String
    let systemImage: String
    @ViewBuilder let content: Content

    init(_ title: String, systemImage: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.systemImage = systemImage
        self.content = content()
    }

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

private struct FieldStack<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct FieldRow: View {
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
                    .frame(minWidth: UILayout.textFieldMinWidth, maxWidth: .infinity)
                    .accessibilityLabel(Text(verbatim: title))
                if let help, error == nil {
                    HelpText(help)
                }
                if let error {
                    InlineIssue(error)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct SecureFieldRow: View {
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
                    .frame(minWidth: UILayout.textFieldMinWidth, maxWidth: .infinity)
                    .accessibilityLabel(Text(verbatim: title))
                if let help, error == nil {
                    HelpText(help)
                }
                if let error {
                    InlineIssue(error)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct HelpText: View {
    let text: String

    init(_ text: String) {
        self.text = text
    }

    var body: some View {
        Text(verbatim: text)
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }
}

struct InlineIssue: View {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var body: some View {
        Label {
            Text(verbatim: message)
        } icon: {
            Image(systemName: "exclamationmark.triangle.fill")
        }
        .font(.caption)
        .foregroundStyle(Color.orange)
        .fixedSize(horizontal: false, vertical: true)
    }
}

struct NoticeView: View {
    let message: String
    let isError: Bool
    let isWarning: Bool

    init(message: String, isError: Bool = false, isWarning: Bool = false) {
        self.message = message
        self.isError = isError
        self.isWarning = isWarning
    }

    var body: some View {
        Label {
            Text(verbatim: message)
                .fixedSize(horizontal: false, vertical: true)
        } icon: {
            Image(systemName: isError ? "xmark.octagon.fill" : (isWarning ? "exclamationmark.triangle.fill" : "checkmark.circle.fill"))
        }
        .font(.caption)
        .foregroundStyle(foregroundColor)
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(foregroundColor.opacity(0.10))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    private var foregroundColor: Color {
        if isError {
            return .red
        }
        if isWarning {
            return .orange
        }
        return .green
    }
}

struct ReadOnlyTextBox: View {
    let text: String
    let accessibilityLabel: String

    var body: some View {
        ScrollView {
            Text(verbatim: text.isEmpty ? " " : text)
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .topLeading)
                .padding(8)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityLabel(Text(verbatim: accessibilityLabel))
        .overlay {
            RoundedRectangle(cornerRadius: 6)
                .stroke(.separator)
                .allowsHitTesting(false)
        }
    }
}
