import AppKit
import Combine
import Foundation

/// A mounted volume as shown in the menu.
struct Volume: Identifiable, Hashable {
    let url: URL
    let name: String
    let isInternal: Bool
    let isRoot: Bool
    var uuid: String? = nil
    var id: String { url.path }
    /// Identifies the drive across mounts on this Mac: its volume UUID, or its
    /// name when the filesystem has none.
    var stableKey: String { Self.stableKey(uuid: uuid, name: name) }

    static func stableKey(uuid: String?, name: String) -> String { uuid.map { "uuid:" + $0 } ?? "name:" + name }

    /// The stableKey of the volume mounted at url right now, if any.
    static func stableKey(of url: URL) -> String? {
        guard let v = try? url.resourceValues(forKeys: [.volumeUUIDStringKey, .volumeNameKey]) else { return nil }
        return stableKey(uuid: v.volumeUUIDString, name: v.volumeName ?? url.lastPathComponent)
    }
}

/// Watches the volumes mounted on this Mac.
@MainActor
final class VolumeMonitor: ObservableObject {
    @Published private(set) var volumes: [Volume] = []
    private var observers: [NSObjectProtocol] = []

    init() {
        refresh()
        let center = NSWorkspace.shared.notificationCenter
        for name in [NSWorkspace.didMountNotification, NSWorkspace.didUnmountNotification, NSWorkspace.didRenameVolumeNotification] {
            observers.append(center.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
                Task { @MainActor in self?.refresh() }
            })
        }
    }

    deinit {
        let center = NSWorkspace.shared.notificationCenter
        observers.forEach { center.removeObserver($0) }
    }

    func refresh() {
        let keys: [URLResourceKey] = [.volumeNameKey, .volumeIsInternalKey, .volumeIsRootFileSystemKey, .volumeIsBrowsableKey, .volumeUUIDStringKey]
        let urls = FileManager.default.mountedVolumeURLs(includingResourceValuesForKeys: keys, options: [.skipHiddenVolumes]) ?? []
        var out: [Volume] = []
        for url in urls {
            guard let values = try? url.resourceValues(forKeys: Set(keys)) else { continue }
            if values.volumeIsBrowsable == false { continue }
            let name = values.volumeName ?? url.lastPathComponent
            out.append(Volume(url: url, name: name, isInternal: values.volumeIsInternal ?? false, isRoot: values.volumeIsRootFileSystem ?? false,
                              uuid: values.volumeUUIDString))
        }
        volumes = out.sorted { a, b in
            if a.isRoot != b.isRoot { return !a.isRoot }
            return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
        }
    }
}
