import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import type { UpdateChannel, UpdateSettings } from "../../../main/update-settings";
import { updateSettingsQueryKey } from "./SystemActions";

export type PromptKind = "orchestrator" | "worker" | "reviewer";
export type PromptItem = { kind: PromptKind; default: string; override: string | null };
export type TemplateName =
	| "review-comment-dispatch"
	| "ci-failing"
	| "merge-conflict"
	| "tracker-bot-comment"
	| "ao-reviewer-batch"
	| "ao-reviewer-single";
export type TemplateItem = { name: TemplateName; default: string; placeholders: string[]; override: string | null };

const systemPromptsQueryKey = ["settings", "systemPrompts"] as const;
const messageTemplatesQueryKey = ["settings", "messageTemplates"] as const;
const spawnConfirmQueryKey = ["settings", "spawnConfirm"] as const;
const autoNudgeQueryKey = ["settings", "autoNudge"] as const;
const reclaimSettingsQueryKey = ["settings", "reclaim"] as const;

// The flat, editable Global-scope draft. Prompt/template overrides are keyed maps
// (kind/name → effective text); the rest are the daemon/app scalar settings.
export type GlobalDraft = {
	prompts: Record<string, string>;
	templates: Record<string, string>;
	spawnConfirm: boolean;
	autoNudge: boolean;
	reclaimEnabled: boolean;
	reclaimGrace: number;
	updatesEnabled: boolean;
	updateChannel: UpdateChannel;
};

export type GlobalScalarField =
	"spawnConfirm" | "autoNudge" | "reclaimEnabled" | "reclaimGrace" | "updatesEnabled" | "updateChannel";

const EMPTY_DRAFT: GlobalDraft = {
	prompts: {},
	templates: {},
	spawnConfirm: true,
	autoNudge: false,
	reclaimEnabled: true,
	reclaimGrace: 15,
	updatesEnabled: false,
	updateChannel: "latest",
};

function recordEqual(a: Record<string, string>, b: Record<string, string>): boolean {
	const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
	for (const k of keys) if (a[k] !== b[k]) return false;
	return true;
}

// useGlobalSettingsForm aggregates every editable Global setting (system prompts,
// message templates, confirm-before-spawn, auto-send, auto-reclaim, and the update
// channel/enabled) into ONE draft + dirty model, so a single save bar commits them
// all. Save FANS OUT to the several existing endpoints — only dirty items — and the
// user sees one Save (locked decision 3). Reset-to-default is folded in: an item
// whose draft equals its built-in default issues DELETE (restore built-in), else
// PUT(draft), so reset flows through the same save bar and Discard can undo it. The
// instant actions (Send test / Check for updates / Run migration) are NOT here.
export function useGlobalSettingsForm() {
	const queryClient = useQueryClient();

	const promptsQuery = useQuery({
		queryKey: systemPromptsQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/prompts", {});
			if (error) throw new Error(apiErrorMessage(error));
			return (data as { prompts: PromptItem[] }).prompts;
		},
	});
	const templatesQuery = useQuery({
		queryKey: messageTemplatesQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/message-templates", {});
			if (error) throw new Error(apiErrorMessage(error));
			return (data as { templates: TemplateItem[] }).templates;
		},
	});
	const spawnConfirmQuery = useQuery({
		queryKey: spawnConfirmQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/spawn-confirm", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as { enabled: boolean };
		},
	});
	const autoNudgeQuery = useQuery({
		queryKey: autoNudgeQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/auto-nudge", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as { enabled: boolean };
		},
	});
	const reclaimQuery = useQuery({
		queryKey: reclaimSettingsQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/reclaim", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as { enabled: boolean; graceMinutes: number };
		},
	});
	const updateQuery = useQuery({ queryKey: updateSettingsQueryKey, queryFn: () => aoBridge.updateSettings.get() });

	const [draft, setDraft] = useState<GlobalDraft>(EMPTY_DRAFT);
	const [baseline, setBaseline] = useState<GlobalDraft>(EMPTY_DRAFT);
	const [savedAt, setSavedAt] = useState<number | null>(null);
	// Seed each slice once, the first time its query resolves, into BOTH draft and
	// baseline (so it starts clean). Settings queries don't auto-refetch, so a
	// seed-once guard keeps user edits from being clobbered.
	const seeded = useRef<Set<string>>(new Set());

	useEffect(() => {
		if (!promptsQuery.data || seeded.current.has("prompts")) return;
		seeded.current.add("prompts");
		const map: Record<string, string> = {};
		for (const p of promptsQuery.data) map[p.kind] = p.override ?? p.default;
		setDraft((d) => ({ ...d, prompts: map }));
		setBaseline((b) => ({ ...b, prompts: map }));
	}, [promptsQuery.data]);

	useEffect(() => {
		if (!templatesQuery.data || seeded.current.has("templates")) return;
		seeded.current.add("templates");
		const map: Record<string, string> = {};
		for (const t of templatesQuery.data) map[t.name] = t.override ?? t.default;
		setDraft((d) => ({ ...d, templates: map }));
		setBaseline((b) => ({ ...b, templates: map }));
	}, [templatesQuery.data]);

	useEffect(() => {
		if (!spawnConfirmQuery.data || seeded.current.has("spawnConfirm")) return;
		seeded.current.add("spawnConfirm");
		const v = spawnConfirmQuery.data.enabled;
		setDraft((d) => ({ ...d, spawnConfirm: v }));
		setBaseline((b) => ({ ...b, spawnConfirm: v }));
	}, [spawnConfirmQuery.data]);

	useEffect(() => {
		if (!autoNudgeQuery.data || seeded.current.has("autoNudge")) return;
		seeded.current.add("autoNudge");
		const v = autoNudgeQuery.data.enabled;
		setDraft((d) => ({ ...d, autoNudge: v }));
		setBaseline((b) => ({ ...b, autoNudge: v }));
	}, [autoNudgeQuery.data]);

	useEffect(() => {
		if (!reclaimQuery.data || seeded.current.has("reclaim")) return;
		seeded.current.add("reclaim");
		const { enabled, graceMinutes } = reclaimQuery.data;
		setDraft((d) => ({ ...d, reclaimEnabled: enabled, reclaimGrace: graceMinutes }));
		setBaseline((b) => ({ ...b, reclaimEnabled: enabled, reclaimGrace: graceMinutes }));
	}, [reclaimQuery.data]);

	useEffect(() => {
		if (!updateQuery.data || seeded.current.has("updates")) return;
		seeded.current.add("updates");
		const { enabled, channel } = updateQuery.data;
		setDraft((d) => ({ ...d, updatesEnabled: enabled, updateChannel: channel }));
		setBaseline((b) => ({ ...b, updatesEnabled: enabled, updateChannel: channel }));
	}, [updateQuery.data]);

	const prompts = promptsQuery.data ?? [];
	const templates = templatesQuery.data ?? [];
	const promptDefault = (kind: string) => prompts.find((p) => p.kind === kind)?.default ?? "";
	const templateDefault = (name: string) => templates.find((t) => t.name === name)?.default ?? "";

	const isPromptDirty = (kind: string) => draft.prompts[kind] !== baseline.prompts[kind];
	const isTemplateDirty = (name: string) => draft.templates[name] !== baseline.templates[name];
	const isFieldDirty = (field: GlobalScalarField) => draft[field] !== baseline[field];

	const dirty =
		!recordEqual(draft.prompts, baseline.prompts) ||
		!recordEqual(draft.templates, baseline.templates) ||
		draft.spawnConfirm !== baseline.spawnConfirm ||
		draft.autoNudge !== baseline.autoNudge ||
		draft.reclaimEnabled !== baseline.reclaimEnabled ||
		draft.reclaimGrace !== baseline.reclaimGrace ||
		draft.updatesEnabled !== baseline.updatesEnabled ||
		draft.updateChannel !== baseline.updateChannel;

	const touch = () => setSavedAt(null);
	const setPrompt = (kind: string, value: string) => {
		touch();
		setDraft((d) => ({ ...d, prompts: { ...d.prompts, [kind]: value } }));
	};
	const setTemplate = (name: string, value: string) => {
		touch();
		setDraft((d) => ({ ...d, templates: { ...d.templates, [name]: value } }));
	};
	const setField = <K extends GlobalScalarField>(field: K, value: GlobalDraft[K]) => {
		touch();
		setDraft((d) => ({ ...d, [field]: value }));
	};

	const mutation = useMutation({
		mutationFn: async () => {
			const ops: Promise<void>[] = [];
			const putPrompt = async (kind: string, base: string) => {
				const { error } = await apiClient.PUT("/api/v1/settings/prompts/{kind}", {
					params: { path: { kind: kind as PromptKind } },
					body: { base },
				});
				if (error) throw new Error(apiErrorMessage(error));
			};
			const deletePrompt = async (kind: string) => {
				const { error } = await apiClient.DELETE("/api/v1/settings/prompts/{kind}", {
					params: { path: { kind: kind as PromptKind } },
				});
				if (error) throw new Error(apiErrorMessage(error));
			};
			const putTemplate = async (name: string, template: string) => {
				const { error } = await apiClient.PUT("/api/v1/settings/message-templates/{name}", {
					params: { path: { name: name as TemplateName } },
					body: { template },
				});
				if (error) throw new Error(apiErrorMessage(error));
			};
			const deleteTemplate = async (name: string) => {
				const { error } = await apiClient.DELETE("/api/v1/settings/message-templates/{name}", {
					params: { path: { name: name as TemplateName } },
				});
				if (error) throw new Error(apiErrorMessage(error));
			};

			for (const kind of Object.keys(draft.prompts)) {
				if (draft.prompts[kind] === baseline.prompts[kind]) continue;
				// draft === built-in default → restore built-in (DELETE); else set override.
				ops.push(
					draft.prompts[kind] === promptDefault(kind) ? deletePrompt(kind) : putPrompt(kind, draft.prompts[kind]),
				);
			}
			for (const name of Object.keys(draft.templates)) {
				if (draft.templates[name] === baseline.templates[name]) continue;
				ops.push(
					draft.templates[name] === templateDefault(name)
						? deleteTemplate(name)
						: putTemplate(name, draft.templates[name]),
				);
			}
			if (draft.spawnConfirm !== baseline.spawnConfirm) {
				ops.push(
					(async () => {
						const { error } = await apiClient.PUT("/api/v1/settings/spawn-confirm", {
							body: { enabled: draft.spawnConfirm },
						});
						if (error) throw new Error(apiErrorMessage(error));
					})(),
				);
			}
			if (draft.autoNudge !== baseline.autoNudge) {
				ops.push(
					(async () => {
						const { error } = await apiClient.PUT("/api/v1/settings/auto-nudge", {
							body: { enabled: draft.autoNudge },
						});
						if (error) throw new Error(apiErrorMessage(error));
					})(),
				);
			}
			if (draft.reclaimEnabled !== baseline.reclaimEnabled || draft.reclaimGrace !== baseline.reclaimGrace) {
				ops.push(
					(async () => {
						const { error } = await apiClient.PUT("/api/v1/settings/reclaim", {
							body: { enabled: draft.reclaimEnabled, graceMinutes: draft.reclaimGrace },
						});
						if (error) throw new Error(apiErrorMessage(error));
					})(),
				);
			}
			if (draft.updatesEnabled !== baseline.updatesEnabled || draft.updateChannel !== baseline.updateChannel) {
				// Selecting Nightly in Settings is itself the acknowledgement of the
				// instability warning; Stable clears it.
				const next: UpdateSettings = {
					enabled: draft.updatesEnabled,
					channel: draft.updateChannel,
					nightlyAck: draft.updateChannel === "nightly",
				};
				ops.push(aoBridge.updateSettings.set(next));
			}
			await Promise.all(ops);
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			setBaseline(draft);
			void queryClient.invalidateQueries({ queryKey: systemPromptsQueryKey });
			void queryClient.invalidateQueries({ queryKey: messageTemplatesQueryKey });
			void queryClient.invalidateQueries({ queryKey: spawnConfirmQueryKey });
			void queryClient.invalidateQueries({ queryKey: autoNudgeQueryKey });
			void queryClient.invalidateQueries({ queryKey: reclaimSettingsQueryKey });
			void queryClient.invalidateQueries({ queryKey: updateSettingsQueryKey });
		},
	});

	const discard = () => {
		setDraft(baseline);
		setSavedAt(null);
	};

	return {
		prompts,
		templates,
		draft,
		promptDefault,
		templateDefault,
		isPromptDirty,
		isTemplateDirty,
		isFieldDirty,
		dirty,
		setPrompt,
		setTemplate,
		setField,
		mutation,
		savedAt,
		save: () => mutation.mutate(),
		discard,
	};
}
