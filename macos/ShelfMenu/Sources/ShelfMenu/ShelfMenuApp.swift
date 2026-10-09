import SwiftUI

/// Shelf menu bar app: lists mounted drives, scans one with a click, and
/// pushes the result to the server through the bundled `shelf` CLI.
@main
struct ShelfMenuApp: App {
    @StateObject private var volumes = VolumeMonitor()
    @StateObject private var scans = ScanManager()
    @StateObject private var settings = AppSettings()
    @StateObject private var toolsCheck = ToolChecker()

    var body: some Scene {
        MenuBarExtra {
            MenuContent()
                .environmentObject(volumes)
                .environmentObject(scans)
                .environmentObject(settings)
                .environmentObject(toolsCheck)
        } label: {
            Image(systemName: scans.isScanning ? "externaldrive.badge.timemachine" : "externaldrive")
        }
        .menuBarExtraStyle(.menu)

        Settings {
            SettingsView()
                .environmentObject(volumes)
                .environmentObject(scans)
                .environmentObject(settings)
                .environmentObject(toolsCheck)
        }
    }
}
