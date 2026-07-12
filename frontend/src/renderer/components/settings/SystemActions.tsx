import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { migrationOfferQueryKey } from "../../hooks/useMigrationOffer";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import type { MigrationState, MigrationStatus } from "../../../main/app-state";
import type { UpdateStatus } from "../../../main/update-settings";
import { Button } from "../ui/button";

// The instant Global-scope actions. These are the TRUE actions of the System
// section (locked decision 3): Send test notification, Check for updates / Update
// / Restart, and Run migration. They fire immediately and are deliberately kept
// OUT of the save bar — they don't stage a draft, they act. The query keys for
// the update + migration state live here (co-located with the components that own
// that state) so the aggregating form hook can invalidate them after a save.
export const updateSettingsQueryKey = ["update-settings"] as const;
export const migrationSettingsQueryKey = ["migration-settings"] as const;

// NotificationsControls posts a native notification down the exact same path a
// real one takes (renderer → aoBridge.notifications.show → IPC → main
// nativeNotifier) but bypasses the SSE/unread dedup, so it reliably surfaces a
// macOS banner even while the window is focused — the case that regressed.
export function NotificationsControls() {
	const [sentAt, setSentAt] = useState<number | null>(null);

	const sendTest = () => {
		// Unique id per click so repeats aren't collapsed onto one visible toast by
		// the main-process notifier (which closes a prior toast with the same id).
		const id = `test-notification-${crypto.randomUUID()}`;
		void aoBridge.notifications.show({
			id,
			title: "Agent Orchestrator",
			body: "Test notification — if you can see this banner, native notifications are working.",
		});
		setSentAt(Date.now());
	};

	return (
		<div className="flex items-center gap-3">
			<Button type="button" variant="outline" onClick={sendTest}>
				Send test notification
			</Button>
			{sentAt ? <span className="text-[12px] text-muted-foreground">Sent. Look for a banner.</span> : null}
		</div>
	);
}

// UpdateActions is the on-demand update control: a Check for updates button plus
// an Update button that downloads then installs. It works even when automatic
// updates are off, so users who never opted in can still pull the latest build.
export function UpdateActions() {
	const [status, setStatus] = useState<UpdateStatus>({ state: "idle" });
	const version = useQuery({ queryKey: ["app-version"], queryFn: () => aoBridge.app.getVersion() });

	useEffect(() => {
		let live = true;
		void aoBridge.updates.getStatus().then((s) => {
			if (live) setStatus(s);
		});
		const off = aoBridge.updates.onStatus(setStatus);
		return () => {
			live = false;
			off?.();
		};
	}, []);

	const checking = status.state === "checking";
	const downloading = status.state === "downloading";
	const busy = checking || downloading;

	return (
		<div className="flex flex-col gap-3">
			<div className="flex items-center gap-2 text-[12px]">
				<span className="text-passive">Current version</span>
				<span className="font-mono text-[11px] text-foreground">{version.data ? `v${version.data}` : "…"}</span>
			</div>
			<div className="flex items-center gap-3">
				<Button type="button" variant="outline" onClick={() => void aoBridge.updates.check()} disabled={busy}>
					{checking && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
					Check for updates
				</Button>

				{status.state === "available" && (
					<Button type="button" variant="primary" onClick={() => void aoBridge.updates.download()}>
						Update to {status.version ? `v${status.version}` : "latest"}
					</Button>
				)}
				{status.state === "downloaded" && (
					<Button type="button" variant="primary" onClick={() => void aoBridge.updates.install()}>
						Restart &amp; install
					</Button>
				)}

				<UpdateStatusLine status={status} />
			</div>
		</div>
	);
}

function UpdateStatusLine({ status }: { status: UpdateStatus }) {
	switch (status.state) {
		case "checking":
			return <span className="text-[12px] text-muted-foreground">Checking for updates…</span>;
		case "available":
			return (
				<span className="text-[12px] text-muted-foreground">
					Update available{status.version ? ` (v${status.version})` : ""}.
				</span>
			);
		case "downloading":
			return <span className="text-[12px] text-muted-foreground">Downloading… {status.percent ?? 0}%</span>;
		case "downloaded":
			return <span className="text-[12px] text-success">Downloaded. Restart to finish updating.</span>;
		case "not-available":
			return <span className="text-[12px] text-muted-foreground">You're on the latest version.</span>;
		case "unsupported":
			return <span className="text-[12px] text-passive">{status.message ?? "Updates need the installed app."}</span>;
		case "error":
			return <span className="text-[12px] text-error">{status.message ?? "Update failed."}</span>;
		default:
			return null;
	}
}

interface MigrationView {
	migration: MigrationState;
	available: boolean;
	legacyRoot: string;
}

// fetchMigrationSettings reads the persisted decision (app marker) and asks the
// daemon whether legacy data is present. Unlike useMigrationOffer it never
// short-circuits on a terminal status: Settings always shows the full state so a
// user who declined or already completed can re-run. A 501/unreachable daemon
// resolves to "not available", never an error.
async function fetchMigrationSettings(): Promise<MigrationView> {
	const migration = await aoBridge.appState.getMigration();
	const { data, error } = await apiClient.GET("/api/v1/import");
	return {
		migration,
		available: !error && (data?.available ?? false),
		legacyRoot: data?.legacyRoot ?? "",
	};
}

const STATUS_LABEL: Record<MigrationStatus, string> = {
	pending: "Not migrated yet",
	completed: "Completed",
	declined: "Declined",
	failed: "Last attempt failed",
};

function statusClass(status: MigrationStatus): string {
	switch (status) {
		case "completed":
			return "text-success";
		case "failed":
			return "text-error";
		default:
			return "text-muted-foreground";
	}
}

function formatTime(iso?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? "" : d.toLocaleString();
}

// MigrationControls re-runs the legacy-AO import. It reads the persisted
// migration decision + the daemon's availability, shows the last report/error,
// and exposes a Run / Re-run button that calls the idempotent POST
// /api/v1/import (safe even when completed/declined/failed). Issue #2205.
export function MigrationControls() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: migrationSettingsQueryKey,
		queryFn: fetchMigrationSettings,
	});

	const run = useMutation({
		mutationFn: async () => {
			const nowIso = () => new Date().toISOString();
			const { data, error } = await apiClient.POST("/api/v1/import");
			if (error) {
				const msg = apiErrorMessage(error);
				await aoBridge.appState.setMigration({ status: "failed", lastAttemptAt: nowIso(), error: msg });
				throw new Error(msg);
			}
			const report = data?.report;
			await aoBridge.appState.setMigration({
				status: "completed",
				lastAttemptAt: nowIso(),
				completedAt: nowIso(),
				report: report
					? { projectsImported: report.projectsImported, projectsSkipped: report.projectsSkipped }
					: undefined,
			});
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: migrationSettingsQueryKey });
			void queryClient.invalidateQueries({ queryKey: migrationOfferQueryKey });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});

	const migration = query.data?.migration ?? { status: "pending" as MigrationStatus };
	const available = query.data?.available ?? false;
	const legacyRoot = query.data?.legacyRoot ?? "";
	const report = migration.report;
	const completed = migration.status === "completed";
	const buttonLabel = run.isPending
		? "Running…"
		: completed
			? "Re-run migration"
			: migration.status === "failed"
				? "Retry migration"
				: "Run migration";

	return (
		<div className="flex flex-col gap-4">
			<div className="flex flex-col gap-2 text-[12px]">
				<Row label="Status">
					<span className={statusClass(migration.status)}>{STATUS_LABEL[migration.status]}</span>
				</Row>
				{formatTime(migration.completedAt || migration.lastAttemptAt) && (
					<Row label={completed ? "Completed" : "Last attempt"}>
						<span className="text-foreground">{formatTime(migration.completedAt || migration.lastAttemptAt)}</span>
					</Row>
				)}
				{report && (
					<Row label="Last report">
						<span className="text-foreground">
							{report.projectsImported} imported, {report.projectsSkipped} already present
						</span>
					</Row>
				)}
				<Row label="Legacy install">
					{query.isLoading ? (
						<span className="text-passive">Checking…</span>
					) : available ? (
						<span className="font-mono text-[11px] text-foreground">{legacyRoot || "found"}</span>
					) : (
						<span className="text-passive">None found</span>
					)}
				</Row>
			</div>

			{migration.status === "failed" && migration.error && (
				<p className="text-[12px] leading-5 text-error">
					{migration.error}. Your legacy projects are untouched (nothing is ever deleted).
				</p>
			)}
			{run.isError && (
				<p className="text-[12px] leading-5 text-error">
					{run.error instanceof Error ? run.error.message : "Migration failed."}
				</p>
			)}
			{run.isSuccess && !run.isPending && <p className="text-[12px] leading-5 text-success">Migration complete.</p>}

			<div className="flex items-center gap-3">
				<Button
					type="button"
					variant="primary"
					onClick={() => run.mutate()}
					disabled={run.isPending || (!available && !completed)}
				>
					{run.isPending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
					{buttonLabel}
				</Button>
				{!available && !query.isLoading && (
					<span className="text-[12px] text-passive">Nothing to import from a legacy install.</span>
				)}
			</div>
		</div>
	);
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="flex items-center gap-3">
			<span className="w-28 shrink-0 text-passive">{label}</span>
			<span className="min-w-0 flex-1 truncate">{children}</span>
		</div>
	);
}
