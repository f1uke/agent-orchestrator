/**
 * A Swift-shaped fixture for the editor harness. Three things in here are the
 * point of the file:
 *
 *  - three REAL `// MARK:` section markers: one with the `-` separator, one
 *    without, and one INDENTED — the common case in real Swift, and the one an
 *    `^\s*`-anchored regex silently drops because a grammar scopes a line's
 *    leading whitespace as plain text rather than as comment;
 *  - a `MARK:` in a comment that TRAILS code, which Xcode does not band either;
 *  - a comment that merely MENTIONS `MARK:` mid-sentence, which must NOT become
 *    a band (the spike's own source did exactly this to itself);
 *  - a string literal holding `// MARK: - …`, which must not become one either —
 *    that one is only excluded if shiki's tokens reach Monaco carrying a usable
 *    `StandardTokenType`, so it is the canary for the whole theme/scope mapping.
 *
 * It is deliberately taller than any viewport: `minimap.size: "fit"` only
 * collapses `scale` once the file outgrows the minimap canvas, so a short
 * fixture would pass while the real bug shipped.
 */
const HEADER = `import Foundation
import UIKit

/// A view controller with the section markers Xcode bands in its minimap.
/// The regex that finds them is anchored to the start of a comment, so this
/// sentence mentioning MARK: in passing must not become a band of its own.
final class PromotionHubViewController: UIViewController {
	private let reuseIdentifier = "promotion-cell"
	private let bannerTemplate = "// MARK: - Not A Section Header"
	private var offers: [Offer] = []
	private var page = 0

// MARK: - Lifecycle

	override func viewDidLoad() {
		super.viewDidLoad()
		configureCollectionView()
		loadOffers(page: 0)
	}

	override func viewWillAppear(_ animated: Bool) {
		super.viewWillAppear(animated)
		refreshIfNeeded(force: false)
	}

// MARK: User Interaction

	@objc private func didTapRefresh(_ sender: UIButton) {
		page = 0
		loadOffers(page: page)
	}

	private func didSelect(offer: Offer, at index: Int) {
		guard index < offers.count else { return }
		router.present(offer: offer, animated: true)
	}

	// MARK: - Helpers

	private func refreshIfNeeded(force: Bool) {
		let deadline = 30 // MARK: not a section header, it trails code
		guard force || age > deadline else { return }
		loadOffers(page: 0)
	}
`;

const FILLER = Array.from(
	{ length: 60 },
	(_, i) => `
	private func helper${i}(value: Int) -> String {
		// step ${i}: fold the value into the running total
		let total = value * ${i + 1}
		return "helper-\\(total)"
	}`,
).join("\n");

export const SWIFT_FIXTURE = `${HEADER}${FILLER}\n}\n`;

/** The labels Monaco should band, in order. */
export const EXPECTED_SECTION_HEADERS = ["Lifecycle", "User Interaction", "Helpers"];

/**
 * The fixture with one extra INDENTED `// MARK: -` section carrying `label`.
 *
 * The fixture's own longest label is `User Interaction` (16 chars), and the fit
 * calculation is in minimap-canvas pixels — so "it fits at 620px" is a statement
 * about THAT label, not about the setting. Real section names get longer
 * ("Networking & Cache", "Collection View Data Source"), and the failure is
 * silent: Monaco middle-truncates to `Networ…Cache` and reports nothing. This
 * lets a spec push the label until it breaks and pin the width where it does.
 */
export function fixtureWithLabel(label: string): string {
	return `${HEADER}\n\t// MARK: - ${label}\n${FILLER}\n}\n`;
}
