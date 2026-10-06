import { describe, expect, it } from "vitest";
import { type SessionChild, childLabel, childStripModel, childTooltip } from "./children";

function child(agentId: string, over: Partial<SessionChild> = {}): SessionChild {
	return {
		agentId,
		sessionId: "demo-1",
		projectId: "demo",
		branch: `ao-child/demo-1/${agentId}`,
		targetBranch: "feature/x",
		baseSha: "abc",
		worktreePath: `/data/child-worktrees/demo/demo-1/${agentId}`,
		state: "running",
		commits: 0,
		filesChanged: 0,
		createdAt: "2026-10-06T00:00:00Z",
		updatedAt: "2026-10-06T00:00:00Z",
		...over,
	};
}

describe("childStripModel", () => {
	it("leads with what needs a person, then what runs, then what merged, and counts empties without drawing them", () => {
		const model = childStripModel([
			child("a1", { state: "merged", createdAt: "2026-10-06T00:00:01Z" }),
			child("a2", { state: "removed" }),
			child("a3", { state: "running" }),
			child("a4", { state: "conflict", createdAt: "2026-10-06T00:00:05Z" }),
		]);
		expect(model.shown.map((c) => c.agentId)).toEqual(["a4", "a3", "a1"]);
		expect(model.empty).toBe(1);
		expect(model.overflow).toBe(0);
	});

	it("caps the chips and says how many more there are", () => {
		const model = childStripModel(["a", "b", "c", "d", "e"].map((id) => child(id)));
		expect(model.shown).toHaveLength(3);
		expect(model.overflow).toBe(2);
	});
});

describe("child text", () => {
	it("names a child by what the worker said it was for", () => {
		expect(childLabel(child("a1", { description: "Write the parser", agentType: "general-purpose" }))).toBe(
			"Write the parser",
		);
		expect(childLabel(child("a1", { agentType: "general-purpose" }))).toBe("general-purpose");
		expect(childLabel(child("a1"))).toBe("a1");
	});

	it("tells a person where a stuck child's work is", () => {
		const text = childTooltip(
			child("a1", { state: "conflict", commits: 2, filesChanged: 3, detail: "conflicts with feature/x in C" }),
		);
		expect(text).toContain("conflict");
		expect(text).toContain("2 commits, 3 files");
		expect(text).toContain("conflicts with feature/x in C");
		expect(text).toContain("branch ao-child/demo-1/a1");
	});
});
