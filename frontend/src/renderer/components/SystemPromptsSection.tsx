import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Textarea } from "./ui/textarea";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type Kind = "orchestrator" | "worker" | "reviewer";
type PromptItem = { kind: Kind; default: string; override: string | null };
const systemPromptsQueryKey = ["settings", "systemPrompts"] as const;

const KIND_LABELS: Record<Kind, string> = {
	orchestrator: "Orchestrator",
	worker: "Worker",
	reviewer: "Reviewer",
};

// SystemPromptsSection is the Global Settings card for editing AO's standing
// system prompts. Each kind shows the effective global base (override else
// built-in default). Save (PUT) sets a custom global base; Reset-to-default
// (DELETE) restores the built-in. AO always injects a protected floor
// (coordination + confidentiality) and dynamic bits (git convention, spawn-confirm,
// session/project ids) on top — those are not editable here.
export function SystemPromptsSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: systemPromptsQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/prompts", {});
			if (error) throw new Error(apiErrorMessage(error));
			return (data as { prompts: PromptItem[] }).prompts;
		},
	});
	const [drafts, setDrafts] = useState<Record<string, string>>({});
	useEffect(() => {
		if (query.data) {
			setDrafts(Object.fromEntries(query.data.map((p) => [p.kind, p.override ?? p.default])));
		}
	}, [query.data]);

	const save = useMutation({
		mutationFn: async ({ kind, base }: { kind: Kind; base: string }) => {
			const { error } = await apiClient.PUT("/api/v1/settings/prompts/{kind}", {
				params: { path: { kind } },
				body: { base },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: systemPromptsQueryKey }),
	});
	const reset = useMutation({
		mutationFn: async (kind: Kind) => {
			const { error } = await apiClient.DELETE("/api/v1/settings/prompts/{kind}", { params: { path: { kind } } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: systemPromptsQueryKey }),
	});

	const items = query.data ?? [];

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">System prompts</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-5">
				<p className="text-[12px] text-muted-foreground">
					Edit the global base each session kind starts from. AO always appends a protected coordination floor,
					the confidentiality guard, and dynamic context (git convention, spawn-confirm, session and project ids) —
					those are not shown here. Use <code>{"{{.ProjectID}}"}</code> in the orchestrator base to insert the project id.
				</p>
				{items.map((p) => (
					<div key={p.kind} className="flex flex-col gap-1.5">
						<Label htmlFor={`prompt-${p.kind}`} className="text-[12px] text-muted-foreground">
							{KIND_LABELS[p.kind]}
						</Label>
						<Textarea
							id={`prompt-${p.kind}`}
							className="min-h-40 font-mono text-[12px]"
							value={drafts[p.kind] ?? ""}
							onChange={(e) => setDrafts((d) => ({ ...d, [p.kind]: e.target.value }))}
						/>
						<div className="flex items-center gap-3">
							<Button
								type="button"
								variant="primary"
								onClick={() => save.mutate({ kind: p.kind, base: drafts[p.kind] ?? "" })}
								disabled={save.isPending}
							>
								{save.isPending ? "Saving…" : "Save changes"}
							</Button>
							<Button
								type="button"
								variant="outline"
								onClick={() => reset.mutate(p.kind)}
								disabled={p.override == null || reset.isPending}
							>
								Reset to default
							</Button>
						</div>
					</div>
				))}
				{(save.isError || reset.isError) && (
					<span className="text-[12px] text-error">
						{(save.error ?? reset.error) instanceof Error ? (save.error ?? reset.error)!.message : "Save failed"}
					</span>
				)}
			</CardContent>
		</Card>
	);
}
