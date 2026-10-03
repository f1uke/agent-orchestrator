import Darwin
import Foundation
import Network
import XCTest

// AO's simulator screen reader.
//
// One test that never finishes on its own: it serves the device's whole
// accessibility tree over HTTP on 127.0.0.1, so a read costs a snapshot rather
// than an xcodebuild launch. XCTest reads every process on screen - the app, a
// web sheet hosted by SafariViewService, SpringBoard's alerts, the keyboard -
// which the in-app accessibility bridge cannot.
//
// It READS, and it TYPES when asked (see Typist.swift). Every other touch goes
// through `ao sim tap` and friends. The daemon asks it to type only while the
// caller holds the device's lease and gesture hold, so the lease stays the
// only way anything drives the device.
//
// It is started and stopped by the AO daemon, one per device, and it does not
// trust the daemon to be there to stop it: when nobody has asked it anything
// for AO_RUNNER_IDLE_SECONDS it ends the test, so a daemon that died leaves no
// runner behind.

/// Bumped whenever the wire format changes. The daemon refuses a runner that
/// reports a different one, because it may be a stale build left on a port.
let runnerVersion = "4"

@_silgen_name("proc_pidpath")
private func proc_pidpath(_ pid: Int32, _ buffer: UnsafeMutableRawPointer, _ size: UInt32) -> Int32

final class RunnerTests: XCTestCase {
    /// A typeText that fails records an issue rather than throwing. While the
    /// runner is typing, the issue is the caller's answer (see Typist), not
    /// this test's failure.
    override func record(_ issue: XCTIssue) {
        if Typist.collecting {
            Typist.issues.append(issue.compactDescription)
            return
        }
        super.record(issue)
    }

    func testServe() throws {
        let env = ProcessInfo.processInfo.environment
        guard let portText = env["AO_RUNNER_PORT"], let port = NWEndpoint.Port(portText) else {
            XCTFail("AO_RUNNER_PORT is not set; the AO daemon passes it as TEST_RUNNER_AO_RUNNER_PORT")
            return
        }
        let idle = TimeInterval(env["AO_RUNNER_IDLE_SECONDS"] ?? "") ?? 60
        let server = Server(port: port, idleTimeout: idle)
        try server.start()
        while !server.finished {
            _ = RunLoop.current.run(mode: .default, before: Date(timeIntervalSinceNow: 0.5))
            server.checkIdle()
        }
        server.stop()
    }
}

/// The HTTP surface. Requests are parsed on a background queue. Reads are
/// answered on the main thread, which is where XCTest's queries belong;
/// /status is answered where it arrives, so a snapshot that is slow (an app
/// whose main thread is blocked makes XCTest wait) does not look like a dead
/// runner to the daemon's health check.
final class Server {
    private let port: NWEndpoint.Port
    private let idleTimeout: TimeInterval
    private var listener: NWListener?
    // Main thread only.
    private(set) var finished = false
    private let lock = NSLock()
    private var lastRequest = Date()

    init(port: NWEndpoint.Port, idleTimeout: TimeInterval) {
        self.port = port
        self.idleTimeout = idleTimeout
    }

    func start() throws {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: port)
        params.allowLocalEndpointReuse = true
        let listener = try NWListener(using: params)
        listener.newConnectionHandler = { [weak self] connection in
            guard let self else { return }
            Connection(connection: connection, server: self).start()
        }
        listener.stateUpdateHandler = { [weak self] state in
            if case let .failed(error) = state {
                NSLog("ao-runner: listener failed on port %@: %@", "\(self?.port.rawValue ?? 0)", "\(error)")
                DispatchQueue.main.async { self?.finished = true }
            }
        }
        listener.start(queue: DispatchQueue(label: "ao-runner.listener"))
        self.listener = listener
        NSLog("ao-runner: listening on 127.0.0.1:%d", Int(port.rawValue))
    }

    func stop() {
        listener?.cancel()
    }

    /// Any request counts as somebody still being there.
    func touch() {
        lock.lock()
        lastRequest = Date()
        lock.unlock()
    }

    func checkIdle() {
        lock.lock()
        let since = Date().timeIntervalSince(lastRequest)
        lock.unlock()
        if since > idleTimeout {
            NSLog("ao-runner: no request for %.0fs, stopping", idleTimeout)
            finished = true
        }
    }

    /// Set while typeText runs. XCTest spins the main run loop while it waits
    /// for the app to settle, which would run another request's block in the
    /// middle of the typing; such a block is put back on the queue instead.
    private(set) var typing = false

    /// Runs a request's work on the main thread, after any typing in progress.
    func onMain(_ work: @escaping () -> Void) {
        DispatchQueue.main.async { [self] in
            if typing {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) { self.onMain(work) }
                return
            }
            work()
        }
    }

    /// Answers one request. Called on the main thread.
    func handle(method: String, path: String, query: [String: String], body: Data) -> (Int, Any) {
        touch()
        switch (method, path) {
        case ("GET", "/hierarchy"):
            return (200, Hierarchy.read(
                bundleIDs: query["app"].map { $0.split(separator: ",").map(String.init) } ?? [],
                hitTest: query["hitTest"] == "1", at: query["at"].flatMap(Hierarchy.point)))
        case ("GET", "/focus"):
            let (status, out) = Typist.focus()
            return (status, out)
        case ("POST", "/type"):
            guard let json = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
                  let text = json["text"] as? String else {
                return (400, ["error": ["code": "bad_request", "message": "the body must be {\"text\": \"...\"}"]])
            }
            typing = true
            defer { typing = false }
            let (status, out) = Typist.type(text, layout: json["layout"] as? String)
            return (status, out)
        case ("POST", "/stop"):
            finished = true
            return (200, ["stopping": true])
        default:
            return (404, ["error": "no route \(method) \(path)"])
        }
    }

    func status() -> [String: Any] {
        let env = ProcessInfo.processInfo.environment
        return [
            "version": runnerVersion,
            "udid": env["SIMULATOR_UDID"] ?? "",
            "pid": Int(getpid()),
        ]
    }
}

/// One HTTP/1.1 exchange, then close. The only client is the AO daemon on the
/// same machine, so there is no keep-alive, chunking or TLS to speak of.
final class Connection {
    private let connection: NWConnection
    private let server: Server
    private var buffer = Data()
    private static let maxRequest = 64 * 1024

    init(connection: NWConnection, server: Server) {
        self.connection = connection
        self.server = server
    }

    func start() {
        connection.start(queue: DispatchQueue(label: "ao-runner.connection"))
        receive()
    }

    private func receive() {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 16 * 1024) { [self] data, _, complete, error in
            if let data { buffer.append(data) }
            if let request = Request.parse(buffer) {
                respond(to: request)
                return
            }
            if error != nil || complete || buffer.count > Self.maxRequest {
                connection.cancel()
                return
            }
            receive()
        }
    }

    private func respond(to request: Request) {
        if request.method == "GET" && request.path == "/status" {
            server.touch()
            send(200, server.status())
            return
        }
        server.onMain { [self] in
            let (status, body) = server.handle(
                method: request.method, path: request.path, query: request.query, body: request.body)
            send(status, body)
        }
    }

    private func send(_ status: Int, _ body: Any) {
        let payload = (try? JSONSerialization.data(withJSONObject: body, options: [])) ?? Data("{}".utf8)
        var head = "HTTP/1.1 \(status) \(status == 200 ? "OK" : "Error")\r\n"
        head += "Content-Type: application/json\r\nContent-Length: \(payload.count)\r\nConnection: close\r\n\r\n"
        var out = Data(head.utf8)
        out.append(payload)
        connection.send(content: out, completion: .contentProcessed { [connection] _ in connection.cancel() })
    }
}

struct Request {
    let method: String
    let path: String
    let query: [String: String]
    let body: Data

    /// A request is complete once its headers are, and its body when it has
    /// a Content-Length (only /type sends one).
    static func parse(_ data: Data) -> Request? {
        guard let end = data.range(of: Data("\r\n\r\n".utf8)),
              let head = String(data: data[data.startIndex..<end.lowerBound], encoding: .utf8) else { return nil }
        let lines = head.components(separatedBy: "\r\n")
        guard let line = lines.first else { return nil }
        let parts = line.split(separator: " ")
        guard parts.count >= 2, let components = URLComponents(string: String(parts[1])) else { return nil }
        var length = 0
        for header in lines.dropFirst() {
            let pair = header.split(separator: ":", maxSplits: 1)
            if pair.count == 2, pair[0].trimmingCharacters(in: .whitespaces).lowercased() == "content-length" {
                length = Int(pair[1].trimmingCharacters(in: .whitespaces)) ?? 0
            }
        }
        let bodyStart = end.upperBound
        guard data.count - (bodyStart - data.startIndex) >= length else { return nil }
        let body = data[bodyStart..<(bodyStart + length)]
        var query: [String: String] = [:]
        for item in components.queryItems ?? [] { query[item.name] = item.value ?? "" }
        return Request(method: String(parts[0]), path: components.path, query: query, body: Data(body))
    }
}

/// The screen as AO reads it: every application on screen, each with its own
/// tree, frontmost first.
enum Hierarchy {
    /// Processes that draw INTO another app's screen. Nothing reports them as
    /// the foreground application - the app that presented them still is - so
    /// each is read whenever XCTest says it is running in the foreground.
    ///
    /// SafariViewService hosts ASWebAuthenticationSession and
    /// SFSafariViewController: on iOS 26 the whole login page of an app that
    /// signs in on the web lives there, and the app's own tree holds none of it.
    static let remoteViewHosts = ["com.apple.SafariViewService"]

    /// With hitTest, every element a caller could tap is also hit-tested, and
    /// one drawn under something else carries `covered` (see Occlusion). It
    /// costs about a millisecond per element, so only callers that hand out
    /// tap points ask for it.
    /// "x,y" as fractions of the screen, the way every AO coordinate is given.
    static func point(_ text: String) -> CGPoint? {
        let parts = text.split(separator: ",").compactMap { Double($0.trimmingCharacters(in: .whitespaces)) }
        guard parts.count == 2, parts.allSatisfy({ $0.isFinite && $0 >= 0 && $0 <= 1 }) else { return nil }
        return CGPoint(x: parts[0], y: parts[1])
    }

    /// With `at`, the element a touch at that point reaches carries `reached`
    /// (see Occlusion.reached), for a recorder turning a coordinate into a
    /// selector.
    static func read(bundleIDs explicit: [String], hitTest: Bool = false, at: CGPoint? = nil) -> [String: Any] {
        let started = Date()
        var errors: [String] = []
        var targets: [(bundleID: String, pid: Int32)] = []
        var source = "explicit"
        if explicit.isEmpty {
            (targets, source) = onScreenWithSource()
            if targets.isEmpty {
                errors.append("XCTest reported no foreground application")
            }
        } else {
            targets = explicit.map { ($0, pid(of: XCUIApplication(bundleIdentifier: $0))) }
        }

        var snapshots: [(target: (bundleID: String, pid: Int32), snapshot: XCUIElementSnapshot)] = []
        var screen: CGSize = .zero
        for target in targets {
            let app = XCUIApplication(bundleIdentifier: target.bundleID)
            do {
                let snapshot = try app.snapshot()
                if snapshot.frame.width * snapshot.frame.height > screen.width * screen.height {
                    screen = snapshot.frame.size
                }
                snapshots.append((target, snapshot))
            } catch {
                errors.append("\(target.bundleID): \(error.localizedDescription)")
            }
        }
        // Judged across every application at once: what covers an element is
        // as often another process (the keyboard, a web sheet) as its own app.
        var occlusion: Occlusion.Result?
        if hitTest {
            occlusion = Occlusion.judge(snapshots.map(\.snapshot))
        }
        var reached: ObjectIdentifier?
        var atSummary: [String: Any]?
        if let at {
            let point = CGPoint(x: at.x * screen.width, y: at.y * screen.height)
            let (found, error) = Occlusion.reached(snapshots.map(\.snapshot), at: point)
            reached = found.map { ObjectIdentifier($0 as AnyObject) }
            atSummary = ["x": finite(point.x), "y": finite(point.y), "found": found != nil]
            if let error { atSummary?["error"] = error }
        }
        var apps: [[String: Any]] = []
        for (target, snapshot) in snapshots {
            var entry: [String: Any] = [
                "bundleId": target.bundleID,
                "tree": node(snapshot, covers: occlusion?.covers ?? [:], reached: reached),
            ]
            if target.pid > 0 { entry["pid"] = Int(target.pid) }
            if remoteViewHosts.contains(target.bundleID) { entry["remoteView"] = true }
            apps.append(entry)
        }
        var out: [String: Any] = [
            "version": runnerVersion,
            "screen": ["width": finite(screen.width), "height": finite(screen.height)],
            "apps": apps,
            "foregroundSource": source,
            "elapsedMs": Int(Date().timeIntervalSince(started) * 1000),
        ]
        if let occlusion {
            var summary: [String: Any] = [
                "checked": occlusion.checked, "covered": occlusion.covers.count, "elapsedMs": occlusion.elapsedMs,
            ]
            if let error = occlusion.error { summary["error"] = error }
            out["hitTest"] = summary
        }
        if let atSummary { out["at"] = atSummary }
        if !errors.isEmpty { out["errors"] = errors }
        return out
    }

    /// Every application drawing on screen, frontmost first: a presented
    /// remote-view sheet, then the foreground applications.
    static func onScreen() -> [(bundleID: String, pid: Int32)] {
        onScreenWithSource().0
    }

    static func onScreenWithSource() -> ([(bundleID: String, pid: Int32)], String) {
        var (targets, source) = foreground()
        // A presented sheet is on top of the app that presented it.
        for host in remoteViewHosts.reversed() where !targets.contains(where: { $0.bundleID == host }) {
            let app = XCUIApplication(bundleIdentifier: host)
            if app.state == .runningForeground {
                targets.insert((host, pid(of: app)), at: 0)
            }
        }
        return (targets, source)
    }

    /// What XCTest says is in the foreground, as applications with a bundle
    /// id. Neither question is public API, so a missing selector answers
    /// nothing rather than crashing, and the caller says so.
    ///
    /// The first route is XCUIDevice.system (public) asking for its
    /// foreground applications (not public). The second is the accessibility
    /// client underneath it, which only knows pids.
    static func foreground() -> ([(bundleID: String, pid: Int32)], String) {
        let system = XCUIDevice.shared.system as NSObject
        let foregroundSel = NSSelectorFromString("activeForegroundApplications")
        if system.responds(to: foregroundSel),
           let list = system.perform(foregroundSel)?.takeUnretainedValue() as? [NSObject] {
            let apps: [(bundleID: String, pid: Int32)] = list.compactMap { item in
                guard let bundleID = item.value(forKey: "bundleID") as? String, !bundleID.isEmpty else { return nil }
                return (bundleID, (item.value(forKey: "processID") as? NSNumber)?.int32Value ?? 0)
            }
            if !apps.isEmpty { return (apps, "system") }
        }
        let device = XCUIDevice.shared as NSObject
        let interfaceSel = NSSelectorFromString("accessibilityInterface")
        let activeSel = NSSelectorFromString("activeApplications")
        guard device.responds(to: interfaceSel),
              let client = device.perform(interfaceSel)?.takeUnretainedValue() as? NSObject,
              client.responds(to: activeSel),
              let elements = client.perform(activeSel)?.takeUnretainedValue() as? [NSObject] else {
            return ([], "none")
        }
        let apps: [(bundleID: String, pid: Int32)] = elements.compactMap { element in
            guard let pid = (element.value(forKey: "processIdentifier") as? NSNumber)?.int32Value,
                  let bundleID = bundleID(pid: pid) else { return nil }
            return (bundleID, pid)
        }
        return (apps, "accessibility")
    }

    static func pid(of app: XCUIApplication) -> Int32 {
        let object = app as NSObject
        guard object.responds(to: NSSelectorFromString("processID")) else { return 0 }
        return (object.value(forKey: "processID") as? NSNumber)?.int32Value ?? 0
    }

    /// A simulator process is a process on the Mac, so its executable path
    /// names the bundle it was launched from.
    static func bundleID(pid: Int32) -> String? {
        var buffer = [CChar](repeating: 0, count: 4 * Int(MAXPATHLEN))
        guard proc_pidpath(pid, &buffer, UInt32(buffer.count)) > 0 else { return nil }
        var url = URL(fileURLWithPath: String(cString: buffer))
        while url.pathComponents.count > 1 {
            if url.pathExtension == "app" || url.pathExtension == "appex" {
                return Bundle(url: url)?.bundleIdentifier
            }
            url.deleteLastPathComponent()
        }
        return nil
    }

    static func node(_ snapshot: XCUIElementSnapshot, covers: [ObjectIdentifier: Occlusion.Cover] = [:],
                     reached: ObjectIdentifier? = nil) -> [String: Any] {
        let frame = snapshot.frame
        var out: [String: Any] = [
            "type": typeName(snapshot.elementType),
            "frame": [
                "x": finite(frame.origin.x), "y": finite(frame.origin.y),
                "width": finite(frame.size.width), "height": finite(frame.size.height),
            ],
            "enabled": snapshot.isEnabled,
        ]
        if !snapshot.identifier.isEmpty { out["id"] = snapshot.identifier }
        if !snapshot.label.isEmpty { out["label"] = snapshot.label }
        if let value = snapshot.value.map({ "\($0)" }), !value.isEmpty { out["value"] = value }
        if let placeholder = snapshot.placeholderValue, !placeholder.isEmpty { out["placeholder"] = placeholder }
        if snapshot.isSelected { out["selected"] = true }
        // Keyboard focus where XCTest reports it: inside a web view every
        // element answers hasFocus, so "focused" would mark the whole page.
        if Typist.hasKeyboardFocus(snapshot) ?? snapshot.hasFocus { out["focused"] = true }
        if let cover = covers[ObjectIdentifier(snapshot as AnyObject)] {
            var covered: [String: Any] = ["by": cover.by]
            if let point = cover.point { covered["point"] = ["x": finite(point.x), "y": finite(point.y)] }
            out["covered"] = covered
        }
        if let reached, reached == ObjectIdentifier(snapshot as AnyObject) { out["reached"] = true }
        let children = snapshot.children.map { node($0, covers: covers, reached: reached) }
        if !children.isEmpty { out["children"] = children }
        return out
    }

    static func finite(_ value: CGFloat) -> Double {
        let double = Double(value)
        return double.isFinite ? double : 0
    }

    static func typeName(_ type: XCUIElement.ElementType) -> String {
        let index = Int(type.rawValue)
        return index < typeNames.count ? typeNames[index] : "Type\(index)"
    }

    // XCUIElement.ElementType, in raw-value order.
    static let typeNames = [
        "Any", "Other", "Application", "Group", "Window", "Sheet", "Drawer", "Alert", "Dialog", "Button",
        "RadioButton", "RadioGroup", "CheckBox", "DisclosureTriangle", "PopUpButton", "ComboBox", "MenuButton",
        "ToolbarButton", "Popover", "Keyboard", "Key", "NavigationBar", "TabBar", "TabGroup", "Toolbar",
        "StatusBar", "Table", "TableRow", "TableColumn", "Outline", "OutlineRow", "Browser", "CollectionView",
        "Slider", "PageIndicator", "ProgressIndicator", "ActivityIndicator", "SegmentedControl", "Picker",
        "PickerWheel", "Switch", "Toggle", "Link", "Image", "Icon", "SearchField", "ScrollView", "ScrollBar",
        "StaticText", "TextField", "SecureTextField", "DatePicker", "TextView", "Menu", "MenuItem", "MenuBar",
        "MenuBarItem", "Map", "WebView", "IncrementArrow", "DecrementArrow", "Timeline", "RatingIndicator",
        "ValueIndicator", "SplitGroup", "Splitter", "RelevanceIndicator", "ColorWell", "HelpTag", "Matte",
        "DockItem", "Ruler", "RulerMarker", "Grid", "LevelIndicator", "Cell", "LayoutArea", "LayoutItem",
        "Handle", "Stepper", "Tab", "TouchBar", "StatusItem",
    ]
}
