import Darwin
import Foundation

// Standalone process tests: run with macos/test-cancellation.sh. Uses the
// production ActiveScan directly and works with Command Line Tools (no XCTest).
private struct Failure: Error { let message: String }

private func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    if !condition() { throw Failure(message: message) }
}

private final class RunningScan {
    let process = Process()
    let exited = DispatchSemaphore(value: 0)
    var children: [pid_t] = []

    init(_ scan: ActiveScan, withChild: Bool = false) throws {
        let output = Pipe()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", withChild
            ? "trap '' INT; /bin/sleep 60 & echo ready; wait"
            : "trap '' INT; echo ready; exec /bin/sleep 60"]
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        let done = exited
        process.terminationHandler = { _ in scan.ended(); done.signal() }
        try process.run()
        // The ready line comes after SIGINT is ignored, avoiding a launch race.
        try expect(output.fileHandleForReading.readData(ofLength: 6) == Data("ready\n".utf8), "child not ready")
        scan.attach(pid: process.processIdentifier)
        if withChild {
            var buf = [pid_t](repeating: 0, count: 64)
            let n = proc_listchildpids(process.processIdentifier, &buf, Int32(buf.count * MemoryLayout<pid_t>.size))
            children = n > 0 ? buf.filter { $0 > 0 } : []
        }
    }

    deinit {
        for child in children { kill(child, SIGKILL) }
        if process.isRunning { kill(process.processIdentifier, SIGKILL) }
        process.waitUntilExit()
    }

    func expectKilled() throws {
        try expect(exited.wait(timeout: .now() + 3) == .success, "scanner did not exit")
        try expect(process.terminationReason == .uncaughtSignal && process.terminationStatus == SIGKILL,
                   "scanner was not force-stopped")
    }
}

@main
struct ActiveScanTests {
    static func main() throws {
        try cancelEscalates()
        try cancelBeforeAttach()
        try oldDeadlineCannotKillNextScan()
        try cancelStopsToolChild()
        print("PASS: cancellation escalation, delayed attach, generation isolation, tool cleanup")
    }

    static func begin(_ scan: ActiveScan) throws {
        try expect(scan.begin(path: NSTemporaryDirectory()), "scan did not begin")
    }

    static func cancelEscalates() throws {
        let scan = ActiveScan()
        try begin(scan)
        let running = try RunningScan(scan)
        scan.interrupt(.user, timeout: 0.1)
        try running.expectKilled()
        guard case .user? = scan.stopReason else { throw Failure(message: "lost cancellation reason") }
    }

    static func cancelBeforeAttach() throws {
        let scan = ActiveScan()
        try begin(scan)
        scan.interrupt(.user, timeout: 0.05)
        Thread.sleep(forTimeInterval: 0.2)
        let running = try RunningScan(scan)
        try running.expectKilled()
    }

    static func oldDeadlineCannotKillNextScan() throws {
        let scan = ActiveScan()
        try begin(scan)
        let first = try RunningScan(scan)
        scan.interrupt(.user, timeout: 0.15)
        kill(first.process.processIdentifier, SIGKILL)
        try first.expectKilled()
        try begin(scan)
        let next = try RunningScan(scan)
        try expect(next.exited.wait(timeout: .now() + 0.4) == .timedOut, "old deadline killed next scan")
        try expect(next.process.isRunning && scan.stopReason == nil, "next scan inherited cancellation")
        withExtendedLifetime(first) {}
    }

    static func cancelStopsToolChild() throws {
        let scan = ActiveScan()
        try begin(scan)
        let running = try RunningScan(scan, withChild: true)
        try expect(!running.children.isEmpty, "no tool child")
        scan.interrupt(.user, timeout: 0.1)
        try running.expectKilled()
        let deadline = Date().addingTimeInterval(2)
        while running.children.contains(where: { kill($0, 0) == 0 }) && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.01)
        }
        for child in running.children { try expect(kill(child, 0) == -1, "tool child survived") }
    }
}
