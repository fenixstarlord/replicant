import Foundation

/// Clears macOS's download quarantine flag from a tool package, so a
/// vendor's signed but un-notarized command-line tool can load its own
/// libraries. Only applied to the folder the user installed the tool in.
enum Quarantine {
    static let attribute = "com.apple.quarantine"

    /// The package root for a tool binary: `<root>/bin/art-cmd` -> `<root>`.
    static func packageRoot(forBinary path: String) -> URL {
        let bin = URL(fileURLWithPath: path)
        let parent = bin.deletingLastPathComponent()
        return parent.lastPathComponent == "bin" ? parent.deletingLastPathComponent() : parent
    }

    static func isQuarantined(_ path: String) -> Bool {
        getxattr(path, attribute, nil, 0, 0, 0) >= 0
    }

    /// Removes the flag from every file under root. Returns how many were cleared.
    @discardableResult
    static func clear(root: URL) -> Int {
        var cleared = 0
        let fm = FileManager.default
        var paths = [root.path]
        if let e = fm.enumerator(at: root, includingPropertiesForKeys: nil) {
            for case let u as URL in e { paths.append(u.path) }
        }
        for p in paths where isQuarantined(p) {
            if removexattr(p, attribute, 0) == 0 { cleared += 1 }
        }
        return cleared
    }
}
