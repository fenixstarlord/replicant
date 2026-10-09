import AppKit
import Combine
import Foundation

/// A per-drive automatic scan rule, keyed by volume name so it survives
/// unmounting, like the ignore list.
struct AutoScanRule: Codable, Equatable {
    var onMount = false
    var intervalHours = 0   // 0 = off

    var isEmpty: Bool { !onMount && intervalHours == 0 }
    var interval: TimeInterval? { intervalHours > 0 ? TimeInterval(intervalHours) * 3600 : nil }
}

/// Scans drives without a click. Two settings:
///  - a global "scan any external drive when it is mounted" switch, and
///  - per-drive rules: on mount, on an interval while mounted, or both.
/// Ignored drives and the boot volume are never scanned automatically.
@MainActor
final class AutoScanner: ObservableObject {
    static let intervals: [(hours: Int, label: String)] = [
        (0, "Off"), (1, "Every hour"), (6, "Every 6 hours"), (12, "Every 12 hours"), (24, "Daily"), (168, "Weekly"),
    ]

    /// Global: scan every external, non-ignored drive when it is mounted.
    @Published var allOnMount: Bool { didSet { defaults.set(allOnMount, forKey: "autoScanOnMount") } }
    /// Per-drive rules by volume name.
    @Published private(set) var rules: [String: AutoScanRule]
    /// Last successful scan per volume path, manual or automatic.
    @Published private(set) var lastScanned: [String: Date]

    private let defaults = UserDefaults.standard
    private let volumes: VolumeMonitor
    private let scans: ScanManager
    private let settings: AppSettings
    private var observer: NSObjectProtocol?
    private var timer: Timer?
    private var known: Set<String>

    init(volumes: VolumeMonitor, scans: ScanManager, settings: AppSettings) {
        self.volumes = volumes
        self.scans = scans
        self.settings = settings
        allOnMount = defaults.bool(forKey: "autoScanOnMount")
        if let data = defaults.data(forKey: "autoScanRules"), let r = try? JSONDecoder().decode([String: AutoScanRule].self, from: data) {
            rules = r
        } else {
            rules = [:]
        }
        lastScanned = (defaults.dictionary(forKey: "autoScanLast") as? [String: Date]) ?? [:]
        known = Set(volumes.volumes.map(\.id))

        scans.onFinished = { [weak self] url, ok in
            guard let self, ok else { return }
            lastScanned[url.path] = Date()
            defaults.set(lastScanned, forKey: "autoScanLast")
        }
        observer = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didMountNotification, object: nil, queue: .main) { [weak self] note in
            let url = note.userInfo?[NSWorkspace.volumeURLUserInfoKey] as? URL
            Task { @MainActor in self?.mounted(url) }
        }
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
        timer?.tolerance = 10
    }

    deinit {
        if let observer { NSWorkspace.shared.notificationCenter.removeObserver(observer) }
        timer?.invalidate()
    }

    // MARK: rules

    func rule(for name: String) -> AutoScanRule { rules[name] ?? AutoScanRule() }

    func setRule(_ rule: AutoScanRule, for name: String) {
        if rule.isEmpty { rules.removeValue(forKey: name) } else { rules[name] = rule }
        if let data = try? JSONEncoder().encode(rules) { defaults.set(data, forKey: "autoScanRules") }
    }

    /// Names with a rule that are not currently mounted.
    var unmountedRuleNames: [String] {
        let mounted = Set(volumes.volumes.map(\.name))
        return rules.keys.filter { !mounted.contains($0) }.sorted()
    }

    /// Volumes that may ever be scanned automatically.
    func allowed(_ v: Volume) -> Bool { !v.isRoot && !settings.isIgnored(v) }

    /// Whether a mount of this volume triggers a scan.
    func scansOnMount(_ v: Volume) -> Bool {
        guard allowed(v) else { return false }
        return rule(for: v.name).onMount || (allOnMount && !v.isInternal)
    }

    // MARK: triggers

    private func mounted(_ url: URL?) {
        volumes.refresh()
        let current = Set(volumes.volumes.map(\.id))
        let added = current.subtracting(known)
        known = current
        for v in volumes.volumes where (added.contains(v.id) || v.url == url) && scansOnMount(v) {
            scans.scan(v.url)
        }
    }

    private func tick() {
        known = Set(volumes.volumes.map(\.id))
        for v in volumes.volumes where allowed(v) {
            guard let interval = rule(for: v.name).interval else { continue }
            if let last = lastScanned[v.url.path], Date().timeIntervalSince(last) < interval { continue }
            if scans.current == v.name || scans.queue.contains(v.url) { continue }
            scans.scan(v.url)
        }
    }
}
