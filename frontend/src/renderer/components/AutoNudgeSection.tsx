import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Switch } from "./ui/switch";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type AutoNudgeSettings = { enabled: boolean };
const autoNudgeQueryKey = ["settings", "autoNudge"] as const;

// AutoNudgeSection is the Global Settings card for the GLOBAL default of the
// "auto-nudge the worker when its PR has unresolved review comments" feature.
// When on, a session whose pull request receives an unresolved review comment
// (or a changes-requested review) automatically nudges its worker. This is the
// default for new sessions; each session can override it from its Comments tab.
// Daemon-backed state (GET/PUT /api/v1/settings/auto-nudge). Unlike
// SpawnConfirmSection, the toggle saves immediately on change — no Save button.
export function AutoNudgeSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: autoNudgeQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/auto-nudge", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as AutoNudgeSettings;
		},
	});
	const [form, setForm] = useState<AutoNudgeSettings>({ enabled: false });
	const [savedAt, setSavedAt] = useState<number | null>(null);

	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	const save = useMutation({
		mutationFn: async (next: AutoNudgeSettings) => {
			const { error } = await apiClient.PUT("/api/v1/settings/auto-nudge", { body: next });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: autoNudgeQueryKey });
		},
		onError: () => {
			// Revert the optimistic toggle — the query's last-known-good value wins.
			if (query.data) setForm(query.data);
		},
	});

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Auto-send unresolved PR comments to the worker</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<p className="text-[12px] text-muted-foreground">
					When on, a session whose pull request gets an unresolved review comment (or a changes-requested review)
					automatically nudges its worker. This is the default for new sessions — each session can override it from its
					Reviews tab.
				</p>
				<div className="flex items-center gap-3">
					<Switch
						id="autoNudgeEnabled"
						checked={form.enabled}
						onCheckedChange={(checked) => {
							setSavedAt(null);
							setForm({ enabled: checked });
							save.mutate({ enabled: checked });
						}}
					/>
					<Label htmlFor="autoNudgeEnabled" className="text-[12px] text-muted-foreground">
						Enabled by default
					</Label>
					{save.isError && (
						<span className="text-[12px] text-error">
							{save.error instanceof Error ? save.error.message : "Save failed"}
						</span>
					)}
					{savedAt && !save.isPending && !save.isError && <span className="text-[12px] text-success">Saved.</span>}
				</div>
			</CardContent>
		</Card>
	);
}
