import SwiftUI

struct SettingsView: View {
    @EnvironmentObject var volumes: VolumeMonitor
    @EnvironmentObject var settings: AppSettings
    @EnvironmentObject var toolsCheck: ToolChecker
    @State private var server = ""
    @State private var token = ""
    @State private var message = ""
    @State private var busy = false
    @State private var newIgnore = ""

    var body: some View {
        Form {
            Section("Server") {
                TextField("Server URL", text: $server, prompt: Text("http://shelf.netbird.cloud:8080"))
                SecureField("API key", text: $token, prompt: Text(settings.hasToken ? "saved (leave blank to keep)" : "shelf_…"))
                HStack {
                    Button(busy ? "Checking…" : "Save and test") { Task { await save() } }
                        .disabled(busy || server.isEmpty)
                    if !message.isEmpty { Text(message).font(.caption).foregroundStyle(message.hasPrefix("OK") ? .green : .red) }
                }
                Text("Create a key on the server under API keys. Settings are stored in ~/.config/shelf/config.toml, shared with the shelf command.")
                    .font(.caption).foregroundStyle(.secondary)
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
        .onAppear { server = settings.server }
    }

    private func save() async {
        busy = true
        defer { busy = false }
        var args = ["login", server.trimmingCharacters(in: .whitespaces)]
        var stdin: String? = nil
        if !token.isEmpty { stdin = token + "\n" } else {
            // Keep the saved token: pass it back through stdin by reading the config.
            if let saved = savedToken() { stdin = saved + "\n" } else { message = "Enter the API key"; return }
        }
        _ = args
        let (code, out) = await ShelfCLI.run(args, stdin: stdin)
        settings.reload()
        message = code == 0 ? "OK: " + out.trimmingCharacters(in: .whitespacesAndNewlines) : out.trimmingCharacters(in: .whitespacesAndNewlines)
        if code == 0 { token = "" }
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
