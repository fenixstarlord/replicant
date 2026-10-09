import AppKit
import Combine
import Foundation

/// Scans drives without a click: when a drive is mounted, and/or on a
/// fixed interval while it stays mounted. Ignored drives and the boot
/// volume are never scanned automatically; internal drives only when
/// opted in. Settings live in UserDefaults.
@MainActor
final class AutoScanner: ObservableObject {
    static let intervals: [(hours: Int, label: String)] = [
        (0, "Off"), (1, "Every hour"), (6, "Every 6 hours"), (12, "Every 12 hours"), (24, "Daily"), (168, "Weekly"),
    ]

    @Published var onMount: Bool { didSet { defaults.set(onMount, forKey: "autoScanOnMount") } }
    @Published var intervalHours: Int { didSet { defaults.set(intervalHours, forKey: "autoScanIntervalHours") } }
    @Published var includeInternal: Bool { didSet { defaults.set(includeInternal, forKey: "autoScanInternal") } }
    /// Last finished scan per volume path, manual or automatic.
    @Published private(set) var lastScanned: [String: Date]
    @Published private(set) var lastCheck: Date? = nil

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
        onMount = defaults.bool(forKey: "autoScanOnMount")
        intervalHours = defaults.integer(forKey: "autoScanIntervalHours")
        includeInternal = defaults.bool(forKey: "autoScanInternal")
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

    /// Volumes that may be scanned automatically.
    func eligible(_ v: Volume) -> Bool {
        if v.isRoot || settings.isIgnored(v) { return false }
        if v.isInternal && !includeInternal { return false }
        return true
    }

    var interval: TimeInterval? { intervalHours > 0 ? TimeInterval(intervalHours) * 3600 : nil }

    private func mounted(_ url: URL?) {
        volumes.refresh()
        let current = Set(volumes.volumes.map(\.id))
        let added = current.subtracting(known)
        known = current
        guard onMount else { return }
        for v in volumes.volumes where added.contains(v.id) || v.url == url {
            if eligible(v) { scans.scan(v.url) }
        }
    }

    private func tick() {
        known = Set(volumes.volumes.map(\.id))
        guard let interval else { return }
        lastCheck = Date()
        for v in volumes.volumes where eligible(v) {
            if let last = lastScanned[v.url.path], Date().timeIntervalSince(last) < interval { continue }
            if scans.current == v.name || scans.queue.contains(v.url) { continue }
            scans.scan(v.url)
        }
    }

    /// When the next interval scan of a volume is due, for display.
    func nextDue(_ v: Volume) -> Date? {
        guard let interval, eligible(v) else { return nil }
        guard let last = lastScanned[v.url.path] else { return Date() }
        return last.addingTimeInterval(interval)
    }
}
