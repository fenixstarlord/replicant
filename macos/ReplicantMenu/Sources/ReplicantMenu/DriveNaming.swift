import AppKit
import Foundation

/// The first time a drive is chosen for a scan on this Mac, asks what the
/// catalog should call it. The name is the catalog's only: the drive itself
/// is never renamed or written to. Later choices scan straight away; the
/// drive can be renamed any time on the server.
@MainActor
enum DriveNaming {
    /// Drives whose naming dialog is open or whose lookup is running.
    private static var asking: Set<String> = []

    /// Scans v, asking for its catalog name first if this Mac has not named it.
    static func scan(_ v: Volume, settings: AppSettings, scans: ScanManager) {
        if settings.wasNamed(v) {
            scans.scan(v.url)
            return
        }
        let key = v.stableKey
        guard !asking.contains(key) else { return }
        asking.insert(key)
        Task {
            defer { asking.remove(key) }
            let info = await lookup(v.url)
            // What the catalog will call the drive if no name is sent: its custom
            // name if it has one, else the name the volume reports (for a network
            // share that can be its mount path).
            let volumeName = info?.volume_name.flatMap { $0.isEmpty ? nil : $0 } ?? v.name
            let current = info?.known == true && info?.renamed == true ? (info?.name ?? volumeName) : volumeName
            guard let chosen = ask(volume: v.name, suggestion: current, volumeName: volumeName) else { return }
            // Send a name only when it differs from what the catalog would store
            // anyway; the volume's own name tells the server to follow it again.
            let name = chosen == current ? nil : chosen
            if name == nil {
                settings.markNamed(v)
                scans.scan(v.url)
                return
            }
            // Remember the drive as named only once the name has been stored, so
            // a failed or cancelled scan asks again next time.
            scans.scan(ScanRequest(url: v.url, name: name, volumeKey: key) { ok in
                if ok { settings.markNamed(v) }
            })
        }
    }

    /// What `replicant drive --json` reports for a path.
    private struct DriveInfo: Decodable {
        let known: Bool
        let name: String?
        let volume_name: String?
        let renamed: Bool?
    }

    private static func lookup(_ url: URL) async -> DriveInfo? {
        let (code, out) = await ReplicantCLI.run(["drive", "--json", url.path])
        guard code == 0,
              let line = out.split(separator: "\n").last(where: { $0.hasPrefix("{") })
        else { return nil }
        return try? JSONDecoder().decode(DriveInfo.self, from: Data(line.utf8))
    }

    /// Shows the naming dialog; returns the chosen name, or nil if cancelled.
    /// An empty field means the volume's own name.
    private static func ask(volume: String, suggestion: String, volumeName: String) -> String? {
        let alert = NSAlert()
        alert.messageText = "Name “\(volume)” in the catalog"
        alert.informativeText = "This is the name the catalog shows for this drive. The drive itself is not changed. Leave it empty to use the volume's name. You can rename the drive later on the server."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.stringValue = suggestion
        field.placeholderString = volumeName
        alert.accessoryView = field
        alert.addButton(withTitle: "Scan")
        alert.addButton(withTitle: "Cancel")
        alert.window.initialFirstResponder = field
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else { return nil }
        let name = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? volumeName : name
    }
}
