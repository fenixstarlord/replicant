import AppKit
import Combine
import Foundation
import UserNotifications

/// Checks GitHub Releases for a newer build of this app: shortly after
/// launch, then every six hours, and on demand from Settings. A new
/// version shows in the menu and posts one notification; the download
/// link points at the matching zip (client or standalone).
@MainActor
final class UpdateChecker: ObservableObject {
    static let releasesAPI = URL(string: "https://api.github.com/repos/fenixstarlord/replicant/releases/latest")!
    static let releasesPage = URL(string: "https://github.com/fenixstarlord/replicant/releases")!

    struct Release {
        let tag: String
        let page: URL
        let download: URL?
        let notes: String
    }

    @Published private(set) var available: Release?
    @Published private(set) var lastChecked: Date?
    @Published private(set) var checking = false
    @Published private(set) var message = ""

    /// This build's version, as CFBundleShortVersionString (git describe).
    static var currentVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
    }

    private var timer: Timer?

    init() {
        Task {
            try? await Task.sleep(for: .seconds(15))
            await check()
        }
        timer = Timer.scheduledTimer(withTimeInterval: 6 * 3600, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.check() }
        }
        timer?.tolerance = 600
    }

    deinit { timer?.invalidate() }

    /// Parses "v0.1.0", "0.1.0-3-gabcdef", "v1.2" into comparable parts;
    /// nil for builds without a version (plain "dev").
    static func parts(_ v: String) -> [Int]? {
        var s = v.hasPrefix("v") ? String(v.dropFirst()) : v
        if let dash = s.firstIndex(of: "-") { s = String(s[..<dash]) }
        let nums = s.split(separator: ".").map { Int($0) }
        if nums.isEmpty || nums.contains(where: { $0 == nil }) { return nil }
        return nums.map { $0! }
    }

    static func isNewer(_ tag: String, than current: String) -> Bool {
        guard let a = parts(tag), let b = parts(current) else { return false }
        for i in 0..<max(a.count, b.count) {
            let x = i < a.count ? a[i] : 0, y = i < b.count ? b[i] : 0
            if x != y { return x > y }
        }
        return false
    }

    func check() async {
        if checking { return }
        checking = true
        defer { checking = false }
        var req = URLRequest(url: Self.releasesAPI)
        req.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        req.setValue("Replicant/\(Self.currentVersion)", forHTTPHeaderField: "User-Agent")
        req.timeoutInterval = 15
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else {
                message = "Could not check for updates (HTTP \((resp as? HTTPURLResponse)?.statusCode ?? 0))."
                return
            }
            guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let tag = json["tag_name"] as? String,
                  let page = (json["html_url"] as? String).flatMap(URL.init(string:)) else {
                message = "Unexpected reply from GitHub."
                return
            }
            lastChecked = Date()
            let wanted = LocalServer.isStandalone ? "replicant-standalone-" : "replicant-client-"
            var download: URL?
            for a in json["assets"] as? [[String: Any]] ?? [] {
                if let name = a["name"] as? String, name.hasPrefix(wanted), let u = (a["browser_download_url"] as? String).flatMap(URL.init(string:)) {
                    download = u
                }
            }
            let notes = (json["body"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if Self.isNewer(tag, than: Self.currentVersion) {
                let isNew = available?.tag != tag
                available = Release(tag: tag, page: page, download: download, notes: notes)
                message = "Version \(tag) is available; you have \(Self.currentVersion)."
                if isNew { notify(tag) }
            } else {
                available = nil
                message = Self.parts(Self.currentVersion) == nil
                    ? "This is a development build (\(Self.currentVersion)); latest release is \(tag)."
                    : "Up to date (\(Self.currentVersion); latest release is \(tag))."
            }
        } catch {
            message = "Could not check for updates: \(error.localizedDescription)"
        }
    }

    /// One notification per new version, remembered across launches.
    private func notify(_ tag: String) {
        let key = "updateNotified"
        if UserDefaults.standard.string(forKey: key) == tag { return }
        UserDefaults.standard.set(tag, forKey: key)
        let content = UNMutableNotificationContent()
        content.title = "Replicant \(tag) is available"
        content.body = "Open the menu and choose Update to download it."
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "update-\(tag)", content: content, trigger: nil))
    }

    /// Opens the download for this app's flavour, or the release page.
    func openDownload() {
        guard let r = available else { NSWorkspace.shared.open(Self.releasesPage); return }
        NSWorkspace.shared.open(r.download ?? r.page)
    }
}
