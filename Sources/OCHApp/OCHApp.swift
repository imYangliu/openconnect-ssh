import SwiftUI

@main
struct OCHApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
                .frame(minWidth: 860, minHeight: 680)
        }
        .windowStyle(.titleBar)
    }
}
