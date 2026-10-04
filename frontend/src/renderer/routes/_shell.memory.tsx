import { createFileRoute } from "@tanstack/react-router";
import { MemoryPage } from "../components/memory/MemoryPage";

export const Route = createFileRoute("/_shell/memory")({
	component: MemoryPage,
});
