import AppKit
import Combine
import Foundation
import SwiftUI

struct DriveProfile: Codable, Identifiable {
    var key: String
    var name = ""
    var volume_name = ""
    var group_id: Int64 = 0
    var client_id: Int64 = 0
    var fingerprint = true
    var extractors = Self.extractorChoices.map(\.id)
    var configured = false
    var indexed = false
    var revision: Int64 = 0
    var new_group: String? = nil
    var new_client: String? = nil
    var id: String { key }

    static let extractorChoices: [(id: String, title: String)] = [
        ("ffprobe", "ffprobe — video and audio"), ("art-cmd", "ARRI ART — camera metadata"),
        ("redline", "REDline — RED camera metadata"), ("ale", "ALE logs"),
        ("sony-xml", "Sony XML sidecars"), ("braw-sidecar", "BRAW sidecars"), ("bwf", "WAV/BWF audio metadata")
    ]

    func displayName(mountedName: String) -> String {
        name.isEmpty || name == mountedName ? mountedName : "\(name) (\(mountedName))"
    }
}

struct ProfileTaxonomy: Codable, Identifiable {
    let id: Int64
    let name: String
}

struct ProfileCatalog: Codable {
    var profiles: [DriveProfile] = []
    var groups: [ProfileTaxonomy] = []
    var clients: [ProfileTaxonomy] = []
}

private struct VolumeIdentity: Decodable {
    let key: String
    let volume_name: String
}

/// Profiles are server-owned. Cache is scoped to the server, and mount identity
/// is session-only so a different tape at the same path cannot inherit settings.
@MainActor
final class DriveProfiles: ObservableObject {
    @Published private(set) var catalog = ProfileCatalog()
    @Published private(set) var keys: [String: String] = [:]
    @Published private(set) var message = ""
    @Published private(set) var refreshing = false
    private let settings: AppSettings
    private let scans: ScanManager
    private let volumes: VolumeMonitor
    private var server = ""
    private var asking = Set<String>()
    private var windows: [String: NSWindow] = [:]
    private var observers: [NSObjectProtocol] = []
    private var timer: Timer?
    private var refreshTask: Task<Void, Never>?
    private var autoPrompted = Set<String>()

    init(settings: AppSettings, scans: ScanManager, volumes: VolumeMonitor) {
        self.settings = settings
        self.scans = scans
        self.volumes = volumes
        observers.append(NotificationCenter.default.addObserver(forName: .scanFinished, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        })
        let center = NSWorkspace.shared.notificationCenter
        for event in [NSWorkspace.didMountNotification, NSWorkspace.didUnmountNotification] {
            observers.append(center.addObserver(forName: event, object: nil, queue: .main) { [weak self] note in
                let path = (note.userInfo?[NSWorkspace.volumeURLUserInfoKey] as? URL)?.path
                Task { @MainActor in
                    if let path { self?.keys[path] = nil; self?.autoPrompted.remove(path) }
                    await self?.refresh()
                }
            })
        }
        timer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
        Task { await refresh() }
    }

    deinit {
        timer?.invalidate()
        for o in observers {
            NotificationCenter.default.removeObserver(o)
            NSWorkspace.shared.notificationCenter.removeObserver(o)
        }
    }

    private var cacheKey: String { "driveProfiles:" + server }

    func profile(for v: Volume) -> DriveProfile? {
        let key = keys[v.url.path] ?? v.uuid
        return catalog.profiles.first { $0.key == key }
    }

    func label(for v: Volume) -> String { profile(for: v)?.displayName(mountedName: v.name) ?? v.name }

    func refresh() async {
        if let task = refreshTask { await task.value; return }
        let task = Task { await reloadCatalog() }
        refreshTask = task
        await task.value
        refreshTask = nil
    }

    private func reloadCatalog() async {
        refreshing = true
        defer { refreshing = false }
        settings.reload()
        if server != settings.server {
            server = settings.server
            catalog = ProfileCatalog()
            if let data = UserDefaults.standard.data(forKey: cacheKey),
               let cached = try? JSONDecoder().decode(ProfileCatalog.self, from: data) { catalog = cached }
        }
        guard !server.isEmpty else { return }
        let requestedServer = server
        let (code, out) = await ReplicantCLI.run(["profiles"])
        guard requestedServer == settings.server else { return }
        if code == 0, let fresh = try? JSONDecoder().decode(ProfileCatalog.self, from: Data(out.utf8)) {
            catalog = fresh
            message = ""
            cache()
        } else {
            message = "Shared settings unavailable. Check the connection and update the server."
        }
        // Avoid volume queries during an active tape read; LTFS can serialize
        // even metadata calls behind a long locate. Previously resolved keys work.
        guard !scans.isScanning else { return }
        for v in volumes.volumes where keys[v.url.path] == nil && !settings.isIgnored(v) {
            if scans.isScanning { break }
            if let identity = await identify(v.url), volumes.volumes.contains(where: { $0 == v }) {
                keys[v.url.path] = identity.key
            }
        }
    }

    private func cache() {
        if let data = try? JSONEncoder().encode(catalog) { UserDefaults.standard.set(data, forKey: cacheKey) }
    }

    private func identify(_ url: URL) async -> VolumeIdentity? {
        let (code, out) = await ReplicantCLI.run(["drive", "--json", "--identity-only", url.path], timeout: 20)
        guard code == 0 else { return nil }
        return try? JSONDecoder().decode(VolumeIdentity.self, from: Data(out.utf8))
    }

    func requestScan(_ url: URL, edit: Bool = false, automatic: Bool = false) {
        if automatic && autoPrompted.contains(url.path) { return }
        guard !asking.contains(url.path) else {
            windows[url.path]?.makeKeyAndOrderFront(nil)
            return
        }
        asking.insert(url.path)
        Task {
            await refresh()
            let knownKey = keys[url.path]
            // Editing a running drive needs no filesystem access. The queued
            // scan independently verifies the volume key before using settings.
            let identity: VolumeIdentity?
            if edit, let key = knownKey {
                identity = VolumeIdentity(key: key, volume_name: url.lastPathComponent)
            } else { identity = await identify(url) }
            guard let identity else {
                message = "Could not identify \(url.lastPathComponent). Try again when the drive is ready."
                asking.remove(url.path)
                return
            }
            keys[url.path] = identity.key
            let v = volumes.volumes.first { $0.url == url }
            let mountedName = v?.name ?? url.lastPathComponent
            var profile = catalog.profiles.first { $0.key == identity.key }
            if profile == nil {
                guard message.isEmpty else { asking.remove(url.path); return }
                profile = DriveProfile(key: identity.key, name: identity.volume_name, volume_name: identity.volume_name)
            }
            guard let profile else { asking.remove(url.path); return }
            if !edit && (profile.configured || profile.indexed) {
                asking.remove(url.path)
                enqueue(url, profile: profile, mountedName: mountedName, automatic: automatic)
                return
            }
            if automatic { autoPrompted.insert(url.path) }
            showEditor(url, profile: profile, mountedName: mountedName, scanAfterSave: !edit, automatic: automatic)
        }
    }

    private func enqueue(_ url: URL, profile: DriveProfile, mountedName: String, automatic: Bool) {
        scans.scan(ScanRequest(url: url, automatic: automatic, profileKey: profile.key,
                               displayName: profile.displayName(mountedName: mountedName)))
    }

    private func showEditor(_ url: URL, profile: DriveProfile, mountedName: String, scanAfterSave: Bool, automatic: Bool) {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 620),
                              styleMask: [.titled], backing: .buffered, defer: false)
        window.title = "Scan settings — \(mountedName)"
        window.isReleasedWhenClosed = false
        let targetServer = server
        let close = { [weak self, weak window] in
            window?.close()
            self?.windows[url.path] = nil
            self?.asking.remove(url.path)
        }
        window.contentView = NSHostingView(rootView: DriveProfileEditor(profile: profile, catalog: catalog,
            scanAfterSave: scanAfterSave, cancel: close, save: { [weak self] updated in
                guard let self else { return "Settings window closed" }
                self.settings.reload()
                guard self.settings.server == targetServer else { return "Server changed. Close and reopen drive settings." }
                guard let data = try? JSONEncoder().encode(updated), let input = String(data: data, encoding: .utf8) else { return "Could not encode settings" }
                let (code, out) = await ReplicantCLI.run(["profiles", "save"], stdin: input)
                guard code == 0, let saved = try? JSONDecoder().decode(DriveProfile.self, from: Data(out.utf8)) else { return out.isEmpty ? "Could not save settings" : out }
                self.catalog.profiles.removeAll { $0.key == saved.key }
                self.catalog.profiles.append(saved)
                self.cache()
                close()
                if scanAfterSave {
                    self.autoPrompted.remove(url.path)
                    self.enqueue(url, profile: saved, mountedName: mountedName, automatic: automatic)
                }
                return nil
            }))
        windows[url.path] = window
        window.center()
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
    }
}

private struct DriveProfileEditor: View {
    @State var profile: DriveProfile
    let catalog: ProfileCatalog
    let scanAfterSave: Bool
    let cancel: () -> Void
    let save: (DriveProfile) async -> String?
    @StateObject private var tools = ToolChecker()
    @State private var busy = false
    @State private var error = ""
    @State private var newGroup = ""
    @State private var newClient = ""

    var body: some View {
      ScrollView {
        VStack(alignment: .leading, spacing: 14) {
            Text("Drive scan settings").font(.headline)
            Text("Saved on the server for this drive on every Mac.").font(.caption).foregroundStyle(.secondary)
            TextField("Catalog name", text: $profile.name)
            Picker("Client", selection: $profile.client_id) {
                Text("None").tag(Int64(0))
                ForEach(catalog.clients) { Text($0.name).tag($0.id) }
                Text("New client…").tag(Int64(-1))
            }
            if profile.client_id == -1 { TextField("New client name", text: $newClient) }
            Picker("Group", selection: $profile.group_id) {
                Text("None").tag(Int64(0))
                ForEach(catalog.groups) { Text($0.name).tag($0.id) }
                Text("New group…").tag(Int64(-1))
            }
            if profile.group_id == -1 { TextField("New group name", text: $newGroup) }
            Divider()
            Text("Metadata extraction").font(.subheadline.bold())
            ForEach(DriveProfile.extractorChoices, id: \.id) { choice in
                HStack {
                    Toggle(choice.title, isOn: Binding(get: { profile.extractors.contains(choice.id) }, set: { on in
                        profile.extractors.removeAll { $0 == choice.id }
                        if on { profile.extractors.append(choice.id) }
                    }))
                    if tools.tools.contains(where: { $0.name == choice.id && !$0.available }) {
                        Text("Unavailable here").font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            Divider()
            Toggle("File fingerprints (duplicate matching)", isOn: $profile.fingerprint)
            Text("Fingerprints read file contents and can take much longer on tape. Files are cataloged even with all options off.")
                .font(.caption).foregroundStyle(.secondary)
            if !error.isEmpty { Text(error).font(.caption).foregroundStyle(.red).textSelection(.enabled) }
            HStack {
                Spacer()
                Button("Cancel", action: cancel).keyboardShortcut(.cancelAction)
                Button(scanAfterSave ? "Save and scan" : "Save") {
                    var updated = profile
                    if updated.group_id == -1 { updated.group_id = 0; updated.new_group = newGroup.trimmingCharacters(in: .whitespacesAndNewlines) }
                    if updated.client_id == -1 { updated.client_id = 0; updated.new_client = newClient.trimmingCharacters(in: .whitespacesAndNewlines) }
                    busy = true
                    Task { error = await save(updated) ?? ""; busy = false }
                }.keyboardShortcut(.defaultAction)
                .disabled((profile.group_id == -1 && newGroup.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty) ||
                          (profile.client_id == -1 && newClient.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty))
            }
        }
        .padding(20).disabled(busy)
      }.frame(width: 460, height: 620)
    }
}
