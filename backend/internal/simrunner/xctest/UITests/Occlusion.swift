import Foundation
import XCTest

// What a touch at an element's tap point would actually land on.
//
// XCTest reports every element with its frame, and a frame says nothing about
// what is drawn on top of it: a row scrolled under the tab bar, a button under
// the keyboard's `^ v Done` bar, a page under a sheet all come back looking
// exactly as reachable as anything else. A tap at their centre lands on the
// tab bar, the keyboard bar or the sheet instead. Measured on nter (iOS 26.3):
// `ต่อไป` under the keyboard bar and Top Stories rows under the tab bar, six
// times in twelve runs of an agent driving the app.
//
// So the runner asks the accessibility server what is at each element's
// centre - the same system-wide hit-test VoiceOver uses, across every process
// on screen - rather than guessing from bar heights. An element whose centre
// answers with something unrelated, in another layer of the screen, is
// covered. When part of it is still showing, the part is hit-tested too, and
// a point there is handed out instead of the centre.
enum Occlusion {
    /// Element types that only group things, mirroring the Go converter's
    /// isLayoutContainer: an element of one of these with no name, value,
    /// identifier or focus is never reported, so it is never judged either.
    static let layoutTypes: Set<XCUIElement.ElementType> = [
        .other, .group, .window, .scrollView, .webView, .cell, .table, .collectionView, .any,
    ]

    /// Containers whose children move together. Two elements that meet inside
    /// one are laid out side by side in the same content, and the
    /// accessibility hit-test, which answers the NEAREST element rather than
    /// the one under the point in some layouts, must not make one of them
    /// "cover" the other.
    static let scrollingTypes: Set<XCUIElement.ElementType> = [.scrollView, .table, .collectionView, .webView]

    /// Types that name the thing on top better than the leaf the hit-test
    /// answered: "covered by Keyboard", not "covered by Key r".
    static let overlayTypes: Set<XCUIElement.ElementType> = [
        .keyboard, .tabBar, .toolbar, .navigationBar, .sheet, .alert, .dialog, .popover, .menu, .statusBar,
    ]

    /// A container this much of the screen, or more, is a layer of the screen
    /// (a window, a tab bar controller's view) rather than a piece of one
    /// view's layout. Measured: the layers that cover things meet at 100% of
    /// the screen; a web view's frame below the sheet's grabber is 92%.
    static let layerFraction: CGFloat = 0.95

    /// The thinnest strip of an element worth touching. A sliver along the
    /// tab bar's edge is a target a person would not try for either.
    static let minimumStrip: CGFloat = 10

    struct Cover {
        let by: [String: Any]
        /// A point in the part of the element still showing, in screen points.
        let point: CGPoint?
    }

    /// The hit-test is not public API. A missing selector means no element is
    /// judged, and the caller says so rather than calling everything visible.
    ///
    /// The error is an Objective-C `NSError **`, which is `__autoreleasing`:
    /// the callee stores an autoreleased error WITHOUT retaining it for us. It
    /// must be typed AutoreleasingUnsafeMutablePointer so Swift retains what
    /// comes back. Typed as a plain UnsafeMutablePointer, Swift released an
    /// error it never owned, and the autorelease pool released it again later
    /// - the runner died with EXC_BAD_ACCESS in objc_autoreleasePoolPop
    /// whenever a hit-test failed, which happens while the screen changes.
    typealias HitTest = @convention(c) (NSObject, Selector, NSObject, CGPoint, AutoreleasingUnsafeMutablePointer<NSError?>?) -> NSObject?
    static let hitTestSelector = NSSelectorFromString("hitTestElement:withPoint:error:")

    static func hitTester() -> ((NSObject, CGPoint) -> NSObject?)? {
        let device = XCUIDevice.shared as NSObject
        let interfaceSel = NSSelectorFromString("accessibilityInterface")
        guard device.responds(to: interfaceSel),
              let client = device.perform(interfaceSel)?.takeUnretainedValue() as? NSObject,
              client.responds(to: hitTestSelector) else { return nil }
        let function = unsafeBitCast(client.method(for: hitTestSelector), to: HitTest.self)
        return { root, point in
            var error: NSError?
            return function(client, hitTestSelector, root, point, &error)
        }
    }

    /// One snapshot node, flattened.
    struct Node {
        let snapshot: XCUIElementSnapshot
        let parent: Int
        let root: Int
        let element: NSObject?
    }

    struct Result {
        /// Covered elements, by the snapshot object they were read from.
        var covers: [ObjectIdentifier: Cover] = [:]
        var checked = 0
        var elapsedMs = 0
        var error: String?
    }

    static func judge(_ roots: [XCUIElementSnapshot]) -> Result {
        let started = Date()
        var result = judgeTree(roots)
        result.elapsedMs = Int(Date().timeIntervalSince(started) * 1000)
        return result
    }

    /// The snapshot flattened into one array, with the questions both the
    /// occlusion judge and the point hit-test ask of it.
    struct Tree {
        var nodes: [Node] = []
        var byElement: [NSObject: [Int]] = [:]
        let screen: CGRect
        let hitTest: (NSObject, CGPoint) -> NSObject?

        init?(_ roots: [XCUIElementSnapshot], hitTest: @escaping (NSObject, CGPoint) -> NSObject?) {
            guard let screen = roots.map(\.frame).max(by: { $0.width * $0.height < $1.width * $1.height }),
                  screen.width > 0, screen.height > 0 else { return nil }
            self.screen = screen
            self.hitTest = hitTest
            for root in roots { add(root, parent: -1, root: -1) }
        }

        private mutating func add(_ snapshot: XCUIElementSnapshot, parent: Int, root: Int) {
            let index = nodes.count
            let element = Occlusion.accessibilityElement(snapshot)
            nodes.append(Node(snapshot: snapshot, parent: parent, root: parent < 0 ? index : root, element: element))
            if let element { byElement[element, default: []].append(index) }
            for child in snapshot.children { add(child, parent: index, root: parent < 0 ? index : root) }
        }

        func ancestors(_ index: Int) -> [Int] {
            var out: [Int] = []
            var at = nodes[index].parent
            while at >= 0 { out.append(at); at = nodes[at].parent }
            return out
        }

        func related(_ a: Int, _ b: Int) -> Bool {
            a == b || ancestors(a).contains(b) || ancestors(b).contains(a)
        }

        /// Whether a and b are parts of one layer of the screen, so neither
        /// is drawn over the other: they meet inside content that scrolls
        /// together, or inside a container smaller than a layer.
        func sameLayer(_ a: Int, _ b: Int) -> Bool {
            let above = Set(ancestors(a))
            guard let meet = ancestors(b).first(where: { above.contains($0) }) else { return false }
            var at = meet
            while at >= 0 {
                if scrollingTypes.contains(nodes[at].snapshot.elementType) { return true }
                at = nodes[at].parent
            }
            let frame = nodes[meet].snapshot.frame
            return frame.width * frame.height < layerFraction * screen.width * screen.height
        }

        /// What answers at a point, as indices into the tree; nil when the
        /// hit-test gave nothing this read knows. The hit-test is system-wide,
        /// so which root it starts from does not change the answer.
        func hit(_ point: CGPoint, from index: Int) -> [Int]? {
            guard let root = nodes[nodes[index].root].element,
                  let answer = hitTest(root, point),
                  let element = Occlusion.unwrap(answer) else { return nil }
            return byElement[element]
        }
    }

    static func judgeTree(_ roots: [XCUIElementSnapshot]) -> Result {
        var result = Result()
        guard let hitTest = hitTester() else {
            result.error = "XCTest has no accessibility hit-test here (\(NSStringFromSelector(hitTestSelector)))"
            return result
        }
        guard let tree = Tree(roots, hitTest: hitTest) else { return result }
        let nodes = tree.nodes
        let screen = tree.screen
        func related(_ a: Int, _ b: Int) -> Bool { tree.related(a, b) }
        func sameLayer(_ a: Int, _ b: Int) -> Bool { tree.sameLayer(a, b) }
        func hit(_ point: CGPoint, from index: Int) -> [Int]? { tree.hit(point, from: index) }
        /// The element a touch at the point reaches, unless it is index or
        /// part of index's own layer.
        func covering(_ hits: [Int], _ index: Int) -> Int? {
            if hits.contains(where: { related($0, index) || sameLayer($0, index) }) { return nil }
            return hits.first
        }

        /// The covering element as a person would name it.
        func describe(_ top: Int, layer: Int) -> [String: Any] {
            var named: Int?
            var overlay: Int?
            var at = top
            while true {
                let snapshot = nodes[at].snapshot
                if named == nil, !snapshot.label.isEmpty || !snapshot.identifier.isEmpty { named = at }
                if overlayTypes.contains(snapshot.elementType) { overlay = at }
                if at == layer || nodes[at].parent < 0 { break }
                at = nodes[at].parent
            }
            // The keyboard's own row of buttons (globe, dictation) sits beside
            // the Keyboard element, not inside it: the layer holds both.
            if overlay == nil {
                var queue = [nodes[layer].snapshot]
                while !queue.isEmpty {
                    let next = queue.removeFirst()
                    if overlayTypes.contains(next.elementType) { return naming(next) }
                    queue.append(contentsOf: next.children)
                }
            }
            return naming(nodes[overlay ?? named ?? top].snapshot)
        }

        func naming(_ snapshot: XCUIElementSnapshot) -> [String: Any] {
            var out: [String: Any] = ["type": Hierarchy.typeName(snapshot.elementType)]
            if !snapshot.label.isEmpty { out["label"] = snapshot.label }
            if !snapshot.identifier.isEmpty { out["id"] = snapshot.identifier }
            return out
        }

        let screenArea = screen.width * screen.height
        for (index, node) in nodes.enumerated() where node.parent >= 0 && judged(node.snapshot) {
            let frame = node.snapshot.frame
            let centre = CGPoint(x: frame.midX, y: frame.midY)
            guard screen.contains(centre) else { continue }
            result.checked += 1
            guard let hits = hit(centre, from: index), let top = covering(hits, index) else { continue }

            // The layer on top, as far up as it stays out of this element's
            // own branch and smaller than the screen: the tab bar, not the
            // tab button; the keyboard's panel, not one key.
            var layer = top
            while true {
                let up = nodes[layer].parent
                guard up >= 0, !related(up, index) else { break }
                let f = nodes[up].snapshot.frame
                if f.width * f.height >= layerFraction * screenArea { break }
                layer = up
            }

            var point: CGPoint?
            let showing = visibleFrame(node.snapshot).intersection(screen)
            for strip in strips(of: showing, minus: nodes[layer].snapshot.frame) {
                let candidate = CGPoint(x: strip.midX, y: strip.midY)
                if let found = hit(candidate, from: index), covering(found, index) == nil {
                    point = candidate
                    break
                }
            }
            result.covers[ObjectIdentifier(node.snapshot as AnyObject)] = Cover(by: describe(top, layer: layer), point: point)
        }
        return result
    }

    /// The element a touch at `point` reaches - what a recorder has to name
    /// when all it was told is a coordinate.
    ///
    /// The tree's order cannot answer it: the roots run frontmost first, a
    /// page's chart can come after the buttons it does not cover, and a frame
    /// says nothing about a keyboard or a tab bar drawn over it. The hit-test
    /// can, but only about the LAYER: on iOS 26 it answers a neighbouring tab
    /// for a point inside the tab bar. So it picks the layer, and the deepest
    /// element whose frame holds the point inside that layer - the answer's
    /// own branch, or anything in one layer with it - is what was touched.
    ///
    /// The SMALLEST such element, not the deepest: a web page's content runs
    /// on under the sign-in sheet's address bar and is twenty levels deeper
    /// than the bar's buttons, so "deepest" named the page for a tap on
    /// Cancel. A finger on a control inside a bigger thing touched the
    /// control, and the control is the smaller of the two.
    static func reached(_ roots: [XCUIElementSnapshot], at point: CGPoint) -> (snapshot: XCUIElementSnapshot?, error: String?) {
        guard let hitTest = hitTester() else {
            return (nil, "XCTest has no accessibility hit-test here (\(NSStringFromSelector(hitTestSelector)))")
        }
        guard let tree = Tree(roots, hitTest: hitTest), !tree.nodes.isEmpty else { return (nil, "nothing is on the screen") }
        guard tree.screen.contains(point) else { return (nil, "the point is off the screen") }
        guard let hits = tree.hit(point, from: 0), !hits.isEmpty else {
            return (nil, "the hit-test answered nothing this read holds")
        }
        var best: (index: Int, area: CGFloat, depth: Int)?
        for (index, node) in tree.nodes.enumerated() where node.parent >= 0 {
            let snapshot = node.snapshot
            guard judged(snapshot), !isScrollIndicator(snapshot), snapshot.frame.contains(point),
                  hits.contains(where: { tree.related($0, index) || tree.sameLayer($0, index) }) else { continue }
            // Smallest wins; between equals the deeper, then the later one,
            // which is drawn later.
            let area = snapshot.frame.width * snapshot.frame.height
            let depth = tree.ancestors(index).count
            if let current = best, area > current.area || (area == current.area && depth < current.depth) { continue }
            best = (index, area, depth)
        }
        guard let best else { return (nil, "nothing reported holds the point in the layer the hit-test answered") }
        return (tree.nodes[best.index].snapshot, nil)
    }

    /// A scroll view's indicator ("Vertical scroll bar, 2 pages"). It is laid
    /// over the content's edge, but a finger there reaches the content, and
    /// its label changes with the content's length.
    static func isScrollIndicator(_ snapshot: XCUIElementSnapshot) -> Bool {
        snapshot.elementType == .other && snapshot.label.contains("scroll bar")
    }

    /// Whether the Go converter will report this element, which is the only
    /// reason to spend a hit-test on it.
    static func judged(_ snapshot: XCUIElementSnapshot) -> Bool {
        let frame = snapshot.frame
        guard frame.width > 0, frame.height > 0 else { return false }
        if !layoutTypes.contains(snapshot.elementType) { return true }
        let value = snapshot.value.map { "\($0)" } ?? ""
        return !snapshot.label.isEmpty || !snapshot.identifier.isEmpty || !value.isEmpty
            || (Typist.hasKeyboardFocus(snapshot) ?? snapshot.hasFocus)
    }

    /// The parts of a rectangle outside another, largest first, that are wide
    /// and tall enough to touch.
    static func strips(of area: CGRect, minus cover: CGRect) -> [CGRect] {
        guard !area.isNull, !area.isEmpty else { return [] }
        let overlap = area.intersection(cover)
        // Clear of the cover already - a scroll view that ends where the tab
        // bar begins has clipped the element - but still held to the minimum.
        let candidates = overlap.isNull || overlap.isEmpty ? [area] : [
            CGRect(x: area.minX, y: area.minY, width: area.width, height: overlap.minY - area.minY),
            CGRect(x: area.minX, y: overlap.maxY, width: area.width, height: area.maxY - overlap.maxY),
            CGRect(x: area.minX, y: area.minY, width: overlap.minX - area.minX, height: area.height),
            CGRect(x: overlap.maxX, y: area.minY, width: area.maxX - overlap.maxX, height: area.height),
        ]
        return candidates
            .filter { $0.width >= minimumStrip && $0.height >= minimumStrip }
            .sorted { $0.width * $0.height > $1.width * $1.height }
    }

    /// The part of the element its scrolling ancestors still show; its whole
    /// frame where XCTest cannot say.
    static func visibleFrame(_ snapshot: XCUIElementSnapshot) -> CGRect {
        let object = snapshot as AnyObject
        guard object.responds(to: NSSelectorFromString("visibleFrame")),
              let value = object.value(forKey: "visibleFrame") as? NSValue else { return snapshot.frame }
        let frame = value.cgRectValue
        return frame.isNull || frame.isEmpty ? snapshot.frame : frame
    }

    static func accessibilityElement(_ snapshot: XCUIElementSnapshot) -> NSObject? {
        let object = snapshot as AnyObject
        guard object.responds(to: NSSelectorFromString("accessibilityElement")) else { return nil }
        return object.value(forKey: "accessibilityElement") as? NSObject
    }

    /// The hit-test answers an accessibility element; some XCTest versions
    /// wrap it in a snapshot.
    static func unwrap(_ answer: NSObject) -> NSObject? {
        if answer.responds(to: NSSelectorFromString("accessibilityElement")) {
            return answer.value(forKey: "accessibilityElement") as? NSObject
        }
        return answer
    }
}
