import Foundation
import ServiceManagement

/// Settings shared with the `shelf` CLI (server and API key live in
/// ~/.config/shelf/config.toml, written by `shelf login`) plus the app's
/// own ignore list and launch-at-login flag.
@MainActor
final class AppSettings: ObservableObject {
    @Published var server: String = ""
    @Published var hasToken: Bool = false
    @Published var ignored: Set<String> {
        didSet { UserDefaults.standard.set(Array(ignored).sorted(), forKey: "ignoredVolumes") }
    }
    @Published var launchAtLogin: Bool = SMAppService.mainApp.status == .enabled

    init() {
        ignored = Set(UserDefaults.standard.stringArray(forKey: "ignoredVolumes") ?? [])
        reload()
    }

    static var configURL: URL {
        if let p = ProcessInfo.processInfo.environment["SHELF_CONFIG"], !p.isEmpty { return URL(fileURLWithPath: p) }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".config/shelf/config.toml")
    }

    /// Reads the two keys we care about from the TOML file. The CLI owns
    /// the file; this is only for display.
    func reload() {
        server = ""
        hasToken = false
        guard let text = try? String(contentsOf: Self.configURL, encoding: .utf8) else { return }
        for raw in text.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("[") { break } // top-level keys come first
            guard let eq = line.firstIndex(of: "=") else { continue }
            let key = line[..<eq].trimmingCharacters(in: .whitespaces)
            var value = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
            if value.hasPrefix("\"") && value.hasSuffix("\"") && value.count >= 2 { value = String(value.dropFirst().dropLast()) }
            switch key {
            case "server": server = value
            case "token": hasToken = !value.isEmpty
            default: break
            }
        }
    }

    func isIgnored(_ v: Volume) -> Bool { ignored.contains(v.name) }

    func setIgnored(_ name: String, _ on: Bool) {
        if on { ignored.insert(name) } else { ignored.remove(name) }
    }

    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
        } catch {
            NSLog("launch at login: \(error)")
        }
        launchAtLogin = SMAppService.mainApp.status == .enabled
    }
}
