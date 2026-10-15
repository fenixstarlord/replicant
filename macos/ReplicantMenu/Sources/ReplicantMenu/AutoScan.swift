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
    /// External drive names ever mounted while the app ran.
    @Published private(set) var seen: Set<String>
    /// Drive names in the server's catalog, from `replicant drives --json`.
    @Published private(set) var catalog: [String] = []

    private let defaults = UserDefaults.standard
    private let volumes: VolumeMonitor
    private let scans: ScanManager
    private let settings: AppSettings
    private var observer: NSObjectProtocol?
    private var unmountObserver: NSObjectProtocol?
    private var timer: Timer?
    private var known: Set<String>
    /// Per volume path: no automatic retry before `until`, after a failed or
    /// cancelled scan; `failures` counts failures in a row for the back-off.
    private var retry: [String: (until: Date, failures: Int)] = [:]

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
        seen = Set(defaults.stringArray(forKey: "autoScanSeen") ?? [])
        known = Set(volumes.volumes.map(\.id))
        remember()

        scans.onFinished = { [weak self] url, outcome in
            self?.finished(url, outcome)
        }
        observer = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didMountNotification, object: nil, queue: .main) { [weak self] note in
            let url = note.userInfo?[NSWorkspace.volumeURLUserInfoKey] as? URL
            Task { @MainActor in self?.mounted(url) }
        }
        // A back-off belongs to the drive that failed: forget it when that drive
        // goes, so another drive mounted at the same path starts fresh.
        unmountObserver = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didUnmountNotification, object: nil, queue: .main) { [weak self] note in
            guard let path = (note.userInfo?[NSWorkspace.volumeURLUserInfoKey] as? URL)?.path else { return }
            Task { @MainActor in self?.retry.removeValue(forKey: path) }
        }
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
        timer?.tolerance = 10
    }

    deinit {
        if let observer { NSWorkspace.shared.notificationCenter.removeObserver(observer) }
        if let unmountObserver { NSWorkspace.shared.notificationCenter.removeObserver(unmountObserver) }
        timer?.invalidate()
    }

    // MARK: rules

    func rule(for name: String) -> AutoScanRule { rules[name] ?? AutoScanRule() }

    func setRule(_ rule: AutoScanRule, for name: String) {
        if rule.isEmpty { rules.removeValue(forKey: name) } else { rules[name] = rule }
        if let data = try? JSONEncoder().encode(rules) { defaults.set(data, forKey: "autoScanRules") }
    }

    /// Records the external drives mounted right now.
    private func remember() {
        let names = volumes.volumes.filter { !$0.isRoot && !$0.isInternal }.map(\.name)
        let merged = seen.union(names)
        if merged != seen {
            seen = merged
            defaults.set(Array(seen).sorted(), forKey: "autoScanSeen")
        }
    }

    /// Asks the server which drives it knows, so unplugged drives can
    /// get rules too.
    func refreshCatalog() async {
        let (code, out) = await ReplicantCLI.run(["drives", "--json"])
        guard code == 0, let data = out.data(using: .utf8),
              let rows = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] else { return }
        // Rules match mounted volumes by their own name, so offer the name the
        // volume reports, not a catalog name the drive may have been given.
        catalog = rows.compactMap { row in
            if let v = row["volume_name"] as? String, !v.isEmpty { return v }
            return row["name"] as? String
        }.sorted()
    }

    /// Every drive name that can have a rule, mounted or not: mounted
    /// drives, the server's catalog, drives seen before, and existing rules.
    var ruleNames: [String] {
        var names = Set(volumes.volumes.filter { allowed($0) }.map(\.name))
        names.formUnion(catalog)
        names.formUnion(seen)
        names.formUnion(rules.keys)
        names.subtract(settings.ignored)
        return names.sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending }
    }

    /// Volumes that may ever be scanned automatically.
    func allowed(_ v: Volume) -> Bool { !v.isRoot && !settings.isIgnored(v) }

    /// Whether a mount of this volume triggers a scan.
    func scansOnMount(_ v: Volume) -> Bool {
        guard allowed(v) else { return false }
        return rule(for: v.name).onMount || (allOnMount && !v.isInternal)
    }

    // MARK: results

    /// Records how a scan ended, for the interval rules. A failure is retried
    /// after a growing pause rather than at the next minute's check; a cancel
    /// waits for the drive's next interval; an eject or a removed drive
    /// changes nothing (the drive is gone).
    private func finished(_ url: URL, _ outcome: ScanOutcome) {
        let path = url.path
        switch outcome {
        case .succeeded:
            lastScanned[path] = Date()
            defaults.set(lastScanned, forKey: "autoScanLast")
            retry[path] = nil
        case .failed:
            let n = (retry[path]?.failures ?? 0) + 1
            retry[path] = (Date().addingTimeInterval(Self.backoff(failures: n, interval: interval(forPath: path))), n)
        case .cancelled:
            retry[path] = (Date().addingTimeInterval(interval(forPath: path) ?? Self.maxBackoff), retry[path]?.failures ?? 0)
        case .driveRemoved where volumes.volumes.contains(where: { $0.url.path == path }):
            // "Removed", yet still mounted: the scan lost its folder some other
            // way (a network hiccup); back off like any failure.
            finished(url, .failed)
        case .stoppedForEject, .driveRemoved, .quit:
            break
        }
    }

    nonisolated static let maxBackoff: TimeInterval = 6 * 3600

    /// The pause before the next automatic try after `failures` failures in
    /// a row: 5, 10, 20, 40 minutes and so on, never longer than the drive's
    /// interval or 6 hours.
    nonisolated static func backoff(failures: Int, interval: TimeInterval?) -> TimeInterval {
        let pause = 5 * 60 * pow(2, Double(min(max(failures, 1), 12) - 1))
        return min(pause, interval ?? maxBackoff, maxBackoff)
    }

    private func interval(forPath path: String) -> TimeInterval? {
        volumes.volumes.first { $0.url.path == path }.flatMap { rule(for: $0.name).interval }
    }

    // MARK: triggers

    private func mounted(_ url: URL?) {
        volumes.refresh()
        remember()
        let current = Set(volumes.volumes.map(\.id))
        let added = current.subtracting(known)
        known = current
        for v in volumes.volumes where (added.contains(v.id) || v.url == url) && scansOnMount(v) {
            scans.scan(ScanRequest(url: v.url, automatic: true))
        }
    }

    private func tick() {
        known = Set(volumes.volumes.map(\.id))
        for v in volumes.volumes where allowed(v) {
            guard let interval = rule(for: v.name).interval else { continue }
            if let last = lastScanned[v.url.path], Date().timeIntervalSince(last) < interval { continue }
            if let r = retry[v.url.path], Date() < r.until { continue }
            if scans.currentURL == v.url || scans.queue.contains(v.url) { continue }
            scans.scan(ScanRequest(url: v.url, automatic: true))
        }
    }
}
