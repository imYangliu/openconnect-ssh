import SwiftUI

struct ContentView: View {
    @ObservedObject var model: AppModel
    @State private var selectedPane: AppPane = .overview
    @State private var lastAutoVPNRefresh = Date.distantPast
    @State private var lastAutoServiceRefresh = Date.distantPast
    @State private var lastAutoRuntimeLogRefresh = Date.distantPast
    @State private var confirmingDeleteSavedPassword = false
    @State private var confirmingReloadConfiguration = false
    @State private var confirmingSyncTOML = false
    @State private var confirmingInstallService = false
    @State private var confirmingUninstallService = false

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
        .onChange(of: model.config) {
            model.syncConfigTextAfterSettingsChange()
        }
        .onAppear {
            model.refreshServiceStatus(silent: true)
            model.refreshRuntimeLogTail()
        }
        .onReceive(Self.autoRefreshTimer) { now in
            autoRefresh(now: now)
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
        .alert(tr("confirm.service_install.title"), isPresented: $confirmingInstallService) {
            Button(tr("button.cancel"), role: .cancel) {}
            Button(tr("button.install_service")) {
                model.installService()
            }
        } message: {
            Text(verbatim: tr("confirm.service_install.message"))
        }
        .alert(tr("confirm.service_uninstall.title"), isPresented: $confirmingUninstallService) {
            Button(tr("button.cancel"), role: .cancel) {}
            Button(tr("button.uninstall_service"), role: .destructive) {
                model.uninstallService()
            }
        } message: {
            Text(verbatim: tr("confirm.service_uninstall.message"))
        }
        .environment(\.locale, model.config.appLanguage.locale)
    }

    private func tr(_ key: String) -> String {
        L10n.tr(key, language: model.config.appLanguage)
    }

    private static let autoRefreshTimer = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    private func autoRefresh(now: Date) {
        guard !model.isBusy else {
            return
        }
        if selectedPane.refreshesRuntimeLog,
           now.timeIntervalSince(lastAutoRuntimeLogRefresh) >= selectedPane.runtimeLogRefreshInterval {
            model.refreshRuntimeLogTail()
            lastAutoRuntimeLogRefresh = now
        }
        if selectedPane.refreshesVPNStatus,
           now.timeIntervalSince(lastAutoVPNRefresh) >= 5 {
            model.refreshStatus()
            lastAutoVPNRefresh = now
        }
        if selectedPane.refreshesServiceStatus,
           now.timeIntervalSince(lastAutoServiceRefresh) >= 15 {
            model.refreshServiceStatus(silent: true)
            lastAutoServiceRefresh = now
        }
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
                sidebarStatusPanel
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

            serviceHeaderLabel
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

    private var serviceHeaderLabel: some View {
        Label {
            Text(verbatim: model.serviceStatus.isAvailable ? tr("status.service.available_short") : tr("status.service.needs_attention_short"))
        } icon: {
            Image(systemName: model.serviceStatus.isAvailable ? "checkmark.shield.fill" : "shield.slash")
        }
        .font(.caption)
        .foregroundStyle(model.serviceStatus.isAvailable ? Color.green : Color.orange)
        .lineLimit(1)
    }

    private var sidebarStatusPanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label {
                Text(verbatim: model.connectionSummaryText)
            } icon: {
                Image(systemName: model.traySystemImage)
            }
            .font(.caption)
            .foregroundStyle(connectionColor)

            serviceHeaderLabel
            includeStatusLabel
        }
        .padding(10)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    @ViewBuilder
    private var paneContent: some View {
        switch selectedPane {
        case .overview:
            scrollingPane {
                overviewPane
            }
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
        case .service:
            scrollingPane {
                servicePane
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

    private var overviewPane: some View {
        SettingsStack {
            VStack(alignment: .leading, spacing: 8) {
                Text(verbatim: tr("overview.title"))
                    .font(.title2)
                    .fontWeight(.semibold)
                Text(verbatim: tr("overview.subtitle"))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            LazyVGrid(columns: [
                GridItem(.flexible(), spacing: 12),
                GridItem(.flexible(), spacing: 12)
            ], alignment: .leading, spacing: 12) {
                StatusCard(
                    title: tr("card.connection.title"),
                    value: model.connectionSummaryText,
                    detail: connectionDetailText,
                    systemImage: model.traySystemImage,
                    color: connectionColor
                )
                StatusCard(
                    title: tr("card.service.title"),
                    value: model.serviceSummaryText,
                    detail: model.serviceFallbackText,
                    systemImage: model.serviceStatus.isAvailable ? "checkmark.shield.fill" : "shield.slash",
                    color: model.serviceStatus.isAvailable ? .green : .orange
                )
                StatusCard(
                    title: tr("card.ssh_include.title"),
                    value: tr(includeStatusTitleKey),
                    detail: model.includeInstalled ? tr("status.ssh_include.ready_help") : tr("status.ssh_include.missing_help"),
                    systemImage: model.includeInstalled ? "checkmark.circle.fill" : "exclamationmark.triangle.fill",
                    color: model.includeInstalled ? .green : .orange
                )
                StatusCard(
                    title: tr("card.config.title"),
                    value: model.hasUnsavedConfigTextChanges ? tr("status.config.unsaved") : tr("status.config.saved_or_synced"),
                    detail: ConfigPaths.configTOML.path,
                    systemImage: model.hasUnsavedConfigTextChanges ? "doc.badge.clock" : "doc.text",
                    color: model.hasUnsavedConfigTextChanges ? .orange : .secondary
                )
            }

            HStack(spacing: 10) {
                Button {
                    model.connect()
                } label: {
                    Label(tr("button.connect"), systemImage: "bolt.horizontal.circle")
                }
                .keyboardShortcut(.defaultAction)
                .buttonStyle(.borderedProminent)
                .disabled(model.isConnectionBusy)

                Button {
                    model.disconnect()
                } label: {
                    Label(tr("button.disconnect"), systemImage: "xmark.circle")
                }
                .disabled(model.isConnectionBusy)

                Button {
                    model.refreshStatus()
                    model.refreshServiceStatus()
                } label: {
                    Label(tr("button.refresh_all"), systemImage: "arrow.clockwise")
                }
                .disabled(model.isBusy)
            }

            if !model.connectionStatusText.isEmpty {
                NoticeView(message: model.connectionStatusText, isError: model.connectionStatusIsError)
            }

            FormSection(tr("section.service_mode"), systemImage: "checkmark.shield") {
                Text(verbatim: model.serviceFallbackText)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)

                HStack(spacing: 10) {
                    Button {
                        model.refreshServiceStatus()
                    } label: {
                        Label(tr("button.refresh_service"), systemImage: "arrow.clockwise")
                    }
                    .disabled(model.isServiceBusy)

                    Button {
                        confirmingInstallService = true
                    } label: {
                        Label(tr("button.install_service"), systemImage: "square.and.arrow.down")
                    }
                    .disabled(model.isServiceBusy || model.serviceStatus.installed == true)

                    Button(role: .destructive) {
                        confirmingUninstallService = true
                    } label: {
                        Label(tr("button.uninstall_service"), systemImage: "trash")
                    }
                    .disabled(model.isServiceBusy || model.serviceStatus.installed != true)
                }

                if !model.serviceStatusText.isEmpty {
                    NoticeView(message: model.serviceStatusText, isError: model.serviceStatusIsError)
                }
            }
        }
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
                .disabled(model.isConnectionBusy)

                Button {
                    model.disconnect()
                } label: {
                    Label {
                        Text(verbatim: tr("button.disconnect"))
                    } icon: {
                        Image(systemName: "xmark.circle")
                    }
                }
                .disabled(model.isConnectionBusy)

                Button {
                    model.refreshStatus()
                } label: {
                    Label {
                        Text(verbatim: tr("button.status"))
                    } icon: {
                        Image(systemName: "waveform.path.ecg")
                    }
                }
                .disabled(model.isConnectionBusy)
            }
        }
    }

    private var servicePane: some View {
        SettingsStack {
            FormSection(tr("section.service_mode"), systemImage: "checkmark.shield") {
                VStack(alignment: .leading, spacing: 10) {
                    ServiceStatusRow(title: tr("field.service_installed"), value: model.serviceStatus.installed, yes: tr("value.yes"), no: tr("value.no"), unknown: tr("value.unknown"))
                    ServiceStatusRow(title: tr("field.service_running"), value: model.serviceStatus.running, yes: tr("value.yes"), no: tr("value.no"), unknown: tr("value.unknown"))
                    if !model.serviceStatus.registrationStatus.isEmpty {
                        KeyValueRow(title: tr("field.registration_status"), value: model.serviceStatus.registrationStatus)
                    }
                    ServiceStatusRow(title: tr("field.xpc_reachable"), value: model.serviceStatus.xpcReachable, yes: tr("value.yes"), no: tr("value.no"), unknown: tr("value.unknown"))
                    ServiceStatusRow(title: tr("field.socket_exists"), value: model.serviceStatus.socketExists, yes: tr("value.yes"), no: tr("value.no"), unknown: tr("value.unknown"))
                    ServiceStatusRow(title: tr("field.socket_reachable"), value: model.serviceStatus.socketReachable, yes: tr("value.yes"), no: tr("value.no"), unknown: tr("value.unknown"))
                    if !model.serviceStatus.socketPath.isEmpty {
                        KeyValueRow(title: tr("field.socket_path"), value: model.serviceStatus.socketPath)
                    }
                }

                HelpText(model.serviceFallbackText)

                HStack(spacing: 10) {
                    Button {
                        model.refreshServiceStatus()
                    } label: {
                        Label(tr("button.refresh_service"), systemImage: "arrow.clockwise")
                    }
                    .disabled(model.isServiceBusy)

                    Button {
                        confirmingInstallService = true
                    } label: {
                        Label(tr("button.install_service"), systemImage: "square.and.arrow.down")
                    }
                    .disabled(model.isServiceBusy || model.serviceStatus.installed == true)

                    Button(role: .destructive) {
                        confirmingUninstallService = true
                    } label: {
                        Label(tr("button.uninstall_service"), systemImage: "trash")
                    }
                    .disabled(model.isServiceBusy || model.serviceStatus.installed != true)

                    if model.serviceStatus.registrationStatus == "requiresApproval" {
                        Button {
                            model.openServiceSettings()
                        } label: {
                            Label(tr("button.open_system_settings"), systemImage: "gearshape")
                        }
                        .disabled(model.isServiceBusy)
                    }
                }
            }

            if !model.serviceStatusText.isEmpty {
                NoticeView(message: model.serviceStatusText, isError: model.serviceStatusIsError)
            }

            FormSection(tr("section.service_raw_status"), systemImage: "list.bullet.rectangle") {
                ReadOnlyTextBox(
                    text: model.serviceStatus.rawOutput.isEmpty ? tr("status.service.no_status_yet") : model.serviceStatus.rawOutput,
                    accessibilityLabel: tr("section.service_raw_status")
                )
                .frame(minHeight: 180)
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
                FieldStack {
                    HStack(alignment: .firstTextBaseline, spacing: 12) {
                        Text(verbatim: tr("field.route_mode"))
                            .foregroundStyle(.secondary)
                            .frame(width: UILayout.labelWidth, alignment: .trailing)
                        VStack(alignment: .leading, spacing: 4) {
                            Picker(tr("field.route_mode"), selection: $model.config.routeMode) {
                                ForEach(AppRouteMode.allCases) { mode in
                                    Text(verbatim: tr(mode.titleKey)).tag(mode)
                                }
                            }
                            .pickerStyle(.segmented)
                            .frame(maxWidth: 420)
                            HelpText(tr("help.route_mode"))
                        }
                    }
                }
                TextEditor(text: $model.config.extraRoutesText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 130)
                    .disabled(model.config.routeMode == .openconnect)
                    .accessibilityLabel(Text(verbatim: tr("section.extra_routes")))
                    .overlay {
                        RoundedRectangle(cornerRadius: 6)
                            .stroke(.separator)
                            .allowsHitTesting(false)
                    }
                HelpText(tr("help.extra_routes"))
                if model.config.routeMode == .openconnect {
                    HelpText(tr("help.extra_routes_inactive"))
                }
                if let error = extraRoutesError {
                    InlineIssue(error)
                }
            }

            FormSection(tr("section.dns"), systemImage: "network") {
                FieldStack {
                    HStack(alignment: .firstTextBaseline, spacing: 12) {
                        Text(verbatim: tr("field.dns_mode"))
                            .foregroundStyle(.secondary)
                            .frame(width: UILayout.labelWidth, alignment: .trailing)
                        VStack(alignment: .leading, spacing: 4) {
                            Picker(tr("field.dns_mode"), selection: $model.config.dnsMode) {
                                ForEach(AppDNSMode.allCases) { mode in
                                    Text(verbatim: tr(mode.titleKey)).tag(mode)
                                }
                            }
                            .pickerStyle(.segmented)
                            .frame(maxWidth: 420)
                            HelpText(tr("help.dns_mode"))
                        }
                    }
                }
            }

            FormSection(tr("section.proxy"), systemImage: "arrow.left.arrow.right") {
                Toggle(isOn: $model.config.proxyEnabled) {
                    Text(verbatim: tr("toggle.enable_proxy"))
                }
                HelpText(tr("help.proxy_optional"))
                FieldStack {
                    FieldRow(
                        tr("field.local_host"),
                        text: $model.config.proxyLocalHost,
                        placeholder: tr("placeholder.local_host"),
                        help: tr("help.local_host"),
                        error: model.config.proxyEnabled
                            ? requiredError(model.config.proxyLocalHost, key: "validation.local_host_required")
                            : nil
                    )
                    FieldRow(
                        tr("field.local_port"),
                        text: $model.config.proxyLocalPort,
                        placeholder: tr("placeholder.proxy_port"),
                        error: model.config.proxyEnabled
                            ? portError(model.config.proxyLocalPort, requiredKey: "validation.local_port_required")
                            : nil
                    )
                    FieldRow(
                        tr("field.remote_port"),
                        text: $model.config.proxyRemotePort,
                        placeholder: tr("placeholder.proxy_port"),
                        error: model.config.proxyEnabled
                            ? portError(model.config.proxyRemotePort, requiredKey: "validation.remote_port_required")
                            : nil
                    )
                }
                .disabled(!model.config.proxyEnabled)
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
                Text(verbatim: tr("status.auto_refresh.on"))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HSplitView {
                VStack(alignment: .leading, spacing: 8) {
                    Text(verbatim: tr("label.operation_history"))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    ReadOnlyTextBox(text: model.logText, accessibilityLabel: tr("label.operation_history"))
                }
                VStack(alignment: .leading, spacing: 8) {
                    Text(verbatim: tr("label.runtime_log"))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    ReadOnlyTextBox(text: model.runtimeLogText, accessibilityLabel: tr("label.runtime_log"))
                }
            }
        }
        .padding(22)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var extraRoutesError: String? {
        guard model.config.routeMode == .extra else {
            return nil
        }
        for route in model.config.extraRoutes where !SetupCIDRHelper.isValidCIDR(route) {
            return L10n.tr("validation.route_cidr_invalid", language: model.config.appLanguage, route)
        }
        return nil
    }

    private var connectionColor: Color {
        switch model.connectionRunState {
        case .connected:
            return .green
        case .disconnected:
            return .secondary
        case .error:
            return .red
        case .unknown:
            return .orange
        }
    }

    private var connectionDetailText: String {
        if !model.lastVPNStatusOutput.isEmpty {
            return model.lastVPNStatusOutput
        }
        return tr("status.connection.refresh_hint")
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
    case overview
    case connection
    case ssh
    case routes
    case service
    case advanced
    case config
    case logs

    var id: Self { self }

    var titleKey: String {
        switch self {
        case .overview:
            return "pane.overview"
        case .connection:
            return "pane.connection"
        case .ssh:
            return "pane.ssh"
        case .routes:
            return "pane.routes"
        case .service:
            return "pane.service"
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
        case .overview:
            return "gauge.medium"
        case .connection:
            return "network"
        case .ssh:
            return "terminal"
        case .routes:
            return "point.3.connected.trianglepath.dotted"
        case .service:
            return "checkmark.shield"
        case .advanced:
            return "slider.horizontal.3"
        case .config:
            return "doc.plaintext"
        case .logs:
            return "list.bullet.rectangle"
        }
    }

    var refreshesVPNStatus: Bool {
        switch self {
        case .overview, .connection, .logs:
            return true
        case .ssh, .routes, .service, .advanced, .config:
            return false
        }
    }

    var refreshesServiceStatus: Bool {
        switch self {
        case .overview, .service:
            return true
        case .connection, .ssh, .routes, .advanced, .config, .logs:
            return false
        }
    }

    var refreshesRuntimeLog: Bool {
        switch self {
        case .overview, .service, .logs:
            return true
        case .connection, .ssh, .routes, .advanced, .config:
            return false
        }
    }

    var runtimeLogRefreshInterval: TimeInterval {
        self == .logs ? 1 : 3
    }
}

private struct StatusCard: View {
    let title: String
    let value: String
    let detail: String
    let systemImage: String
    let color: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: systemImage)
                    .foregroundStyle(color)
                    .frame(width: 18)
                VStack(alignment: .leading, spacing: 4) {
                    Text(verbatim: title)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text(verbatim: value)
                        .font(.headline)
                        .lineLimit(2)
                }
                Spacer(minLength: 0)
            }

            Text(verbatim: detail)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(3)
                .truncationMode(.middle)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(12)
        .frame(minHeight: 118, maxHeight: 138, alignment: .topLeading)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }
}

private struct ServiceStatusRow: View {
    let title: String
    let value: Bool?
    let yes: String
    let no: String
    let unknown: String

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: iconName)
                .foregroundStyle(color)
                .frame(width: 18)
            Text(verbatim: title)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth, alignment: .trailing)
            Text(verbatim: label)
            Spacer()
        }
    }

    private var label: String {
        guard let value else { return unknown }
        return value ? yes : no
    }

    private var iconName: String {
        guard let value else { return "questionmark.circle" }
        return value ? "checkmark.circle.fill" : "xmark.circle.fill"
    }

    private var color: Color {
        guard let value else { return .secondary }
        return value ? .green : .orange
    }
}

private struct KeyValueRow: View {
    let title: String
    let value: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(verbatim: title)
                .foregroundStyle(.secondary)
                .frame(width: UILayout.labelWidth + 28, alignment: .trailing)
            Text(verbatim: value)
                .font(.system(.body, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer()
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
