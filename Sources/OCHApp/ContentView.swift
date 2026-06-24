import SwiftUI

struct ContentView: View {
    @StateObject private var model = AppModel()

    var body: some View {
        HStack(spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    SectionHeader("VPN")
                    Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                        FieldRow("Gateway", text: $model.config.vpnHost)
                        FieldRow("User", text: $model.config.vpnUser)
                        FieldRow("Auth group", text: $model.config.vpnAuthGroup)
                        SecureFieldRow("Password", text: $model.vpnPassword)
                    }
                    Toggle("Save VPN password in Keychain", isOn: $model.savePassword)

                    SectionHeader("SSH")
                    Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                        FieldRow("Managed Host", text: $model.config.defaultHost)
                        FieldRow("HostName", text: $model.config.targetHost)
                        FieldRow("User", text: $model.config.targetUser)
                        FieldRow("Port", text: $model.config.targetPort)
                    }

                    SectionHeader("Routes")
                    TextEditor(text: $model.config.extraRoutesText)
                        .font(.system(.body, design: .monospaced))
                        .frame(minHeight: 88)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))

                    SectionHeader("Paths")
                    Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                        FieldRow("och", text: $model.config.ochPath)
                        FieldRow("och-vpn", text: $model.config.ochVpnPath)
                        FieldRow("askpass", text: $model.config.askpassPath)
                    }
                }
                .padding(18)
            }
            .frame(minWidth: 430)

            Divider()

            VStack(alignment: .leading, spacing: 14) {
                Text("OCH")
                    .font(.title2)
                    .fontWeight(.semibold)

                HStack {
                    Button("Save") {
                        model.saveConfiguration()
                    }
                    Button("Connect") {
                        model.connect()
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(model.isBusy)
                    Button("Disconnect") {
                        model.disconnect()
                    }
                    .disabled(model.isBusy)
                    Button("Status") {
                        model.refreshStatus()
                    }
                    .disabled(model.isBusy)
                }

                HStack {
                    Button("Install SSH Include") {
                        model.installSSHInclude()
                    }
                    Button("Remove Keychain Password") {
                        model.deleteSavedPassword()
                    }
                    Spacer()
                }

                HStack {
                    Label(model.includeInstalled ? "SSH Include installed" : "SSH Include missing",
                          systemImage: model.includeInstalled ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                        .foregroundStyle(model.includeInstalled ? .green : .orange)
                    Spacer()
                    if model.isBusy {
                        ProgressView()
                            .controlSize(.small)
                    }
                }

                Text("Log")
                    .font(.headline)
                TextEditor(text: $model.logText)
                    .font(.system(.body, design: .monospaced))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))
            }
            .padding(18)
            .frame(minWidth: 390)
        }
    }
}

private struct SectionHeader: View {
    let title: String

    init(_ title: String) {
        self.title = title
    }

    var body: some View {
        Text(title)
            .font(.headline)
            .padding(.top, 2)
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
                .frame(width: 96, alignment: .trailing)
            TextField(title, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 240)
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
                .frame(width: 96, alignment: .trailing)
            SecureField(title, text: $text)
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 240)
        }
    }
}
