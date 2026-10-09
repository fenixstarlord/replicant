import AppKit
import SwiftUI

/// Owns the Settings window. A menu-bar-only app is never the active app,
/// so the standard SwiftUI settings action does nothing; this window is
/// created once, activated, and brought to the front explicitly.
@MainActor
final class SettingsWindow {
    static let shared = SettingsWindow()
    private var window: NSWindow?

    func show<Content: View>(_ content: @autoclosure () -> Content) {
        if window == nil {
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 680),
                             styleMask: [.titled, .closable, .miniaturizable, .resizable],
                             backing: .buffered, defer: false)
            w.title = "Shelf Settings"
            w.isReleasedWhenClosed = false
            let host = NSHostingController(rootView: content().frame(minWidth: 480, minHeight: 600))
            host.sizingOptions = [] // keep the window size we set, not the view's intrinsic size
            w.contentViewController = host
            w.setContentSize(NSSize(width: 500, height: 680))
            w.minSize = NSSize(width: 480, height: 500)
            w.center()
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}
