import AppKit
import SwiftUI

@main
struct OCHApp: App {
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup("OCH", id: "main") {
            ContentView(model: model)
        }
        .windowStyle(.titleBar)

        MenuBarExtra("OCH", systemImage: model.traySystemImage) {
            TrayMenuView(model: model)
        }
    }
}

private struct TrayMenuView: View {
    @ObservedObject var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack {
            Label {
                Text(verbatim: model.connectionSummaryText)
            } icon: {
                Image(systemName: model.traySystemImage)
            }

            Text(verbatim: model.serviceSummaryText)
            Text(verbatim: model.serviceFallbackText)

            Divider()

            Button {
                model.connect()
            } label: {
                Label(tr("button.connect"), systemImage: "bolt.horizontal.circle")
            }
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
                Label(tr("button.refresh_status"), systemImage: "arrow.clockwise")
            }
            .disabled(model.isBusy)

            Divider()

            Button {
                openWindow(id: "main")
                NSApp.activate(ignoringOtherApps: true)
            } label: {
                Label(tr("tray.open_window"), systemImage: "macwindow")
            }

            Button {
                NSApp.terminate(nil)
            } label: {
                Label(tr("tray.quit"), systemImage: "power")
            }
        }
        .environment(\.locale, model.config.appLanguage.locale)
    }

    private func tr(_ key: String) -> String {
        L10n.tr(key, language: model.config.appLanguage)
    }
}
