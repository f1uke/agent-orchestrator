import Foundation
import XCTest

// Typing, the one thing this runner does to the screen.
//
// `ao sim type` used to synthesize HID key USAGES, and the guest turned each
// into whatever its input mode said that key meant: on a guest set to Thai,
// "fa12345" arrived as "ดฟๅ/_ภถ", and Thai text had no key to send at all. The
// pasteboard route that worked around it lost its Command-V whenever the
// software keyboard was up (the first key event only hides it - see
// simbridge.WakeKeyboard), and an app watching each keystroke sees one paste.
// XCTest's typeText sends CHARACTERS, through the
// software keyboard, so what arrives does not depend on the Mac's or the
// simulator's keyboard language - the same route WebDriverAgent and Maestro
// type through.
//
// The runner types only when the daemon asks, and the daemon asks only while
// the caller holds the device's lease and gesture hold: the runner is a
// mechanism, not a second way in.
//
// It does not judge whether the text arrived. It reports the field it typed
// into and what XCTest said, and the daemon reads the screen before and after
// and decides - the same check a paste goes through, so the two routes cannot
// disagree about what "landed" means.
enum Typist {
    /// Issues XCTest recorded while typing. typeText reports a failure (no
    /// keyboard focus, an app that would not idle) by recording a test issue,
    /// not by throwing; RunnerTests routes those here while `collecting` is
    /// set, so a failed type is an answer to the caller rather than a failed
    /// test and a runner that stops serving.
    static var collecting = false
    static var issues: [String] = []

    /// The element types a person types into. A focused element of any other
    /// type is still typed at - XCTest decides what has keyboard focus - but
    /// these are preferred when more than one element reports focus.
    static let editable: Set<XCUIElement.ElementType> = [.textField, .secureTextField, .textView, .searchField]

    /// What would be typed into: the element with keyboard focus and the
    /// application holding it, and whether the software keyboard is up. The
    /// daemon asks before it types, because a secure field takes only what the
    /// keyboard on screen can type (see internal/simtype).
    static func focus() -> (Int, [String: Any]) {
        let started = Date()
        let (target, out) = lookup()
        var answer = out
        answer["elapsedMs"] = Int(Date().timeIntervalSince(started) * 1000)
        return (target == nil ? 422 : 200, answer)
    }

    /// Types text into the focused element. With a layout ("latin" or
    /// "other"), the software keyboard is first switched to a layout whose
    /// letters are Latin, or are not, by its globe key - as a person would -
    /// and switched back afterwards: a secure field takes only what the
    /// keyboard on screen can type, and a Thai layout drops every Latin letter
    /// there (see internal/simtype).
    static func type(_ text: String, layout wanted: String? = nil) -> (Int, [String: Any]) {
        let started = Date()
        guard !text.isEmpty else {
            return (400, ["version": runnerVersion, "error": ["code": "bad_request", "message": "nothing to type"]])
        }
        let (target, looked) = lookup()
        var out = looked
        guard let target else {
            out["elapsedMs"] = Int(Date().timeIntervalSince(started) * 1000)
            return (422, out)
        }

        let app = XCUIApplication(bundleIdentifier: target)
        // Everything from here touches the screen, the globe key included, so
        // every issue XCTest records is this request's answer.
        issues = []
        collecting = true
        defer { collecting = false }
        var restore: Layout?
        if let wanted, wanted == "latin" || wanted == "other" {
            let latin = wanted == "latin"
            let letters = latin ? "Latin letters" : "non-Latin letters"
            guard let original = layout(app), let globe = globeKey(app) else {
                out["error"] = ["code": "no_layout",
                                "message": "the software keyboard is not on screen, so it cannot be switched to \(letters)"]
                return (422, out)
            }
            if original.latin != latin {
                var now = original
                for _ in 0..<maxGlobeTaps where now.latin != latin {
                    globe.tap()
                    now = settledLayout(app) ?? now
                }
                guard now.latin == latin else {
                    _ = switchBack(app, to: original)
                    out["error"] = ["code": "no_layout",
                                    "message": "the simulator has no keyboard layout with \(letters) to switch to"]
                    return (422, out)
                }
                out["keyboardSwitchedTo"] = original.next
                restore = original
            }
        }

        let typingStarted = Date()
        app.typeText(text)
        out["typingMs"] = Int(Date().timeIntervalSince(typingStarted) * 1000)
        if let restore {
            out["keyboardRestored"] = switchBack(app, to: restore)
        }
        out["elapsedMs"] = Int(Date().timeIntervalSince(started) * 1000)
        if !issues.isEmpty {
            out["error"] = ["code": "type_failed", "message": issues.map(firstLine).joined(separator: "; ")]
            return (422, out)
        }
        out["typed"] = true
        return (200, out)
    }

    /// The software keyboard's layout, as far as typing cares: whether its
    /// letter keys are Latin, and what the globe key says it switches to
    /// (which tells two non-Latin layouts apart). nil when no keyboard is on
    /// screen.
    struct Layout: Equatable {
        let latin: Bool
        let next: String
    }

    /// Bounds the globe-key taps: a device has a handful of keyboards.
    static let maxGlobeTaps = 4

    static func layout(_ app: XCUIApplication) -> Layout? {
        guard let snapshot = try? app.snapshot(), keyboardShown(in: snapshot) else { return nil }
        var letters = 0, latin = 0
        var next = ""
        var stack = [snapshot]
        while let node = stack.popLast() {
            if node.elementType == .key, node.label.count == 1, let char = node.label.unicodeScalars.first,
               CharacterSet.letters.contains(char) {
                letters += 1
                if char.isASCII { latin += 1 }
            }
            if node.label == "Next keyboard", next.isEmpty, let value = node.value as? String { next = value }
            stack.append(contentsOf: node.children)
        }
        return Layout(latin: letters > 0 && latin == letters, next: next)
    }

    /// A layout read after the keyboard has had a moment to redraw.
    static func settledLayout(_ app: XCUIApplication) -> Layout? {
        Thread.sleep(forTimeInterval: 0.3)
        return layout(app)
    }

    static func globeKey(_ app: XCUIApplication) -> XCUIElement? {
        let key = app.buttons["Next keyboard"].firstMatch
        return key.exists ? key : nil
    }

    /// Taps the globe key until the keyboard is the layout it was. Returns
    /// whether it got there.
    static func switchBack(_ app: XCUIApplication, to original: Layout) -> Bool {
        var now = layout(app)
        for _ in 0..<maxGlobeTaps where now != original {
            guard let globe = globeKey(app) else { return false }
            globe.tap()
            now = settledLayout(app)
        }
        return now == original
    }

    /// Finds the focused element across every application on screen. Returns
    /// the bundle id holding it, or nil with a no_focus error in the answer.
    static func lookup() -> (String?, [String: Any]) {
        var out: [String: Any] = ["version": runnerVersion]
        let candidates = Hierarchy.onScreen()
        var keyboard = false
        var found: (bundleID: String, field: XCUIElementSnapshot)?
        for target in candidates {
            guard let snapshot = try? XCUIApplication(bundleIdentifier: target.bundleID).snapshot() else { continue }
            if keyboardShown(in: snapshot) { keyboard = true }
            if found == nil, let field = focused(in: snapshot) {
                found = (target.bundleID, field)
            }
        }
        out["keyboard"] = keyboard
        out["checked"] = candidates.map(\.bundleID)
        guard let found else {
            out["error"] = [
                "code": "no_focus",
                "message": "no element on screen has keyboard focus, so there is nothing to type into",
            ]
            return (nil, out)
        }
        out["app"] = found.bundleID
        out["field"] = describe(found.field)
        return (found.bundleID, out)
    }

    /// The element with keyboard focus.
    ///
    /// `hasFocus` cannot answer this: inside a web view EVERY element reports
    /// it (seen on the nter web sign-in, iOS 26.3), including the logo and the
    /// page itself. XCTest's own snapshot carries `hasKeyboardFocus`, which is
    /// what typeText checks before it types; it is not public, so a snapshot
    /// that does not answer to it falls back to a focused text input.
    static func focused(in snapshot: XCUIElementSnapshot) -> XCUIElementSnapshot? {
        var fallback: XCUIElementSnapshot?
        var stack = [snapshot]
        while let node = stack.popLast() {
            if let keyboardFocus = hasKeyboardFocus(node) {
                if keyboardFocus { return node }
            } else if node.hasFocus, editable.contains(node.elementType), fallback == nil {
                fallback = node
            }
            stack.append(contentsOf: node.children.reversed())
        }
        return fallback
    }

    static func hasKeyboardFocus(_ node: XCUIElementSnapshot) -> Bool? {
        let object = node as AnyObject
        guard object.responds(to: NSSelectorFromString("hasKeyboardFocus")) else { return nil }
        return (object.value(forKey: "hasKeyboardFocus") as? NSNumber)?.boolValue
    }

    /// The first line of what XCTest said. Its failure carries the whole
    /// event-dispatch snapshot after it, hundreds of lines nobody acts on.
    static func firstLine(_ message: String) -> String {
        var line = message.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? message
        if let cut = line.range(of: " Event dispatch snapshot") { line = String(line[..<cut.lowerBound]) }
        return line.trimmingCharacters(in: .whitespaces)
    }

    /// A software keyboard on screen. A focused field with the keyboard
    /// hidden (a hardware keyboard, or the simulator believing in one after
    /// HID key presses) still lists it, below the bottom edge.
    static func keyboardShown(in snapshot: XCUIElementSnapshot) -> Bool {
        let screen = snapshot.frame
        var stack = [snapshot]
        while let node = stack.popLast() {
            if node.elementType == .keyboard {
                let visible = node.frame.intersection(screen)
                if !visible.isNull, visible.height > 1 { return true }
            }
            stack.append(contentsOf: node.children)
        }
        return false
    }

    static func describe(_ field: XCUIElementSnapshot) -> [String: Any] {
        var out: [String: Any] = ["type": Hierarchy.typeName(field.elementType)]
        if !field.identifier.isEmpty { out["id"] = field.identifier }
        if !field.label.isEmpty { out["label"] = field.label }
        if let placeholder = field.placeholderValue, !placeholder.isEmpty { out["placeholder"] = placeholder }
        return out
    }
}
