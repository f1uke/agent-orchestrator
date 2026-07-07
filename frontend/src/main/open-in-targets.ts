import path from "node:path";

/** A detected Xcode workspace/project at a directory root. */
export type XcodeTarget = {
	/** The file name, e.g. "MyApp.xcworkspace" — used verbatim as the menu label. */
	name: string;
	/** Absolute path to open with `open <path>` (Xcode is the default handler). */
	path: string;
};

/**
 * The "Open in…" targets that vary by machine/project. Terminal and Finder are
 * always offered by the menu, so they are not represented here; only the
 * conditionally-shown items are: VS Code (installed?) and an Xcode
 * workspace/project (detected at the directory root and Xcode installed?).
 */
export type OpenInTargets = {
	hasVSCode: boolean;
	xcode?: XcodeTarget;
};

export type ResolveOpenTargetsInput = {
	/** Absolute path of the directory being inspected. */
	dir: string;
	/** Top-level entry names in {@link dir}. No recursion — root listing only. */
	entries: string[];
	/** Whether the Visual Studio Code app is installed. */
	vscodeInstalled: boolean;
	/** Whether the Xcode app is installed. */
	xcodeInstalled: boolean;
};

/**
 * Pure mapping from a directory's root listing plus installed-app facts to the
 * conditional "Open in…" targets. Kept side-effect free (the fs listing and app
 * probes happen in the caller) so it is fully unit-testable.
 *
 * An Xcode target is offered only when Xcode is installed AND a `.xcworkspace`
 * or `.xcodeproj` sits at the directory root; a `.xcworkspace` wins over a
 * `.xcodeproj` when both are present.
 */
export function resolveOpenTargets({
	dir,
	entries,
	vscodeInstalled,
	xcodeInstalled,
}: ResolveOpenTargetsInput): OpenInTargets {
	return {
		hasVSCode: vscodeInstalled,
		xcode: xcodeInstalled ? detectXcodeTarget(dir, entries) : undefined,
	};
}

function detectXcodeTarget(dir: string, entries: string[]): XcodeTarget | undefined {
	// Sort so a directory with multiple candidates resolves deterministically.
	const workspaces = entries.filter((entry) => entry.endsWith(".xcworkspace")).sort();
	const projects = entries.filter((entry) => entry.endsWith(".xcodeproj")).sort();
	const name = workspaces[0] ?? projects[0];
	if (!name) return undefined;
	return { name, path: path.join(dir, name) };
}
