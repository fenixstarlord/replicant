import AppKit
import SwiftUI
import WebKit

/// Owns the standalone app's main window: the catalog web UI, served by
/// the bundled server on the loopback interface, in a native WebKit view
/// so it behaves like an app rather than a browser tab.
@MainActor
final class CatalogWindow: NSObject, NSWindowDelegate {
    static let shared = CatalogWindow()
    /// Set by the App so the window can be (re)built with the shared objects.
    var content: (() -> AnyView)?
    private var window: NSWindow?

    func show() {
        guard LocalServer.isStandalone, let content else { return }
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
                             styleMask: [.titled, .closable, .miniaturizable, .resizable],
                             backing: .buffered, defer: false)
            w.title = "Replicant"
            w.isReleasedWhenClosed = false
            w.delegate = self
            w.tabbingMode = .disallowed
            let host = NSHostingController(rootView: content())
            host.sizingOptions = []
            w.contentViewController = host
            w.minSize = NSSize(width: 900, height: 600)
            if !w.setFrameUsingName("ReplicantCatalog") {
                // The hosting view has no intrinsic size; size the window ourselves.
                w.setContentSize(NSSize(width: 1280, height: 820))
                w.center()
            }
            w.setFrameAutosaveName("ReplicantCatalog")
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}

/// The catalog page with a small header: back, forward, reload, scan.
struct CatalogView: View {
    @EnvironmentObject var local: LocalServer
    @EnvironmentObject var volumes: VolumeMonitor
    @EnvironmentObject var scans: ScanManager
    @EnvironmentObject var settings: AppSettings
    @StateObject private var web = WebController()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if local.running {
                WebView(controller: web)
                    .onAppear { web.load(LocalServer.url) }
            } else {
                VStack(spacing: 12) {
                    ProgressView()
                    Text(local.status).foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: .scanFinished)) { _ in web.reload() }
        .onChange(of: local.running) { _, up in if up { web.load(LocalServer.url) } }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button { web.goBack() } label: { Image(systemName: "chevron.left") }.disabled(!web.canGoBack)
            Button { web.goForward() } label: { Image(systemName: "chevron.right") }.disabled(!web.canGoForward)
            Button { web.reload() } label: { Image(systemName: "arrow.clockwise") }
            Spacer()
            if let name = scans.current {
                ProgressView().controlSize(.small)
                Text("Scanning \(name)").font(.callout)
                Text(scans.status).font(.caption).foregroundStyle(.secondary).lineLimit(1).frame(maxWidth: 360, alignment: .trailing)
                Button("Cancel") { scans.cancel() }.controlSize(.small)
            }
            Menu {
                let visible = volumes.volumes.filter { !settings.isIgnored($0) }
                ForEach(visible) { v in
                    Button(v.name) { scans.scan(v.url) }.disabled(scans.current == v.name)
                }
                if visible.isEmpty { Text("No drives") }
                Divider()
                Button("Browse…") { browse() }
            } label: {
                Label("Scan a drive", systemImage: "externaldrive.badge.plus")
            }
            .fixedSize()
        }
        .buttonStyle(.accessoryBar)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(.bar)
    }

    private func browse() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.prompt = "Scan"
        panel.message = "Choose a drive, network share, or folder to scan."
        if panel.runModal() == .OK, let url = panel.url { scans.scan(url) }
    }
}

/// Owns the WKWebView: navigation state, downloads (CSV/ALE exports go
/// to ~/Downloads), and external links (open in the default browser).
@MainActor
final class WebController: NSObject, ObservableObject, WKNavigationDelegate, WKDownloadDelegate, WKUIDelegate {
    @Published var canGoBack = false
    @Published var canGoForward = false
    let webView: WKWebView

    override init() {
        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .default()
        webView = WKWebView(frame: .zero, configuration: cfg)
        super.init()
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.allowsBackForwardNavigationGestures = true
    }

    func load(_ url: String) {
        guard let u = URL(string: url) else { return }
        if webView.url?.host == u.host { return }
        webView.load(URLRequest(url: u))
    }

    func goBack() { webView.goBack() }
    func goForward() { webView.goForward() }
    func reload() { webView.reload() }

    private func sync() {
        canGoBack = webView.canGoBack
        canGoForward = webView.canGoForward
    }

    nonisolated func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        Task { @MainActor in sync() }
    }

    nonisolated func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { decisionHandler(.allow); return }
        let local = url.host == "127.0.0.1" || url.host == "localhost"
        if local || url.scheme == "about" {
            decisionHandler(.allow)
        } else {
            // Vendor download pages, docs links: hand to the browser.
            Task { @MainActor in NSWorkspace.shared.open(url) }
            decisionHandler(.cancel)
        }
    }

    nonisolated func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse, decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        if response.canShowMIMEType { decisionHandler(.allow) } else { decisionHandler(.download) }
    }

    nonisolated func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        Task { @MainActor in download.delegate = self }
    }

    nonisolated func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let dir = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first ?? FileManager.default.temporaryDirectory
        var dest = dir.appendingPathComponent(suggestedFilename)
        var n = 1
        let stem = dest.deletingPathExtension().lastPathComponent, ext = dest.pathExtension
        while FileManager.default.fileExists(atPath: dest.path) {
            n += 1
            dest = dir.appendingPathComponent("\(stem) \(n)").appendingPathExtension(ext)
        }
        completionHandler(dest)
    }

    nonisolated func downloadDidFinish(_ download: WKDownload) {
        Task { @MainActor in
            if let p = download.progress.fileURL { NSWorkspace.shared.activateFileViewerSelecting([p]) }
        }
    }

    // Links with target=_blank open in the same view.
    nonisolated func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for action: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let url = action.request.url { Task { @MainActor in webView.load(URLRequest(url: url)) } }
        return nil
    }

    // confirm() dialogs from the UI (revoke, remove group).
    nonisolated func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        Task { @MainActor in
            let a = NSAlert()
            a.messageText = message
            a.addButton(withTitle: "OK")
            a.addButton(withTitle: "Cancel")
            completionHandler(a.runModal() == .alertFirstButtonReturn)
        }
    }

    nonisolated func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        Task { @MainActor in
            let a = NSAlert()
            a.messageText = message
            a.runModal()
            completionHandler()
        }
    }
}

struct WebView: NSViewRepresentable {
    let controller: WebController
    func makeNSView(context: Context) -> WKWebView { controller.webView }
    func updateNSView(_ view: WKWebView, context: Context) {}
}

/// Opens the main window at launch and when the Dock icon is clicked.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        MainActor.assumeIsolated { CatalogWindow.shared.show() }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        MainActor.assumeIsolated { CatalogWindow.shared.show() }
        return false
    }
}

extension Notification.Name {
    static let scanFinished = Notification.Name("ReplicantScanFinished")
}
