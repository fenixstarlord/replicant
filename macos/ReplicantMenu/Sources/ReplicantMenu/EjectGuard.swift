import AppKit
import Darwin
import DiskArbitration
import Foundation

/// Why a running scan was stopped.
enum StopReason: Sendable {
    case user   // Cancel scan
    case eject  // the drive is being ejected
    case quit   // the app is quitting
}

/// The `replicant scan` process the app is running, readable from any
/// thread: DiskArbitration asks about ejects on its own queue, and the app
/// stops the scan from there or while quitting.
final class ActiveScan: @unchecked Sendable {
    static let shared = ActiveScan()

    private let lock = NSLock()
    private var generation = 0
    private var path: String?          // resolved scan root while a scan runs
    private var pid: pid_t = 0         // 0 until the process has started
    private var reading = false        // the CLI may still have files open on the drive
    private var stopPending = false    // asked to stop before the process started
    private var forcePending = false   // grace period expired before attach
    private var exited: DispatchGroup? // left once the process has exited
    private var reason: StopReason?
    /// Volumes being ejected, and when; automatic scans of them are not started.
    private var ejecting: [String: Date] = [:]

    /// Records a scan of path about to start. Returns false, and records
    /// nothing, if the path is on a volume being ejected.
    func begin(path: String) -> Bool {
        lock.lock(); defer { lock.unlock() }
        let p = Self.resolve(path)
        if ejectingLocked(p) { return false }
        generation += 1
        self.path = p
        pid = 0
        reading = true
        stopPending = false
        forcePending = false
        reason = nil
        let g = DispatchGroup()
        g.enter()
        exited = g
        return true
    }

    /// Records the process once it runs, and stops it at once if a stop was
    /// asked for while it was starting.
    func attach(pid: pid_t) {
        lock.lock(); defer { lock.unlock() }
        guard path != nil else { return } // already exited
        self.pid = pid
        if forcePending { kill(pid, SIGKILL) }
        else if stopPending { kill(pid, SIGINT) }
    }

    /// Records that the CLI has closed every file on the drive (it printed
    /// "finished reading"): from now on an eject does not need to stop it.
    func doneReading() {
        lock.lock(); defer { lock.unlock() }
        reading = false
    }

    /// Records that the process exited. Called from its termination handler.
    func ended() {
        lock.lock()
        let g = exited
        exited = nil
        path = nil
        pid = 0
        reading = false
        stopPending = false
        forcePending = false
        lock.unlock()
        g?.leave()
    }

    /// Why the last scan was stopped, if it was.
    var stopReason: StopReason? {
        lock.lock(); defer { lock.unlock() }
        return reason
    }

    /// Whether the running scan may have files open on the volume mounted at
    /// volume: it is still reading, and its root is on that volume or
    /// contains it (a scan of "/" walks into every mounted drive).
    func isReading(onVolume volume: String) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard let path, reading else { return false }
        let v = Self.resolve(volume)
        return Self.contains(v, path) || Self.contains(path, v)
    }

    /// Interrupts immediately, then force-stops this generation after the grace
    /// period. Never blocks the UI; a blocked LTFS read cannot delay escalation.
    func interrupt(_ why: StopReason, timeout: TimeInterval = 3) {
        lock.lock(); defer { lock.unlock() }
        guard path != nil else { return }
        let gen = generation
        if reason == nil { reason = why }
        if pid > 0 { kill(pid, SIGINT) } else { stopPending = true }
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout) { [self] in
            forceStop(generation: gen)
        }
    }

    /// Stops the scan and waits up to timeout for it to exit, then kills it
    /// and its tools. Returns whether no scan is running any more. Safe to
    /// call from several threads at once: every caller is woken on exit.
    @discardableResult
    func stop(_ why: StopReason, timeout: TimeInterval) -> Bool {
        lock.lock()
        guard path != nil, let g = exited else {
            lock.unlock()
            return true
        }
        let gen = generation
        if reason == nil { reason = why }
        if pid > 0 { kill(pid, SIGINT) } else { stopPending = true }
        lock.unlock()
        if g.wait(timeout: .now() + timeout) == .success { return true }
        // The CLI did not stop in time (a read stuck in the kernel, or the
        // process is suspended). Kill its tools, then the CLI, unless it has
        // exited meanwhile and the pid could belong to something else.
        forceStop(generation: gen)
        return g.wait(timeout: .now() + 1) == .success
    }

    private func forceStop(generation gen: Int) {
        lock.lock(); defer { lock.unlock() }
        guard generation == gen, path != nil else { return }
        // attach can race the deadline. Remember escalation even without a PID.
        forcePending = true
        guard pid > 0 else { return }
        for child in Self.descendants(of: pid).reversed() { kill(child, SIGKILL) }
        kill(pid, SIGKILL)
    }

    func markEjecting(_ volume: String) {
        lock.lock(); defer { lock.unlock() }
        ejecting[Self.resolve(volume)] = Date()
    }

    func doneEjecting(_ volume: String) {
        lock.lock(); defer { lock.unlock() }
        ejecting.removeValue(forKey: Self.resolve(volume))
    }

    /// Whether path is on a volume being ejected. A mark lasts at most
    /// 15 s: long enough to cover the unmount itself.
    func isEjecting(_ path: String) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return ejectingLocked(Self.resolve(path))
    }

    /// Forgets eject marks covering path: the user asked to scan it, so the
    /// drive is evidently still there.
    func clearEjecting(covering path: String) {
        lock.lock(); defer { lock.unlock() }
        let p = Self.resolve(path)
        ejecting = ejecting.filter { !Self.contains($0.key, p) }
    }

    private func ejectingLocked(_ p: String) -> Bool {
        ejecting = ejecting.filter { Date().timeIntervalSince($0.value) < 15 }
        return ejecting.keys.contains { Self.contains($0, p) }
    }

    private static func resolve(_ path: String) -> String {
        let p = URL(fileURLWithPath: path).resolvingSymlinksInPath().standardizedFileURL.path
        return p.count > 1 && p.hasSuffix("/") ? String(p.dropLast()) : p
    }

    /// Whether path is volume or inside it.
    private static func contains(_ volume: String, _ path: String) -> Bool {
        path == volume || path.hasPrefix(volume == "/" ? "/" : volume + "/")
    }

    /// Direct children of pid (ffprobe, art-cmd, REDline under the CLI).
    private static func children(of pid: pid_t) -> [pid_t] {
        var buf = [pid_t](repeating: 0, count: 64)
        let n = proc_listchildpids(pid, &buf, Int32(buf.count * MemoryLayout<pid_t>.size))
        return n > 0 ? Array(buf.prefix(Int(n))).filter { $0 > 0 } : []
    }

    private static func descendants(of pid: pid_t) -> [pid_t] {
        var result = children(of: pid)
        var i = 0
        while i < result.count {
            result.append(contentsOf: children(of: result[i]))
            i += 1
        }
        return result
    }
}

extension Notification.Name {
    /// Posted on the main queue with the volume path as object when macOS is
    /// about to unmount a volume.
    static let volumeEjecting = Notification.Name("ReplicantVolumeEjecting")
}

/// Lets a drive that is being scanned be ejected. macOS asks registered
/// apps before it unmounts a volume; when the volume is one a scan is
/// reading, Replicant stops the scan, waits for it to exit, and then lets
/// the eject go ahead. Without this the eject just fails because the scan
/// has files open on the drive.
final class EjectWatcher: @unchecked Sendable {
    private static var shared: EjectWatcher?

    private let session: DASession
    private let queue = DispatchQueue(label: "com.fenixstarlord.replicant.eject")
    private var unmountObserver: NSObjectProtocol?

    /// Starts watching for the life of the app.
    static func start() {
        if shared == nil { shared = EjectWatcher() }
    }

    private init?() {
        guard let s = DASessionCreate(kCFAllocatorDefault) else { return nil }
        session = s
        DASessionSetDispatchQueue(s, queue)
        DARegisterDiskUnmountApprovalCallback(s, nil, unmountApproval, nil)
        unmountObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didUnmountNotification, object: nil, queue: nil
        ) { note in
            if let url = note.userInfo?[NSWorkspace.volumeURLUserInfoKey] as? URL {
                ActiveScan.shared.doneEjecting(url.path)
            }
        }
    }
}

/// DiskArbitration's question "may this volume be unmounted?", answered on
/// the watcher's queue. Always yes, once any scan of the volume is stopped.
private func unmountApproval(_ disk: DADisk, _ context: UnsafeMutableRawPointer?) -> Unmanaged<DADissenter>? {
    guard let desc = DADiskCopyDescription(disk) as NSDictionary?,
          let url = desc[kDADiskDescriptionVolumePathKey] as? URL
    else { return nil }
    let volume = url.path
    let scans = ActiveScan.shared
    scans.markEjecting(volume)
    DispatchQueue.main.async { NotificationCenter.default.post(name: .volumeEjecting, object: volume) }
    // If something else keeps the drive and the unmount fails, the volume is
    // still mounted a few seconds later: forget the mark then.
    DispatchQueue.global().asyncAfter(deadline: .now() + 5) {
        if isMountPoint(volume) { scans.doneEjecting(volume) }
    }
    guard scans.isReading(onVolume: volume) else { return nil }
    if scans.stop(.eject, timeout: 3) { return nil }
    let message = "Replicant could not stop its scan of this drive in time. Try ejecting it again." as CFString
    return Unmanaged.passRetained(DADissenterCreate(kCFAllocatorDefault, DAReturn(kDAReturnBusy), message))
}

/// Whether path is where a filesystem is mounted right now.
private func isMountPoint(_ path: String) -> Bool {
    var st = statfs()
    guard statfs(path, &st) == 0 else { return false }
    let mounted = withUnsafeBytes(of: &st.f_mntonname) { raw in
        String(decoding: raw.prefix { $0 != 0 }, as: UTF8.self)
    }
    return mounted == path
}
