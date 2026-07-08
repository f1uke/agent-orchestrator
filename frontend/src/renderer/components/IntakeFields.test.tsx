import { describe, expect, it } from "vitest";
import {
	buildIntake,
	deriveGitHubRepo,
	deriveGitLabRepo,
	deriveRepoWebURL,
	deriveTrackerRepo,
	type IntakeForm,
	providerFromOrigin,
} from "./IntakeFields";

const form = (overrides: Partial<IntakeForm> = {}): IntakeForm => ({
	enabled: false,
	provider: "github",
	repo: "",
	assignee: "",
	...overrides,
});

describe("buildIntake", () => {
	it("emits the selected provider when intake is enabled", () => {
		expect(buildIntake(form({ enabled: true, provider: "github", assignee: "ada" }))).toMatchObject({
			enabled: true,
			provider: "github",
			assignee: "ada",
		});
		expect(buildIntake(form({ enabled: true, provider: "gitlab", assignee: "ada" }))).toMatchObject({
			enabled: true,
			provider: "gitlab",
			assignee: "ada",
		});
	});

	it("omits the provider (and everything) for disabled intake", () => {
		expect(buildIntake(form({ enabled: false, provider: "gitlab" }))).toBeUndefined();
	});
});

describe("providerFromOrigin", () => {
	it("detects GitHub hosts", () => {
		expect(providerFromOrigin("https://github.com/acme/repo.git")).toBe("github");
		expect(providerFromOrigin("git@github.com:acme/repo.git")).toBe("github");
	});

	it("detects self-hosted GitLab by host, not by the whole URL", () => {
		expect(providerFromOrigin("https://gitlab.finnomena.com/group/sub/proj.git")).toBe("gitlab");
		expect(providerFromOrigin("git@gitlab.finnomena.com:group/sub/proj.git")).toBe("gitlab");
	});

	it("is not fooled by a GitHub repo whose name contains 'gitlab'", () => {
		expect(providerFromOrigin("https://github.com/acme/gitlab-mirror.git")).toBe("github");
	});

	it("defaults to github for unknown or missing origins", () => {
		expect(providerFromOrigin(undefined)).toBe("github");
		expect(providerFromOrigin("")).toBe("github");
	});
});

describe("deriveGitLabRepo", () => {
	it("preserves the full nested group path", () => {
		expect(deriveGitLabRepo("https://gitlab.finnomena.com/group/sub/proj.git")).toBe("group/sub/proj");
		expect(deriveGitLabRepo("git@gitlab.finnomena.com:group/sub/proj.git")).toBe("group/sub/proj");
	});

	it("returns undefined for a single-segment path", () => {
		expect(deriveGitLabRepo("https://gitlab.finnomena.com/proj.git")).toBeUndefined();
	});
});

describe("deriveGitHubRepo", () => {
	it("truncates to owner/repo", () => {
		expect(deriveGitHubRepo("https://github.com/acme/repo.git")).toBe("acme/repo");
	});
});

describe("deriveTrackerRepo", () => {
	it("dispatches on provider", () => {
		expect(deriveTrackerRepo("https://gitlab.finnomena.com/group/sub/proj.git", "gitlab")).toBe("group/sub/proj");
		expect(deriveTrackerRepo("https://github.com/acme/repo.git", "github")).toBe("acme/repo");
	});
});

describe("deriveRepoWebURL", () => {
	it("builds a GitHub project web URL", () => {
		expect(deriveRepoWebURL("git@github.com:acme/repo.git")).toBe("https://github.com/acme/repo");
	});

	it("builds a self-hosted GitLab project web URL preserving nested groups", () => {
		expect(deriveRepoWebURL("git@gitlab.finnomena.com:group/sub/proj.git")).toBe(
			"https://gitlab.finnomena.com/group/sub/proj",
		);
	});

	it("returns undefined when no origin is known", () => {
		expect(deriveRepoWebURL(undefined)).toBeUndefined();
	});
});
