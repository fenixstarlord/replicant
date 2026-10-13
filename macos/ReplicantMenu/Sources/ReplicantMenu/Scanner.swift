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

/// One finished or failed scan, for the Recent submenu.
struct ScanRecord: Identifiable {
    let id = UUID()
    let path: String
    let name: String
    let finished: Date
    let ok: Bool
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
    /// Called once the scan ends: whether it succeeded with the name applied.
    var onDone: ((Bool) -> Void)? = nil
}

/// Runs scans one at a time through `replicant scan <path>` and reports progress.
@MainActor
final class ScanManager: ObservableObject {
    @Published private(set) var current: String? = nil      // display name
    @Published private(set) var currentURL: URL? = nil       // what is being scanned
    @Published private(set) var status: String = ""          // last progress line
    @Published private(set) var queue: [URL] = []            // waiting, for display
    @Published private(set) var recent: [ScanRecord] = []
    /// Called after every scan with the path and whether it succeeded.
    var onFinished: ((URL, Bool) -> Void)?
    private var pending: [ScanRequest] = []
    private var process: Process?

    var isScanning: Bool { current != nil }

    init() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    /// Scans url now, or queues it behind the running scan.
    func scan(_ url: URL) { scan(ScanRequest(url: url)) }

    /// Scans a request now, or queues it. A plain request for something
    /// already running or queued is dropped; a named one replaces a queued
    /// plain one, or is queued behind a running scan of the same path.
    func scan(_ req: ScanRequest) {
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
        process?.interrupt()
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
            finish(url, name: name, ok: false, summary: "replicant CLI not found")
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
        process = p

        // Stream stderr for progress; collect stdout for the result line.
        errPipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let chunk = String(decoding: h.availableData, as: UTF8.self)
            guard !chunk.isEmpty else { return }
            let lines = chunk.replacingOccurrences(of: "\r", with: "\n").split(separator: "\n").map(String.init)
            if let last = lines.last(where: { !$0.trimmingCharacters(in: .whitespaces).isEmpty }) {
                Task { @MainActor in self?.status = last }
            }
        }
        // stdout carries only the final result line; read it after exit.
        let code: Int32 = await withCheckedContinuation { cont in
            p.terminationHandler = { proc in cont.resume(returning: proc.terminationStatus) }
            do { try p.run() } catch {
                cont.resume(returning: 127)
                Task { @MainActor in self.status = "\(error)" }
            }
        }
        errPipe.fileHandleForReading.readabilityHandler = nil
        let stdout = String(decoding: outPipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        process = nil
        let ok = code == 0
        let summary = ok
            ? (stdout.split(separator: "\n").last.map(String.init) ?? "done")
            : (code == 2 || status.contains("interrupt") ? "cancelled" : status)
        finish(url, name: name, ok: ok, summary: summary)
        req.onDone?(ok && chosen != nil)
        next()
    }

    /// Starts the next queued scan, if any.
    private func next() {
        guard !pending.isEmpty else { return }
        let req = pending.removeFirst()
        queue = pending.map(\.url)
        start(req)
    }

    private func finish(_ url: URL, name: String, ok: Bool, summary: String) {
        recent.insert(ScanRecord(path: url.path, name: name, finished: Date(), ok: ok, summary: summary), at: 0)
        if recent.count > 10 { recent.removeLast() }
        current = nil
        currentURL = nil
        status = ""
        onFinished?(url, ok)
        NotificationCenter.default.post(name: .scanFinished, object: nil)
        let content = UNMutableNotificationContent()
        content.title = ok ? "Scanned \(name)" : "Scan of \(name) failed"
        content.body = summary
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil))
    }
}
