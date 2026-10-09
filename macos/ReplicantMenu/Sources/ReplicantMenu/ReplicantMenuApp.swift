import SwiftUI

/// Replicant menu bar app: lists mounted drives, scans one with a click, and
/// pushes the result to the server through the bundled `replicant` CLI.
@main
struct ReplicantMenuApp: App {
    @StateObject private var volumes: VolumeMonitor
    @StateObject private var scans: ScanManager
    @StateObject private var settings: AppSettings
    @StateObject private var toolsCheck = ToolChecker()
    @StateObject private var autoScan: AutoScanner
    @StateObject private var local = LocalServer()

    init() {
        let volumes = VolumeMonitor(), scans = ScanManager(), settings = AppSettings()
        _volumes = StateObject(wrappedValue: volumes)
        _scans = StateObject(wrappedValue: scans)
        _settings = StateObject(wrappedValue: settings)
        _autoScan = StateObject(wrappedValue: AutoScanner(volumes: volumes, scans: scans, settings: settings))
    }

    var body: some Scene {
        MenuBarExtra {
            MenuContent()
                .environmentObject(volumes)
                .environmentObject(scans)
                .environmentObject(settings)
                .environmentObject(toolsCheck)
                .environmentObject(autoScan)
                .environmentObject(local)
        } label: {
            Image(systemName: scans.isScanning ? "externaldrive.badge.timemachine" : "externaldrive")
        }
        .menuBarExtraStyle(.menu)

    }
}
