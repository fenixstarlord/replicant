import AppKit
import Foundation

/// Installs a vendor command-line package the user downloaded: copies it
/// to Applications and clears the download quarantine so it can run.
enum ToolInstaller {
    struct Failure: LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    /// Where packages go: /Applications when writable, else ~/Applications.
    static func installRoot() -> URL {
        let apps = URL(fileURLWithPath: "/Applications")
        if FileManager.default.isWritableFile(atPath: apps.path) { return apps }
        let home = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Applications")
        try? FileManager.default.createDirectory(at: home, withIntermediateDirectories: true)
        return home
    }

    /// Asks for the downloaded ARRI package (the unzipped folder or the zip).
    @MainActor
    static func chooseARRIPackage() -> URL? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = true
        panel.allowedContentTypes = [.zip, .folder]
        panel.allowsMultipleSelection = false
        panel.prompt = "Install"
        panel.message = "Choose the ARRI Reference Tool CMD download: the unzipped folder (containing bin and lib) or the zip itself."
        panel.directoryURL = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first
        NSApp.activate(ignoringOtherApps: true)
        return panel.runModal() == .OK ? panel.url : nil
    }

    /// Installs the ARRI command-line package from a folder or zip and
    /// returns the installed binary path.
    static func installARRI(from source: URL) throws -> URL {
        let fm = FileManager.default
        var pkg = source
        var temp: URL? = nil
        if source.pathExtension.lowercased() == "zip" {
            let dir = fm.temporaryDirectory.appendingPathComponent("replicant-art-\(UUID().uuidString)")
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            temp = dir
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/usr/bin/ditto")
            p.arguments = ["-xk", source.path, dir.path]
            try p.run()
            p.waitUntilExit()
            guard p.terminationStatus == 0 else { throw Failure(message: "Could not unzip \(source.lastPathComponent).") }
            pkg = dir
        }
        defer { if let t = temp { try? fm.removeItem(at: t) } }

        guard let root = findPackageRoot(under: pkg) else {
            throw Failure(message: "That doesn't look like the ARRI Reference Tool CMD package: no bin/art-cmd and lib/ inside.")
        }
        let dest = installRoot().appendingPathComponent("ARRI Reference Tool CMD")
        if fm.fileExists(atPath: dest.path) { try fm.removeItem(at: dest) }
        try fm.copyItem(at: root, to: dest)
        Quarantine.clear(root: dest)
        let bin = dest.appendingPathComponent("bin/art-cmd")
        try fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: bin.path)
        return bin
    }

    /// Finds the folder containing bin/art-cmd and lib/, up to two levels down.
    static func findPackageRoot(under url: URL) -> URL? {
        let fm = FileManager.default
        func ok(_ u: URL) -> Bool {
            fm.isExecutableFile(atPath: u.appendingPathComponent("bin/art-cmd").path) &&
            fm.fileExists(atPath: u.appendingPathComponent("lib").path)
        }
        if ok(url) { return url }
        for level in [url] + ((try? fm.contentsOfDirectory(at: url, includingPropertiesForKeys: nil)) ?? []) {
            if ok(level) { return level }
            for sub in (try? fm.contentsOfDirectory(at: level, includingPropertiesForKeys: nil)) ?? [] where ok(sub) { return sub }
        }
        return nil
    }
}
