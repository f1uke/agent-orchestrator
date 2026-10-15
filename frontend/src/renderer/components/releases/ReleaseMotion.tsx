import { domMax, LazyMotion, MotionConfig } from "motion/react";
import { createContext, useContext, type ReactNode } from "react";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { releaseMotion, type ReleaseMotion } from "../../lib/releases/motion";

const MotionSet = createContext<ReleaseMotion>(releaseMotion(false));

export function ReleaseMotionRoot({ children }: { children: ReactNode }) {
	const reduced = usePrefersReducedMotion();
	return (
		<LazyMotion features={domMax} strict>
			<MotionConfig reducedMotion="user">
				<MotionSet.Provider value={releaseMotion(reduced)}>{children}</MotionSet.Provider>
			</MotionConfig>
		</LazyMotion>
	);
}

export function useReleaseMotion(): ReleaseMotion {
	return useContext(MotionSet);
}
