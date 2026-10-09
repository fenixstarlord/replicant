import AppKit
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var volumes: VolumeMonitor
    @EnvironmentObject var settings: AppSettings
    @EnvironmentObject var toolsCheck: ToolChecker
    @EnvironmentObject var autoScan: AutoScanner
    @EnvironmentObject var local: LocalServer
    @State private var connection = ""
    @State private var message = ""
    @State private var busy = false
    @State private var newIgnore = ""

    var body: some View {
        Form {
            if LocalServer.isStandalone {
                Section("Catalog") {
                    LabeledContent("Built-in server") { Text(local.status).textSelection(.enabled) }
                    LabeledContent("Data") { Text(LocalServer.dataDir.path).textSelection(.enabled) }
                    HStack {
                        Button("Open catalog") { local.open() }.disabled(!local.running)
                        Button("Show data folder") { NSWorkspace.shared.activateFileViewerSelecting([LocalServer.dataDir]) }
                    }
                    Text("This app runs its own catalog on this Mac, no server or API key needed. Scans go straight into it; open the catalog in your browser to search. Back it up by copying the data folder.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            } else {
            Section("Server") {
                if !settings.server.isEmpty {
                    LabeledContent("Connected to") {
                        Text(settings.server).textSelection(.enabled)
                        if !settings.hasToken { Text("(no key saved)").foregroundStyle(.orange) }
                    }
                }
                TextField("Connection key", text: $connection, prompt: Text("shelf://shelf_…@host:8080"))
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { Task { await save() } }
                HStack {
                    Button(busy ? "Checking…" : "Save and test") { Task { await save() } }
                        .disabled(busy || connection.trimmingCharacters(in: .whitespaces).isEmpty)
                    if !message.isEmpty { Text(message).font(.caption).foregroundStyle(message.hasPrefix("OK") ? .green : .red) }
                }
                Text("Create a key on the server under API keys and paste it here; it includes the server address. Settings are stored in ~/.config/shelf/config.toml, shared with the shelf command.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            }
            Section("Ignore these drives") {
                ForEach(volumes.volumes) { v in
                    Toggle(v.name, isOn: Binding(get: { settings.isIgnored(v) }, set: { settings.setIgnored(v.name, $0) }))
                }
                ForEach(Array(settings.ignored.subtracting(volumes.volumes.map(\.name))).sorted(), id: \.self) { name in
                    Toggle("\(name) (not mounted)", isOn: Binding(get: { true }, set: { settings.setIgnored(name, $0) }))
                }
                HStack {
                    TextField("Add a drive name to ignore", text: $newIgnore)
                    Button("Add") { settings.setIgnored(newIgnore.trimmingCharacters(in: .whitespaces), true); newIgnore = "" }
                        .disabled(newIgnore.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            Section("Automatic scans") {
                Toggle("Scan any external drive when it is mounted", isOn: $autoScan.allOnMount)
                Text("Applies to every external drive except ignored ones. Per-drive rules below add interval scans, or on-mount scans for internal drives.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("Per-drive rules") {
                let mounted = Dictionary(volumes.volumes.map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
                let names = autoScan.ruleNames
                if names.isEmpty {
                    Text("No drives yet. Mount one, or scan one so the server knows it.").foregroundStyle(.secondary)
                }
                ForEach(names, id: \.self) { name in
                    let v = mounted[name]
                    AutoScanRow(name: name, path: v?.url.path, isInternal: v?.isInternal ?? false, mounted: v != nil)
                }
                Text("Lists mounted drives, drives in the server's catalog, and drives seen before. On mount: scan when the drive appears. Interval: rescan while it stays mounted, counted from its last scan; a drive never scanned by this app is scanned at the next check. Scans queue one at a time.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("Metadata tools") {
                if !toolsCheck.checked {
                    Text("Checking…").foregroundStyle(.secondary)
                } else if toolsCheck.tools.isEmpty {
                    Text("Could not run the bundled shelf command.").foregroundStyle(.red)
                } else {
                    ForEach(toolsCheck.tools.filter(\.isExternal)) { t in
                        HStack {
                            Image(systemName: t.available ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                                .foregroundStyle(t.available ? .green : .orange)
                            VStack(alignment: .leading) {
                                if t.available {
                                    Text(t.displayName)
                                    Text(t.version ?? "found").font(.caption).foregroundStyle(.secondary)
                                } else if t.isQuarantined {
                                    Text(t.displayName)
                                    Text("Installed, but macOS blocks downloaded command-line tools until they are allowed.").font(.caption).foregroundStyle(.orange)
                                    Button("Allow it to run") { Task { await toolsCheck.allow(t) } }.controlSize(.small)
                                } else if let v = t.version, !v.isEmpty {
                                    Text(t.displayName)
                                    Text(v).font(.caption).foregroundStyle(.orange)
                                } else if t.name == "art-cmd" {
                                    Text(t.displayName)
                                    Text("Not installed · needed for \(t.formats)").font(.caption).foregroundStyle(.secondary)
                                    HStack {
                                        if let url = t.vendorURL { Link("Download", destination: url) }
                                        Button("Install…") { Task { await toolsCheck.installARRI() } }.controlSize(.small)
                                    }
                                    Text("Download the Command-Line package from ARRI, then click Install and choose the download.").font(.caption).foregroundStyle(.secondary)
                                } else if let url = t.vendorURL {
                                    Link(t.displayName, destination: url)
                                    Text("Not installed · needed for \(t.formats) · click to download").font(.caption).foregroundStyle(.secondary)
                                } else {
                                    Text(t.displayName)
                                    Text("Not installed · needed for \(t.formats)").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                        }
                    }
                    Text("Built in: " + toolsCheck.tools.filter { !$0.isExternal }.map(\.formats).joined(separator: ", "))
                        .font(.caption).foregroundStyle(.secondary)
                }
                if !toolsCheck.installMessage.isEmpty { Text(toolsCheck.installMessage).font(.caption) }
                HStack {
                    Button(toolsCheck.checking ? "Checking…" : "↻ Recheck") { Task { await toolsCheck.check() } }.disabled(toolsCheck.checking)
                    Button("Install notes…") { toolsCheck.openDocs() }
                }
            }
            Section {
                Toggle("Launch at login", isOn: Binding(get: { settings.launchAtLogin }, set: { settings.setLaunchAtLogin($0) }))
            }
        }
        .formStyle(.grouped)
        .task { await autoScan.refreshCatalog() }
    }

    private func save() async {
        busy = true
        defer { busy = false }
        let value = connection.trimmingCharacters(in: .whitespacesAndNewlines)
        var stdin: String? = nil
        if !value.contains("@") {
            // A bare server URL: keep the saved key, if there is one.
            guard let saved = savedToken() else { message = "Paste the whole key from the server's API keys page"; return }
            stdin = saved + "\n"
        }
        let (code, out) = await ShelfCLI.run(["login", value], stdin: stdin)
        settings.reload()
        message = code == 0 ? "OK: " + out.trimmingCharacters(in: .whitespacesAndNewlines) : out.trimmingCharacters(in: .whitespacesAndNewlines)
        if code == 0 { connection = "" }
    }

    private func savedToken() -> String? {
        guard let text = try? String(contentsOf: AppSettings.configURL, encoding: .utf8) else { return nil }
        for raw in text.split(separator: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("[") { break }
            if line.hasPrefix("token"), let eq = line.firstIndex(of: "=") {
                var v = line[line.index(after: eq)...].trimmingCharacters(in: .whitespaces)
                if v.hasPrefix("\"") && v.hasSuffix("\"") && v.count >= 2 { v = String(v.dropFirst().dropLast()) }
                return v.isEmpty ? nil : v
            }
        }
        return nil
    }
}

/// One drive's automatic scan rule.
struct AutoScanRow: View {
    @EnvironmentObject var autoScan: AutoScanner
    let name: String
    let path: String?
    let isInternal: Bool
    let mounted: Bool

    var body: some View {
        let rule = autoScan.rule(for: name)
        HStack {
            VStack(alignment: .leading) {
                Text(mounted ? name : "\(name) (not mounted)")
                if let path, let last = autoScan.lastScanned[path] {
                    Text("scanned \(last.formatted(.relative(presentation: .named)))").font(.caption).foregroundStyle(.secondary)
                } else if mounted, rule.intervalHours > 0 {
                    Text("not scanned yet · next check").font(.caption).foregroundStyle(.secondary)
                } else if isInternal {
                    Text("internal").font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
            Toggle("On mount", isOn: Binding(get: { rule.onMount }, set: { var r = rule; r.onMount = $0; autoScan.setRule(r, for: name) }))
                .toggleStyle(.checkbox)
            Picker("", selection: Binding(get: { rule.intervalHours }, set: { var r = rule; r.intervalHours = $0; autoScan.setRule(r, for: name) })) {
                ForEach(AutoScanner.intervals, id: \.hours) { Text($0.label).tag($0.hours) }
            }
            .labelsHidden().frame(width: 150)
        }
    }
}
