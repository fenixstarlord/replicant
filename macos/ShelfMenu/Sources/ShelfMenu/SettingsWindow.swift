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
            let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 480, height: 640),
                             styleMask: [.titled, .closable, .miniaturizable],
                             backing: .buffered, defer: false)
            w.title = "Shelf Settings"
            w.isReleasedWhenClosed = false
            w.contentViewController = NSHostingController(rootView: content())
            w.center()
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}
