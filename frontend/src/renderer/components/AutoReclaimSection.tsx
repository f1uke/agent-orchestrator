import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type ReclaimSettings = { enabled: boolean; graceMinutes: number };
const reclaimSettingsQueryKey = ["settings", "reclaim"] as const;

// AutoReclaimSection is the Global Settings card for the daemon's auto-reclaim
// loop: once a session is merged or terminated, AO tears down its tmux and
// worktree after a grace period (the git branch survives so it can be
// restored). Unlike UpdatesSection this is daemon-backed state (GET/PUT
// /api/v1/settings/reclaim via apiClient), not the Electron bridge — the
// daemon's reclaim loop is the sole reader of this setting.
export function AutoReclaimSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: reclaimSettingsQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/reclaim", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as ReclaimSettings;
		},
	});
	const [form, setForm] = useState<ReclaimSettings>({ enabled: true, graceMinutes: 15 });
	const [savedAt, setSavedAt] = useState<number | null>(null);

	// Seed the form once settings load (and on refetch), same pattern as
	// UpdatesSection: keying off the loaded value keeps local edits responsive
	// without a controlled-from-query loop.
	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	const save = useMutation({
		mutationFn: async (next: ReclaimSettings) => {
			const { error } = await apiClient.PUT("/api/v1/settings/reclaim", { body: next });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: reclaimSettingsQueryKey });
		},
	});

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Auto-reclaim finished sessions</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<p className="text-[12px] text-muted-foreground">
					When a session is merged or terminated, AO tears down its tmux and worktree after the grace period. The git
					branch is kept, so the session can still be restored.
				</p>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="reclaimEnabled" className="text-[12px] text-muted-foreground">
						Auto-reclaim
					</Label>
					<Select
						value={form.enabled ? "on" : "off"}
						onValueChange={(v) => {
							setSavedAt(null);
							setForm((f) => ({ ...f, enabled: v === "on" }));
						}}
					>
						<SelectTrigger id="reclaimEnabled" className="h-8 w-full text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="on">Enabled</SelectItem>
							<SelectItem value="off">Disabled</SelectItem>
						</SelectContent>
					</Select>
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="reclaimGrace" className="text-[12px] text-muted-foreground">
						Grace period (minutes)
					</Label>
					<input
						id="reclaimGrace"
						type="number"
						min={0}
						className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
						value={form.graceMinutes}
						onChange={(e) => {
							setSavedAt(null);
							setForm((f) => ({ ...f, graceMinutes: Math.max(0, Number(e.target.value) || 0) }));
						}}
					/>
				</div>
				<div className="flex items-center gap-3">
					<Button type="button" variant="primary" onClick={() => save.mutate(form)} disabled={save.isPending}>
						{save.isPending ? "Saving…" : "Save changes"}
					</Button>
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
