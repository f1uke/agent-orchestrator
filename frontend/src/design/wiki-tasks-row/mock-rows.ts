import type { WikiTaskRow } from "../../renderer/hooks/useWiki";

/**
 * Rows shaped like the ones in the real vault, because the row that looks fine
 * in a mockup is the short English one nobody has.
 *
 * Deliberately covers: a long English sentence with a `[[wikilink]]` in the
 * middle AND a `(from: …)` that duplicates its own source line; a long Thai
 * sentence that wraps to three lines; a from-tag that genuinely adds something;
 * a row with no from-tag at all; two links in one row; and a row whose note
 * basename is NOT `_tasks`, so the shortened source label is visible both ways.
 */
export const MOCK_GROUPS: { key: string; label: string; overdue?: boolean; rows: WikiTaskRow[] }[] = [
	{
		key: "overdue",
		label: "Overdue",
		overdue: true,
		rows: [
			{
				id: "r1",
				path: "Areas/mobile-development/_tasks.md",
				line: 41,
				raw: "- [ ] Draft template test case for navigate-in-app — covers external browser, in-app web view, buttons inside articles → tracked under [[STAR-2195-Navigate-In-App-Loop]] (from: My active items)",
				text: "Draft template test case for navigate-in-app — covers external browser, in-app web view, buttons inside articles → tracked under [[STAR-2195-Navigate-In-App-Loop]] (from: My active items)",
				section: "My active items",
				fromDate: "",
			},
			{
				id: "r2",
				path: "Projects/noti-hub/_tasks.md",
				line: 12,
				raw: "- [ ] ตามเรื่อง segment ของ push ที่ยิงซ้ำกับ in-app message ให้ได้ข้อสรุปก่อนรอบ regression",
				text: "ตามเรื่อง segment ของ push ที่ยิงซ้ำกับ in-app message ให้ได้ข้อสรุปก่อนรอบ regression แล้วสรุปกลับไปที่ [[Noti-Hub-Revamp]] ว่าจะตัดอันไหนออก (from: 2026-05-07 standup)",
				section: "My active items",
				fromDate: "2026-05-07",
			},
		],
	},
	{
		key: "undated",
		label: "No due date",
		rows: [
			{
				id: "r3",
				path: "Areas/mobile-development/_tasks.md",
				line: 58,
				raw: "- [ ] [@Ploy] Confirm whether the in-app web view keeps the session cookie after a cold start",
				text: "Confirm whether the in-app web view keeps the session cookie after a cold start",
				section: "My active items",
				owner: "Ploy",
				fromDate: "",
			},
			{
				id: "r4",
				path: "Projects/advisor-app/_tasks.md",
				line: 90,
				raw: "- [ ] Chase the release train: cut 4.19 off the release branch, get the MR into main, then hand [[Advisor-Release-Runbook]] to QA and make sure [[STAR-2210-Portfolio-Rework]] is not in the build (from: Release)",
				text: "Chase the release train: cut 4.19 off the release branch, get the MR into main, then hand [[Advisor-Release-Runbook]] to QA and make sure [[STAR-2210-Portfolio-Rework]] is not in the build (from: Release)",
				section: "Release",
				fromDate: "",
			},
			{
				id: "r5",
				path: "Areas/frontier/roadmap.md",
				line: 7,
				raw: "- [ ] เขียนสรุปว่าทำไมเราถึงเลือก hash router แล้วแปะไว้ใน onboarding doc",
				text: "เขียนสรุปว่าทำไมเราถึงเลือก hash router แล้วแปะไว้ใน onboarding doc",
				section: "Later",
				fromDate: "",
			},
			{
				id: "r6",
				path: "Projects/noti-hub/_tasks.md",
				line: 31,
				raw: "- [ ] Ask design whether the empty state should say anything at all (from: chat 2026-04-30, Mobility HQ)",
				text: "Ask design whether the empty state should say anything at all (from: chat 2026-04-30, Mobility HQ)",
				section: "My active items",
				fromDate: "2026-04-30",
			},
		],
	},
];
