import AppKit
import SwiftUI

struct MenuContent: View {
    @EnvironmentObject var volumes: VolumeMonitor
    @EnvironmentObject var scans: ScanManager
    @EnvironmentObject var settings: AppSettings
    @EnvironmentObject var toolsCheck: ToolChecker
    @EnvironmentObject var autoScan: AutoScanner
    @EnvironmentObject var local: LocalServer

    var body: some View {
        if LocalServer.isStandalone {
            Button(local.running ? "Open catalog" : local.status) { local.open() }.disabled(!local.running)
            Divider()
        }
        if let name = scans.current {
            Text("Scanning \(name)")
            Text(scans.status).font(.caption)
            Button("Cancel scan") { scans.cancel() }
            if !scans.queue.isEmpty { Text("Queued: \(scans.queue.map(\.lastPathComponent).joined(separator: ", "))") }
            Divider()
        }
        if !LocalServer.isStandalone && (settings.server.isEmpty || !settings.hasToken) {
            Button("Set up server and API key…") { showSettings() }
            Divider()
        }
        let visible = volumes.volumes.filter { !settings.isIgnored($0) }
        if visible.isEmpty {
            Text("No drives").foregroundStyle(.secondary)
        } else {
            ForEach(visible) { v in
                Button {
                    scans.scan(v.url)
                } label: {
                    Label(v.name, systemImage: v.isInternal ? "internaldrive" : "externaldrive")
                }
                .disabled(scans.current == v.name)
            }
        }
        Divider()
        Button("Browse…") { browse() }
        if !scans.recent.isEmpty {
            Menu("Recent scans") {
                ForEach(scans.recent) { r in
                    Text("\(r.ok ? "✓" : "✕") \(r.name) · \(r.summary)")
                }
            }
        }
        Divider()
        Button(toolsCheck.missing.isEmpty ? "Settings…" : "⚠️ Settings…") { showSettings() }
        if !toolsCheck.missing.isEmpty {
            Button(toolsCheck.checking ? "Checking tools…" : "↻ Recheck tools") { Task { await toolsCheck.check() } }
                .disabled(toolsCheck.checking)
        }
        Button("Quit Shelf") { NSApplication.shared.terminate(nil) }
    }

    private func showSettings() {
        Task { await toolsCheck.check() }
        SettingsWindow.shared.show(
            SettingsView()
                .environmentObject(volumes)
                .environmentObject(scans)
                .environmentObject(settings)
                .environmentObject(toolsCheck)
                .environmentObject(autoScan)
                .environmentObject(local)
        )
    }

    private func browse() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Scan"
        panel.message = "Choose a drive, network share, or folder to scan."
        NSApp.activate(ignoringOtherApps: true)
        if panel.runModal() == .OK, let url = panel.url {
            scans.scan(url)
        }
    }
}
