import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Textarea } from "./ui/textarea";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type TemplateName =
	| "review-comment-dispatch"
	| "ci-failing"
	| "merge-conflict"
	| "tracker-bot-comment"
	| "ao-reviewer-batch"
	| "ao-reviewer-single";
type TemplateItem = { name: TemplateName; default: string; placeholders: string[]; override: string | null };
const messageTemplatesQueryKey = ["settings", "messageTemplates"] as const;

// MessageTemplatesSection is the Global Settings card for editing the runtime
// nudge messages AO sends into a worker's pane (CI failing, review feedback,
// merge conflict, tracker-bot, AO reviewer). Each shows the effective text
// (override else built-in default) and its documented placeholders. Save (PUT)
// sets a custom override; Reset-to-default (DELETE) restores the built-in.
export function MessageTemplatesSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: messageTemplatesQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/message-templates", {});
			if (error) throw new Error(apiErrorMessage(error));
			return (data as { templates: TemplateItem[] }).templates;
		},
	});
	const [drafts, setDrafts] = useState<Record<string, string>>({});
	const serverSnapshot = useRef<Record<string, string>>({});
	useEffect(() => {
		if (!query.data) return;
		setDrafts((prev) => {
			const next = { ...prev };
			for (const t of query.data) {
				const serverValue = t.override ?? t.default;
				const isDirty = prev[t.name] !== undefined && prev[t.name] !== serverSnapshot.current[t.name];
				if (!isDirty) next[t.name] = serverValue;
				serverSnapshot.current[t.name] = serverValue;
			}
			return next;
		});
	}, [query.data]);

	const save = useMutation({
		mutationFn: async ({ name, template }: { name: TemplateName; template: string }) => {
			const { error } = await apiClient.PUT("/api/v1/settings/message-templates/{name}", {
				params: { path: { name } },
				body: { template },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: messageTemplatesQueryKey }),
	});
	const reset = useMutation({
		mutationFn: async (name: TemplateName) => {
			const { error } = await apiClient.DELETE("/api/v1/settings/message-templates/{name}", { params: { path: { name } } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: messageTemplatesQueryKey }),
	});

	const items = query.data ?? [];

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Message templates</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-5">
				<p className="text-[12px] text-muted-foreground">
					Edit the runtime messages AO sends into a worker's terminal. Dynamic values are inserted via the listed
					placeholders (Go text/template). A bad edit falls back to the built-in default.
				</p>
				{items.map((t) => (
					<div key={t.name} className="flex flex-col gap-1.5">
						<Label htmlFor={`template-${t.name}`} className="text-[12px] text-muted-foreground">
							{t.name}
						</Label>
						{t.placeholders.length > 0 && (
							<span className="text-[11px] text-muted-foreground">
								Placeholders: <code>{t.placeholders.join(" ")}</code>
							</span>
						)}
						<Textarea
							id={`template-${t.name}`}
							className="min-h-28 font-mono text-[12px]"
							value={drafts[t.name] ?? ""}
							onChange={(e) => setDrafts((d) => ({ ...d, [t.name]: e.target.value }))}
						/>
						<div className="flex items-center gap-3">
							<Button
								type="button"
								variant="primary"
								onClick={() => save.mutate({ name: t.name, template: drafts[t.name] ?? "" })}
								disabled={save.isPending}
							>
								{save.isPending ? "Saving…" : "Save changes"}
							</Button>
							<Button
								type="button"
								variant="outline"
								onClick={() => reset.mutate(t.name)}
								disabled={t.override == null || reset.isPending}
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
