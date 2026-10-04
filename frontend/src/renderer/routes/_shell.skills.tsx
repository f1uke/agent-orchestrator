import { createFileRoute, redirect } from "@tanstack/react-router";

// The Memory inbox lived at /skills before approving a proposal wrote Claude
// Code memory rather than a skill; an old link or a restored location lands on
// its new home.
export const Route = createFileRoute("/_shell/skills")({
	beforeLoad: () => {
		throw redirect({ to: "/memory", replace: true });
	},
});
