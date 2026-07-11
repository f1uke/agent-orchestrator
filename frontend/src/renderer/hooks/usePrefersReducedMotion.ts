import * as React from "react";

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

// True when the OS "reduce motion" accessibility preference is on. Consumers use
// it to fall back to a static presentation (e.g. the sidebar's working-session
// status pulse) instead of animating. Reactive: it updates if the user toggles
// the preference. Initialised from matchMedia so the very first render is already
// correct (no animation flash before an effect runs), matching use-mobile.tsx.
export function usePrefersReducedMotion(): boolean {
	const [prefersReducedMotion, setPrefersReducedMotion] = React.useState(() =>
		typeof window !== "undefined" && typeof window.matchMedia === "function"
			? window.matchMedia(REDUCED_MOTION_QUERY).matches
			: false,
	);

	React.useEffect(() => {
		if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
		const mql = window.matchMedia(REDUCED_MOTION_QUERY);
		const onChange = () => setPrefersReducedMotion(mql.matches);
		onChange();
		mql.addEventListener("change", onChange);
		return () => mql.removeEventListener("change", onChange);
	}, []);

	return prefersReducedMotion;
}
