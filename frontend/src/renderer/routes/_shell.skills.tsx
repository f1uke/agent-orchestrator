import { createFileRoute } from "@tanstack/react-router";
import { SkillsPage } from "../components/skills/SkillsPage";

export const Route = createFileRoute("/_shell/skills")({
	component: SkillsPage,
});
