import AppKit
import Foundation

/// One extractor as reported by `shelf doctor --json`.
struct ToolStatus: Identifiable, Decodable {
    let name: String
    let version: String?
    let available: Bool
    var id: String { name }

    /// Built-in parsers never need installing; only external tools are
    /// worth reporting as missing.
    var isExternal: Bool { !(version ?? "").hasPrefix("builtin") }

    var displayName: String {
        switch name {
        case "ffprobe": return "ffprobe (FFmpeg)"
        case "art-cmd": return "ARRI Reference Tool (art-cmd)"
        case "redline": return "REDline (REDCINE-X PRO)"
        default: return name
        }
    }

    /// Vendor download page for an external tool.
    var vendorURL: URL? {
        switch name {
        case "ffprobe": return URL(string: "https://ffmpeg.org/download.html")
        case "art-cmd": return URL(string: "https://www.arri.com/en/learn-help/learn-help-camera-system/tools/arri-reference-tool")
        case "redline": return URL(string: "https://www.red.com/downloads")
        default: return nil
        }
    }

    var formats: String {
        switch name {
        case "ffprobe": return "MOV, MP4, MXF, BRAW, CRM, WAV…"
        case "art-cmd": return "ARRIRAW, ARRICORE, ARRI ProRes"
        case "redline": return "R3D"
        case "ale": return "ARRI/Avid ALE logs"
        case "bwf": return "BWF/iXML audio"
        case "sony-xml": return "Sony card XML"
        case "braw-sidecar": return "Blackmagic RAW sidecars"
        default: return ""
        }
    }
}

private struct DoctorReport: Decodable {
    let extractors: [ToolStatus]
    let docs: String?
}

/// Checks which metadata tools the bundled CLI can find.
@MainActor
final class ToolChecker: ObservableObject {
    @Published private(set) var tools: [ToolStatus] = []
    @Published private(set) var checked = false
    @Published private(set) var checking = false
    @Published private(set) var docsURL = URL(string: "https://github.com/fenixstarlord/indexserver/blob/main/docs/tools.md")!
    private var timer: Timer?

    var missing: [ToolStatus] { tools.filter { $0.isExternal && !$0.available } }

    init() {
        Task { await check() }
        // Tools get installed while the app is running; look again now and then.
        timer = Timer.scheduledTimer(withTimeInterval: 10 * 60, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.check() }
        }
    }

    func check() async {
        if checking { return }
        checking = true
        defer { checking = false }
        let (code, out) = await ShelfCLI.run(["doctor", "--json"])
        guard code == 0, let data = out.data(using: .utf8),
              let report = try? JSONDecoder().decode(DoctorReport.self, from: data) else {
            tools = []
            checked = true
            return
        }
        tools = report.extractors
        if let d = report.docs, let u = URL(string: d) { docsURL = u }
        checked = true
    }

    func openDocs() { NSWorkspace.shared.open(docsURL) }
}
