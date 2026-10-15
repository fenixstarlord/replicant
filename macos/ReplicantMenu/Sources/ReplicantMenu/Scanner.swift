import AppKit
import Foundation
import UserNotifications

/// Finds the bundled `replicant` CLI.
enum ReplicantCLI {
    /// Extra environment for every CLI run (the standalone app sets REPLICANT_CONFIG).
    nonisolated(unsafe) static var environment: [String: String] = [:]

    static func apply(to p: Process) {
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = (env["PATH"] ?? "/usr/bin:/bin") + ":/opt/homebrew/bin:/usr/local/bin"
        // A scan stops by itself if the app quits or crashes, so it never keeps
        // a drive busy with nobody to report to.
        env["REPLICANT_EXIT_WITH_PARENT"] = "1"
        for (k, v) in environment { env[k] = v }
        p.environment = env
    }

    static var url: URL? {
        if let u = Bundle.main.url(forAuxiliaryExecutable: "replicant"), FileManager.default.isExecutableFile(atPath: u.path) { return u }
        let exe = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        // Development: swift run from macos/ReplicantMenu with the Go binary in ../../bin/replicant.
        let candidates = [
            exe.deletingLastPathComponent().appendingPathComponent("replicant"),
            exe.deletingLastPathComponent().appendingPathComponent("../../../../bin/replicant").standardized,
            URL(fileURLWithPath: "/usr/local/bin/replicant"),
            URL(fileURLWithPath: "/opt/homebrew/bin/replicant"),
        ]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0.path) }
    }

    /// Runs the CLI to completion, returning exit code and combined output.
    static func run(_ args: [String], stdin: String? = nil) async -> (Int32, String) {
        guard let url else { return (127, "replicant CLI not found") }
        return await withCheckedContinuation { cont in
            let p = Process()
            p.executableURL = url
            p.arguments = args
            apply(to: p)
            let out = Pipe()
            p.standardOutput = out
            p.standardError = out
            if let stdin {
                let inPipe = Pipe()
                p.standardInput = inPipe
                inPipe.fileHandleForWriting.write(stdin.data(using: .utf8)!)
                try? inPipe.fileHandleForWriting.close()
            }
            p.terminationHandler = { proc in
                let data = out.fileHandleForReading.readDataToEndOfFile()
                cont.resume(returning: (proc.terminationStatus, String(decoding: data, as: UTF8.self)))
            }
            do { try p.run() } catch { cont.resume(returning: (127, "\(error)")) }
        }
    }
}

/// How a scan ended.
enum ScanOutcome: Sendable {
    case succeeded
    case failed
    case cancelled        // Cancel scan
    case stoppedForEject  // stopped so the drive could be ejected
    case driveRemoved     // the drive went away mid-scan (CLI exit 3)
    case quit             // stopped because the app quit

    var symbol: String {
        switch self {
        case .succeeded: "✓"
        case .failed: "✕"
        default: "⏹"
        }
    }
}

/// One finished, failed or stopped scan, for the Recent submenu.
struct ScanRecord: Identifiable {
    let id = UUID()
    let path: String
    let name: String
    let finished: Date
    let outcome: ScanOutcome
    let summary: String
}

/// A scan waiting to run, or running.
struct ScanRequest {
    let url: URL
    /// Catalog name for the drive (`replicant scan --name`), if one was chosen.
    var name: String? = nil
    /// Volume.stableKey of the drive the name was chosen for; checked again
    /// before the scan runs, so the name never lands on another drive
    /// mounted at the same path.
    var volumeKey: String? = nil
    /// Called once the request ends: whether it was scanned with the name applied.
    var onDone: ((Bool) -> Void)? = nil
    /// Started by an auto-scan rule rather than by the user.
    var automatic = false
}

/// Runs scans one at a time through `replicant scan <path>` and reports progress.
@MainActor
final class ScanManager: ObservableObject {
    @Published private(set) var current: String? = nil      // display name
    @Published private(set) var currentURL: URL? = nil       // what is being scanned
    @Published private(set) var status: String = ""          // last progress line
    @Published private(set) var queue: [URL] = []            // waiting, for display
    @Published private(set) var recent: [ScanRecord] = []
    /// Called after every scan with the path and how it ended.
    var onFinished: ((URL, ScanOutcome) -> Void)?
    private var pending: [ScanRequest] = []
    private var observers: [NSObjectProtocol] = []

    var isScanning: Bool { current != nil }

    init() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
        // Quitting stops the running scan and its tools (a crash is covered by
        // the CLI itself, see REPLICANT_EXIT_WITH_PARENT).
        observers.append(NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification, object: nil, queue: .main
        ) { _ in
            ActiveScan.shared.stop(.quit, timeout: 2)
        })
        // A drive being ejected: forget queued scans of it (EjectWatcher stops
        // the running one).
        observers.append(NotificationCenter.default.addObserver(
            forName: .volumeEjecting, object: nil, queue: .main
        ) { [weak self] note in
            guard let volume = note.object as? String else { return }
            MainActor.assumeIsolated { self?.dropQueued(onVolume: volume) }
        })
    }

    /// Scans url now, or queues it behind the running scan.
    func scan(_ url: URL) { scan(ScanRequest(url: url)) }

    /// Scans a request now, or queues it. A plain request for something
    /// already running or queued is dropped; a named one replaces a queued
    /// plain one, or is queued behind a running scan of the same path.
    func scan(_ req: ScanRequest) {
        if ActiveScan.shared.isEjecting(req.url.path) {
            if req.automatic {
                req.onDone?(false)
                return
            }
            // The user picked the drive, so it is still there (an eject that
            // did not go through).
            ActiveScan.shared.clearEjecting(covering: req.url.path)
        }
        if let i = pending.firstIndex(where: { $0.url == req.url }) {
            if req.name != nil {
                pending[i].onDone?(false)
                pending[i] = req
            } else {
                req.onDone?(false)
            }
            return
        }
        if currentURL == req.url && req.name == nil {
            req.onDone?(false)
            return
        }
        if currentURL != nil {
            pending.append(req)
            queue = pending.map(\.url)
            return
        }
        start(req)
    }

    /// Marks the request as running at once, so a second call in the same
    /// turn queues instead of starting a parallel scan.
    private func start(_ req: ScanRequest) {
        current = req.name ?? req.url.lastPathComponent
        currentURL = req.url
        status = "Starting…"
        Task { await run(req) }
    }

    func cancel() {
        ActiveScan.shared.interrupt(.user)
    }

    private func dropQueued(onVolume volume: String) {
        let dropped = pending.filter { ActiveScan.shared.isEjecting($0.url.path) }
        guard !dropped.isEmpty else { return }
        pending.removeAll { ActiveScan.shared.isEjecting($0.url.path) }
        queue = pending.map(\.url)
        dropped.forEach { $0.onDone?(false) }
    }

    private func run(_ req: ScanRequest) async {
        let url = req.url
        var chosen = req.name
        if chosen != nil, let key = req.volumeKey, Volume.stableKey(of: url) != key {
            // Another drive is mounted at this path now; the name was not for it.
            chosen = nil
        }
        let name = current ?? url.lastPathComponent
        guard let cli = ReplicantCLI.url else {
            finish(url, name: name, outcome: .failed, summary: "replicant CLI not found")
            req.onDone?(false)
            next()
            return
        }
        let p = Process()
        p.executableURL = cli
        p.arguments = chosen.map { ["scan", "--name", $0, url.path] } ?? ["scan", url.path]
        ReplicantCLI.apply(to: p)
        let errPipe = Pipe(), outPipe = Pipe()
        p.standardError = errPipe
        p.standardOutput = outPipe

        // Stream stderr for progress; collect stdout for the result line.
        errPipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let chunk = String(decoding: h.availableData, as: UTF8.self)
            guard !chunk.isEmpty else { return }
            // The CLI has closed every file on the drive (it is writing or
            // uploading the bundle now): an eject need not stop it.
            if chunk.contains("finished reading ") { ActiveScan.shared.doneReading() }
            let lines = chunk.replacingOccurrences(of: "\r", with: "\n").split(separator: "\n").map(String.init)
            if let last = lines.last(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) {
                Task { @MainActor in self?.status = last }
            }
        }
        // stdout carries only the final result line; read it after exit.
        guard ActiveScan.shared.begin(path: url.path) else {
            // The drive started ejecting before the scan could start.
            current = nil
            currentURL = nil
            status = ""
            req.onDone?(false)
            next()
            return
        }
        let code: Int32 = await withCheckedContinuation { cont in
            p.terminationHandler = { proc in
                ActiveScan.shared.ended()
                cont.resume(returning: proc.terminationStatus)
            }
            do {
                try p.run()
                ActiveScan.shared.attach(pid: p.processIdentifier)
            } catch {
                ActiveScan.shared.ended()
                cont.resume(returning: 127)
                Task { @MainActor in self.status = "\(error)" }
            }
        }
        errPipe.fileHandleForReading.readabilityHandler = nil
        let stdout = String(decoding: outPipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        let outcome = Self.outcome(code: code, stopped: ActiveScan.shared.stopReason)
        let summary: String
        switch outcome {
        case .succeeded: summary = stdout.split(separator: "\n").last.map(String.init) ?? "done"
        case .failed: summary = status
        case .cancelled: summary = "cancelled"
        case .stoppedForEject: summary = "stopped so the drive could be ejected"
        case .driveRemoved: summary = "the drive went away during the scan; nothing was stored"
        case .quit: summary = "stopped because Replicant quit"
        }
        finish(url, name: name, outcome: outcome, summary: summary)
        req.onDone?(outcome == .succeeded && chosen != nil)
        next()
    }

    /// Classifies a finished scan from the CLI's exit code (0 ok, 3 drive
    /// gone, 130 interrupted) and whether the app stopped it.
    nonisolated static func outcome(code: Int32, stopped: StopReason?) -> ScanOutcome {
        if code == 0 { return .succeeded }
        switch stopped {
        case .eject?: return .stoppedForEject
        case .quit?: return .quit
        case .user?: return .cancelled
        case nil: break
        }
        switch code {
        case 3: return .driveRemoved
        case 130: return .cancelled
        default: return .failed
        }
    }

    /// Starts the next queued scan, if any, skipping drives being ejected.
    private func next() {
        while !pending.isEmpty {
            let req = pending.removeFirst()
            queue = pending.map(\.url)
            if ActiveScan.shared.isEjecting(req.url.path) {
                req.onDone?(false)
                continue
            }
            start(req)
            return
        }
    }

    private func finish(_ url: URL, name: String, outcome: ScanOutcome, summary: String) {
        recent.insert(ScanRecord(path: url.path, name: name, finished: Date(), outcome: outcome, summary: summary), at: 0)
        if recent.count > 10 { recent.removeLast() }
        current = nil
        currentURL = nil
        status = ""
        onFinished?(url, outcome)
        NotificationCenter.default.post(name: .scanFinished, object: nil)
        let title: String
        switch outcome {
        case .succeeded: title = "Scanned \(name)"
        case .failed: title = "Scan of \(name) failed"
        case .stoppedForEject: title = "Stopped scanning \(name) so it could be ejected"
        case .driveRemoved: title = "Scan of \(name) stopped"
        case .cancelled, .quit: return // the user asked for it
        }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = outcome == .stoppedForEject ? "Nothing was stored. Scan it again next time it is connected." : summary
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil))
    }
}
