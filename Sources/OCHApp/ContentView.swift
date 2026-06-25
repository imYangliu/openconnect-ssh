import SwiftUI

struct ContentView: View {
    @StateObject private var model = AppModel()
    @State private var selectedPane: AppPane = .connection
    @AppStorage(AppLanguage.storageKey) private var appLanguageRawValue = AppLanguage.system.rawValue

    private var appLanguage: AppLanguage {
        AppLanguage(rawValue: appLanguageRawValue) ?? .system
    }

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
        .environment(\.locale, appLanguage.locale)
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
                            Label(pane.titleKey, systemImage: pane.systemImage)
                            Spacer()
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 8)
                    .background(selectedPane == pane ? Color.accentColor.opacity(0.16) : Color.clear)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                }
            }

            Spacer()

            VStack(alignment: .leading, spacing: 10) {
                includeStatusLabel
                Button {
                    model.saveConfiguration()
                } label: {
                    Label("button.save", systemImage: "square.and.arrow.down")
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
            Label(selectedPane.titleKey, systemImage: selectedPane.systemImage)
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
        Label(includeStatusTitleKey,
              systemImage: model.includeInstalled ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
            .font(.caption)
            .foregroundStyle(model.includeInstalled ? .green : .orange)
            .lineLimit(1)
    }

    private var includeStatusTitleKey: LocalizedStringKey {
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
            FormSection("section.vpn", systemImage: "lock.shield") {
                FieldStack {
                    FieldRow("field.gateway", text: $model.config.vpnHost)
                    FieldRow("field.user", text: $model.config.vpnUser)
                    FieldRow("field.auth_group", text: $model.config.vpnAuthGroup)
                    SecureFieldRow("field.password", text: $model.vpnPassword)
                }

                Toggle("toggle.save_vpn_password", isOn: $model.savePassword)
            }

            HStack(spacing: 10) {
                Button {
                    model.connect()
                } label: {
                    Label("button.connect", systemImage: "bolt.horizontal.circle")
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.isBusy)

                Button {
                    model.disconnect()
                } label: {
                    Label("button.disconnect", systemImage: "xmark.circle")
                }
                .disabled(model.isBusy)

                Button {
                    model.refreshStatus()
                } label: {
                    Label("button.status", systemImage: "waveform.path.ecg")
                }
                .disabled(model.isBusy)
            }
        }
    }

    private var sshPane: some View {
        SettingsStack {
            FormSection("section.managed_ssh_host", systemImage: "terminal") {
                FieldStack {
                    FieldRow("field.host", text: $model.config.defaultHost)
                    FieldRow("field.hostname", text: $model.config.targetHost)
                    FieldRow("field.user", text: $model.config.targetUser)
                    FieldRow("field.port", text: $model.config.targetPort)
                }
            }

            HStack(spacing: 10) {
                Button {
                    model.installSSHInclude()
                } label: {
                    Label("button.install_include", systemImage: "plus.square.on.square")
                }

                includeStatusLabel
            }
        }
    }

    private var routesPane: some View {
        SettingsStack {
            FormSection("section.extra_routes", systemImage: "point.3.connected.trianglepath.dotted") {
                TextEditor(text: $model.config.extraRoutesText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 130)
                    .overlay {
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(.separator)
                            .allowsHitTesting(false)
                    }
            }

            FormSection("section.proxy", systemImage: "arrow.left.arrow.right") {
                FieldStack {
                    FieldRow("field.local_host", text: $model.config.proxyLocalHost)
                    FieldRow("field.local_port", text: $model.config.proxyLocalPort)
                    FieldRow("field.remote_port", text: $model.config.proxyRemotePort)
                }
            }
        }
    }

    private var advancedPane: some View {
        SettingsStack {
            FormSection("section.language", systemImage: "globe") {
                Picker("field.language", selection: $appLanguageRawValue) {
                    ForEach(AppLanguage.allCases) { language in
                        Text(language.titleKey).tag(language.rawValue)
                    }
                }
                .pickerStyle(.segmented)
            }

            FormSection("section.paths", systemImage: "folder") {
                FieldStack {
                    FieldRow("och", text: $model.config.ochPath)
                    FieldRow("och-vpn", text: $model.config.ochVpnPath)
                    FieldRow("askpass", text: $model.config.askpassPath)
                }
            }

            HStack(spacing: 10) {
                Button {
                    model.deleteSavedPassword()
                } label: {
                    Label("button.remove_keychain_password", systemImage: "key.slash")
                }

                Button {
                    model.loadConfiguration()
                } label: {
                    Label("button.reload", systemImage: "arrow.clockwise")
                }
            }
        }
    }

    private var configPane: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 10) {
                Button {
                    model.applyConfigTextToSettings()
                } label: {
                    Label("button.apply_toml", systemImage: "checkmark.square")
                }

                Button {
                    model.refreshConfigTextFromSettings()
                } label: {
                    Label("button.sync_from_settings", systemImage: "arrow.triangle.2.circlepath")
                }

                Spacer()

                Text(verbatim: ConfigPaths.configTOML.path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            TextEditor(text: $model.configText)
                .font(.system(.body, design: .monospaced))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
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
                Text("label.log")
                    .font(.headline)
                Spacer()
                if model.isBusy {
                    ProgressView()
                        .controlSize(.small)
                }
            }

            TextEditor(text: $model.logText)
                .font(.system(.body, design: .monospaced))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .overlay {
                    RoundedRectangle(cornerRadius: 6)
                        .stroke(.separator)
                        .allowsHitTesting(false)
                }
        }
        .padding(22)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
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

    var titleKey: LocalizedStringKey {
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
    let titleKey: LocalizedStringKey
    let systemImage: String
    @ViewBuilder let content: Content

    init(_ titleKey: LocalizedStringKey, systemImage: String, @ViewBuilder content: () -> Content) {
        self.titleKey = titleKey
        self.systemImage = systemImage
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(titleKey, systemImage: systemImage)
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
    let titleKey: LocalizedStringKey
    @Binding var text: String

    init(_ titleKey: LocalizedStringKey, text: Binding<String>) {
        self.titleKey = titleKey
        self._text = text
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(titleKey)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth, alignment: .trailing)
            TextField(titleKey, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: UILayout.textFieldMinWidth, maxWidth: .infinity)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct SecureFieldRow: View {
    let titleKey: LocalizedStringKey
    @Binding var text: String

    init(_ titleKey: LocalizedStringKey, text: Binding<String>) {
        self.titleKey = titleKey
        self._text = text
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Text(titleKey)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth, alignment: .trailing)
            SecureField(titleKey, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: UILayout.textFieldMinWidth, maxWidth: .infinity)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
