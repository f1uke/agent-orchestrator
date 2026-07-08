import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type SpawnConfirmSettings = { enabled: boolean };
const spawnConfirmQueryKey = ["settings", "spawnConfirm"] as const;

// SpawnConfirmSection is the Global Settings card for the orchestrator's
// "confirm before spawning a worker" gate. When on, the orchestrator presents a
// confirmation summary in chat and waits for approval before running `ao spawn`.
// Daemon-backed state (GET/PUT /api/v1/settings/spawn-confirm), read when the
// orchestrator system prompt is assembled at spawn/restore.
export function SpawnConfirmSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: spawnConfirmQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/spawn-confirm", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as SpawnConfirmSettings;
		},
	});
	const [form, setForm] = useState<SpawnConfirmSettings>({ enabled: true });
	const [savedAt, setSavedAt] = useState<number | null>(null);

	useEffect(() => {
		if (query.data) setForm(query.data);
	}, [query.data]);

	const save = useMutation({
		mutationFn: async (next: SpawnConfirmSettings) => {
			const { error } = await apiClient.PUT("/api/v1/settings/spawn-confirm", { body: next });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => {
			setSavedAt(Date.now());
			void queryClient.invalidateQueries({ queryKey: spawnConfirmQueryKey });
		},
	});

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Confirm before spawning workers</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<p className="text-[12px] text-muted-foreground">
					When on, the orchestrator shows a summary — the task, the source branch, the new branch, and the pull-request
					target — and waits for your approval in chat before it runs <code>ao spawn</code>. When off, it spawns workers
					directly.
				</p>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor="spawnConfirmEnabled" className="text-[12px] text-muted-foreground">
						Confirm before spawning
					</Label>
					<Select
						value={form.enabled ? "on" : "off"}
						onValueChange={(v) => {
							setSavedAt(null);
							setForm({ enabled: v === "on" });
						}}
					>
						<SelectTrigger id="spawnConfirmEnabled" className="h-8 w-full text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							<SelectItem value="on">Enabled</SelectItem>
							<SelectItem value="off">Disabled</SelectItem>
						</SelectContent>
					</Select>
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
