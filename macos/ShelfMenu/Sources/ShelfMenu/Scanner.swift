import AppKit
import Foundation
import UserNotifications

/// Finds the bundled `shelf` CLI.
enum ShelfCLI {
    static var url: URL? {
        if let u = Bundle.main.url(forAuxiliaryExecutable: "shelf"), FileManager.default.isExecutableFile(atPath: u.path) { return u }
        let exe = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        // Development: swift run from macos/ShelfMenu with the Go binary in ../../bin/shelf.
        let candidates = [
            exe.deletingLastPathComponent().appendingPathComponent("shelf"),
            exe.deletingLastPathComponent().appendingPathComponent("../../../../bin/shelf").standardized,
            URL(fileURLWithPath: "/usr/local/bin/shelf"),
            URL(fileURLWithPath: "/opt/homebrew/bin/shelf"),
        ]
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0.path) }
    }

    /// Runs the CLI to completion, returning exit code and combined output.
    static func run(_ args: [String], stdin: String? = nil) async -> (Int32, String) {
        guard let url else { return (127, "shelf CLI not found") }
        return await withCheckedContinuation { cont in
            let p = Process()
            p.executableURL = url
            p.arguments = args
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

/// Runs scans one at a time through `shelf scan <path>` and reports progress.
@MainActor
final class ScanManager: ObservableObject {
    @Published private(set) var current: String? = nil      // display name
    @Published private(set) var status: String = ""          // last progress line
    @Published private(set) var queue: [URL] = []
    @Published private(set) var recent: [ScanRecord] = []
    private var process: Process?

    var isScanning: Bool { current != nil }

    init() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    func scan(_ url: URL) {
        if current != nil || queue.contains(url) {
            if !queue.contains(url) { queue.append(url) }
            return
        }
        Task { await run(url) }
    }

    func cancel() {
        process?.interrupt()
    }

    private func run(_ url: URL) async {
        let name = url.lastPathComponent
        current = name
        status = "Starting…"
        guard let cli = ShelfCLI.url else {
            finish(url, name: name, ok: false, summary: "shelf CLI not found")
            return
        }
        let p = Process()
        p.executableURL = cli
        p.arguments = ["scan", url.path]
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
        if let next = queue.first {
            queue.removeFirst()
            await run(next)
        }
    }

    private func finish(_ url: URL, name: String, ok: Bool, summary: String) {
        recent.insert(ScanRecord(path: url.path, name: name, finished: Date(), ok: ok, summary: summary), at: 0)
        if recent.count > 10 { recent.removeLast() }
        current = nil
        status = ""
        let content = UNMutableNotificationContent()
        content.title = ok ? "Scanned \(name)" : "Scan of \(name) failed"
        content.body = summary
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil))
    }
}
