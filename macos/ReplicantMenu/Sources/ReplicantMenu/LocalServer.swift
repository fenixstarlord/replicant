import AppKit
import Combine
import Foundation

/// Standalone mode: the app bundle also carries `replicant-server`, and this
/// runs it on the loopback interface with authentication off, points the
/// bundled CLI at it, and stops it on quit. The catalog lives in
/// ~/Library/Application Support/Replicant.
@MainActor
final class LocalServer: ObservableObject {
    static let port = 8787
    static var url: String { "http://127.0.0.1:\(port)" }

    /// True when this bundle was built as the standalone app.
    static var isStandalone: Bool { serverURL != nil }

    static var serverURL: URL? {
        guard Bundle.main.object(forInfoDictionaryKey: "ReplicantStandalone") as? Bool == true,
              let u = Bundle.main.url(forAuxiliaryExecutable: "replicant-server"),
              FileManager.default.isExecutableFile(atPath: u.path) else { return nil }
        return u
    }

    static var dataDir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return base.appendingPathComponent("Replicant", isDirectory: true)
    }

    @Published private(set) var running = false
    @Published private(set) var status = "Starting the catalog…"
    private var process: Process?
    private var observer: NSObjectProtocol?

    init() {
        guard let exe = Self.serverURL else { return }
        try? FileManager.default.createDirectory(at: Self.dataDir, withIntermediateDirectories: true)
        // The CLI reads its config from the data dir too, so a remote
        // setup in ~/.config/replicant is left alone.
        ReplicantCLI.environment["REPLICANT_CONFIG"] = Self.dataDir.appendingPathComponent("config.toml").path
        // Stop the server on quit, synchronously: the process is about to exit.
        observer = NotificationCenter.default.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.stop() }
        }
        start(exe)
    }

    private func start(_ exe: URL) {
        let p = Process()
        p.executableURL = exe
        p.arguments = ["serve"]
        var env = ProcessInfo.processInfo.environment
        env["REPLICANT_DATA_DIR"] = Self.dataDir.path
        env["REPLICANT_LISTEN"] = "127.0.0.1:\(Self.port)"
        env["REPLICANT_AUTH"] = "open"
        env["REPLICANT_PUBLIC_URL"] = Self.url
        env["REPLICANT_EXIT_WITH_PARENT"] = "1"
        // Homebrew tools (ffprobe) are not on a GUI app's PATH.
        env["PATH"] = (env["PATH"] ?? "/usr/bin:/bin") + ":/opt/homebrew/bin:/usr/local/bin"
        p.environment = env
        let log = Pipe()
        p.standardOutput = log
        p.standardError = log
        log.fileHandleForReading.readabilityHandler = { [weak self] h in
            let chunk = String(decoding: h.availableData, as: UTF8.self)
            if let line = chunk.split(separator: "\n").last(where: { !$0.isEmpty }) {
                Task { @MainActor in self?.status = String(line) }
            }
        }
        p.terminationHandler = { [weak self] proc in
            Task { @MainActor in
                self?.running = false
                self?.status = "Catalog stopped (exit \(proc.terminationStatus))"
            }
        }
        do {
            try p.run()
            process = p
        } catch {
            status = "Could not start the catalog: \(error.localizedDescription)"
            return
        }
        Task { await waitUntilUp() }
    }

    /// Polls /healthz, then logs the CLI in (no key needed in open mode).
    private func waitUntilUp() async {
        guard let url = URL(string: Self.url + "/healthz") else { return }
        for _ in 0..<50 {
            if let (_, r) = try? await URLSession.shared.data(from: url), (r as? HTTPURLResponse)?.statusCode == 200 {
                running = true
                status = "Catalog running at \(Self.url)"
                let (code, out) = await ReplicantCLI.run(["login", Self.url])
                if code != 0 { status = "Catalog running, but the scanner could not connect: \(out)" }
                NotificationCenter.default.post(name: .localServerReady, object: nil)
                return
            }
            try? await Task.sleep(for: .milliseconds(200))
        }
        status = "The catalog did not start; check \(Self.dataDir.path)"
    }

    /// Shows the catalog window.
    func open() { CatalogWindow.shared.show() }

    func openInBrowser() {
        NSWorkspace.shared.open(URL(string: Self.url)!)
    }

    func stop() {
        guard let p = process, p.isRunning else { return }
        p.interrupt()
        // Give it a moment to close the database cleanly, then insist.
        let deadline = Date().addingTimeInterval(3)
        while p.isRunning && Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        if p.isRunning { p.terminate() }
        process = nil
    }
}

extension Notification.Name {
    static let localServerReady = Notification.Name("ReplicantLocalServerReady")
}
