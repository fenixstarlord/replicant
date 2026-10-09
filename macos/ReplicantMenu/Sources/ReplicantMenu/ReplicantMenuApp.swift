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
    @StateObject private var local: LocalServer
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate

    init() {
        let volumes = VolumeMonitor(), scans = ScanManager(), settings = AppSettings(), local = LocalServer()
        _volumes = StateObject(wrappedValue: volumes)
        _scans = StateObject(wrappedValue: scans)
        _settings = StateObject(wrappedValue: settings)
        _autoScan = StateObject(wrappedValue: AutoScanner(volumes: volumes, scans: scans, settings: settings))
        _local = StateObject(wrappedValue: local)
        // The standalone app's main window (AppKit-owned so the menu-bar-only
        // build has no window scene at all).
        CatalogWindow.shared.content = {
            AnyView(CatalogView()
                .environmentObject(volumes)
                .environmentObject(scans)
                .environmentObject(settings)
                .environmentObject(local))
        }
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
