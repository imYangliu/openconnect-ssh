import SwiftUI

struct ContentView: View {
    @StateObject private var model = AppModel()
    @State private var selectedPane: AppPane = .connection

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
        .frame(minWidth: 960, minHeight: 640, alignment: .topLeading)
        .onChange(of: model.config) { _ in
            model.syncYAMLAfterSettingsChange()
        }
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Text("OCH")
                    .font(.title2)
                    .fontWeight(.semibold)
                Text(ConfigPaths.guiYAML.path)
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
                            Label(pane.title, systemImage: pane.systemImage)
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
                    Label("Save", systemImage: "square.and.arrow.down")
                        .frame(maxWidth: .infinity)
                }
                .keyboardShortcut("s", modifiers: .command)
                .buttonStyle(.borderedProminent)
                .disabled(model.isBusy)
            }
        }
        .padding(18)
        .frame(width: 230)
        .frame(maxHeight: .infinity, alignment: .topLeading)
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private var header: some View {
        HStack(spacing: 12) {
            Label(selectedPane.title, systemImage: selectedPane.systemImage)
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
        Label(model.includeInstalled ? "SSH Include installed" : "SSH Include missing",
              systemImage: model.includeInstalled ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
            .font(.caption)
            .foregroundStyle(model.includeInstalled ? .green : .orange)
            .lineLimit(1)
    }

    @ViewBuilder
    private var paneContent: some View {
        switch selectedPane {
        case .connection:
            ScrollView {
                connectionPane
            }
        case .ssh:
            ScrollView {
                sshPane
            }
        case .routes:
            ScrollView {
                routesPane
            }
        case .advanced:
            ScrollView {
                advancedPane
            }
        case .yaml:
            yamlPane
        case .logs:
            logsPane
        }
    }

    private var connectionPane: some View {
        SettingsStack {
            FormSection("VPN", systemImage: "lock.shield") {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                    FieldRow("Gateway", text: $model.config.vpnHost)
                    FieldRow("User", text: $model.config.vpnUser)
                    FieldRow("Auth group", text: $model.config.vpnAuthGroup)
                    SecureFieldRow("Password", text: $model.vpnPassword)
                }

                Toggle("Save VPN password in Keychain", isOn: $model.savePassword)
            }

            HStack(spacing: 10) {
                Button {
                    model.connect()
                } label: {
                    Label("Connect", systemImage: "bolt.horizontal.circle")
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.isBusy)

                Button {
                    model.disconnect()
                } label: {
                    Label("Disconnect", systemImage: "xmark.circle")
                }
                .disabled(model.isBusy)

                Button {
                    model.refreshStatus()
                } label: {
                    Label("Status", systemImage: "waveform.path.ecg")
                }
                .disabled(model.isBusy)
            }
        }
    }

    private var sshPane: some View {
        SettingsStack {
            FormSection("Managed SSH Host", systemImage: "terminal") {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                    FieldRow("Host", text: $model.config.defaultHost)
                    FieldRow("HostName", text: $model.config.targetHost)
                    FieldRow("User", text: $model.config.targetUser)
                    FieldRow("Port", text: $model.config.targetPort)
                }
            }

            HStack(spacing: 10) {
                Button {
                    model.installSSHInclude()
                } label: {
                    Label("Install Include", systemImage: "plus.square.on.square")
                }

                includeStatusLabel
            }
        }
    }

    private var routesPane: some View {
        SettingsStack {
            FormSection("Extra Routes", systemImage: "point.3.connected.trianglepath.dotted") {
                TextEditor(text: $model.config.extraRoutesText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 130)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))
            }

            FormSection("Proxy", systemImage: "arrow.left.arrow.right") {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                    FieldRow("Local host", text: $model.config.proxyLocalHost)
                    FieldRow("Local port", text: $model.config.proxyLocalPort)
                    FieldRow("Remote port", text: $model.config.proxyRemotePort)
                }
            }
        }
    }

    private var advancedPane: some View {
        SettingsStack {
            FormSection("Paths", systemImage: "folder") {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                    FieldRow("och", text: $model.config.ochPath)
                    FieldRow("och-vpn", text: $model.config.ochVpnPath)
                    FieldRow("askpass", text: $model.config.askpassPath)
                }
            }

            HStack(spacing: 10) {
                Button {
                    model.deleteSavedPassword()
                } label: {
                    Label("Remove Keychain Password", systemImage: "key.slash")
                }

                Button {
                    model.loadConfiguration()
                } label: {
                    Label("Reload", systemImage: "arrow.clockwise")
                }
            }
        }
    }

    private var yamlPane: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 10) {
                Button {
                    model.applyYAMLToSettings()
                } label: {
                    Label("Apply YAML", systemImage: "checkmark.square")
                }

                Button {
                    model.refreshYAMLFromSettings()
                } label: {
                    Label("Sync From Settings", systemImage: "arrow.triangle.2.circlepath")
                }

                Spacer()

                Text(ConfigPaths.guiYAML.path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            TextEditor(text: $model.yamlText)
                .font(.system(.body, design: .monospaced))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))
        }
        .padding(22)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var logsPane: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Log")
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
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))
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
    case yaml
    case logs

    var id: Self { self }

    var title: String {
        switch self {
        case .connection:
            return "Connection"
        case .ssh:
            return "SSH"
        case .routes:
            return "Routes & Proxy"
        case .advanced:
            return "Advanced"
        case .yaml:
            return "YAML"
        case .logs:
            return "Logs"
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
        case .yaml:
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
        .frame(maxWidth: 720, alignment: .topLeading)
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
            Label(title, systemImage: systemImage)
                .font(.headline)
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct FieldRow: View {
    let title: String
    @Binding var text: String

    init(_ title: String, text: Binding<String>) {
        self.title = title
        self._text = text
    }

    var body: some View {
        GridRow {
            Text(title)
                .foregroundStyle(.secondary)
                .frame(width: 108, alignment: .trailing)
            TextField(title, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 300)
        }
    }
}

private struct SecureFieldRow: View {
    let title: String
    @Binding var text: String

    init(_ title: String, text: Binding<String>) {
        self.title = title
        self._text = text
    }

    var body: some View {
        GridRow {
            Text(title)
                .foregroundStyle(.secondary)
                .frame(width: 108, alignment: .trailing)
            SecureField(title, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 300)
        }
    }
}
