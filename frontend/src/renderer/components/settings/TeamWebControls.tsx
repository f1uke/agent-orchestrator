import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { useApiReady } from "../../hooks/useApiReady";
import type { components } from "../../../api/schema";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";

export type TeamWebStatus = components["schemas"]["ReleaseWebStatusResponse"];

export const teamWebQueryKey = ["release-web"] as const;
const POLL_MS = 5_000;

async function must<T>(call: Promise<{ data?: T; error?: unknown }>): Promise<T> {
	const { data, error } = await call;
	if (error !== undefined || data === undefined) throw new Error(apiErrorMessage(error));
	return data;
}

export function useTeamWebStatus() {
	const ready = useApiReady();
	return useQuery({
		queryKey: teamWebQueryKey,
		enabled: ready,
		queryFn: () => must(apiClient.GET("/api/v1/release-web")),
		refetchInterval: POLL_MS,
		refetchIntervalInBackground: false,
	});
}

export function TeamWebControls() {
	const queryClient = useQueryClient();
	const status = useTeamWebStatus();
	const [failure, setFailure] = useState<string | null>(null);
	const [confirmingRotate, setConfirmingRotate] = useState(false);
	const [copied, setCopied] = useState<"key" | "address" | null>(null);

	const fresh = (next: TeamWebStatus) => {
		queryClient.setQueryData(teamWebQueryKey, next);
		void queryClient.invalidateQueries({ queryKey: teamWebQueryKey });
	};
	const onMutate = () => {
		setFailure(null);
		setCopied(null);
	};
	const onError = (e: Error) => setFailure(e.message);

	const start = useMutation({
		mutationFn: () => must(apiClient.POST("/api/v1/release-web/start")),
		onMutate,
		onSuccess: fresh,
		onError,
	});
	const stop = useMutation({
		mutationFn: () => must(apiClient.POST("/api/v1/release-web/stop")),
		onMutate,
		onSuccess: fresh,
		onError,
	});
	const rotate = useMutation({
		mutationFn: () => must(apiClient.POST("/api/v1/release-web/rotate-key")),
		onMutate: () => {
			onMutate();
			setConfirmingRotate(false);
		},
		onSuccess: fresh,
		onError,
	});
	const copyKey = useMutation({
		mutationFn: () => must(apiClient.POST("/api/v1/release-web/copy-key")),
		onMutate,
		onSuccess: () => setCopied("key"),
		onError,
	});

	const web = status.data;
	const running = web?.running === true;
	const busy = start.isPending || stop.isPending || rotate.isPending;
	const checked = start.isPending ? true : stop.isPending ? false : running;

	const copyAddress = () => {
		if (!web?.url) return;
		void navigator.clipboard.writeText(web.url).then(
			() => setCopied("address"),
			() => setFailure("The browser refused to copy the address."),
		);
	};

	return (
		<div className="flex max-w-[62ch] flex-col gap-3.5">
			<div className="flex items-center gap-3">
				<Switch
					id="teamWebEnabled"
					checked={checked}
					disabled={web === undefined || busy}
					onCheckedChange={(on) => (on ? start.mutate() : stop.mutate())}
				/>
				<label htmlFor="teamWebEnabled" className="text-[12px] text-muted-foreground">
					{start.isPending ? "Starting" : stop.isPending ? "Stopping" : running ? "Running" : "Stopped"}
				</label>
			</div>

			{running && web?.url && (
				<div className="flex flex-col gap-1.5 text-[12px]">
					<div className="flex flex-wrap items-center gap-2">
						<span className="text-passive">Local address</span>
						<span className="font-mono text-[11.5px] text-foreground">{web.url}</span>
						<Button type="button" size="sm" variant="outline" onClick={copyAddress}>
							{copied === "address" ? "Copied" : "Copy address"}
						</Button>
					</div>
					<div className="flex items-center gap-2">
						<span className="text-passive">Signed in</span>
						<span className="font-mono text-[11.5px] text-foreground">{web.sessions}</span>
					</div>
				</div>
			)}

			{web && (
				<div className="flex flex-wrap items-center gap-2 text-[12px]">
					<span className="text-passive">Public hosts</span>
					{web.hosts.length > 0 ? (
						<span className="font-mono text-[11.5px] text-foreground">{web.hosts.join(", ")}</span>
					) : (
						<span className="text-[11.5px] text-passive">
							None. Add yours with ao release-web start --host &lt;machine&gt;.&lt;tailnet&gt;.ts.net
						</span>
					)}
				</div>
			)}

			<div className="flex flex-wrap items-center gap-2">
				<Button
					type="button"
					variant="outline"
					disabled={!web?.keyPresent || copyKey.isPending}
					onClick={() => copyKey.mutate()}
				>
					{copied === "key" ? "Key copied" : "Copy key"}
				</Button>
				{confirmingRotate ? (
					<>
						<span className="text-[12px] text-warning">Everyone signed in is signed out. Rotate?</span>
						<Button type="button" variant="primary" disabled={rotate.isPending} onClick={() => rotate.mutate()}>
							Rotate key
						</Button>
						<Button type="button" variant="ghost" onClick={() => setConfirmingRotate(false)}>
							Cancel
						</Button>
					</>
				) : (
					<Button type="button" variant="outline" disabled={busy} onClick={() => setConfirmingRotate(true)}>
						Rotate key
					</Button>
				)}
			</div>

			{web?.keyPresent === false && !running && (
				<p className="text-[11.5px] text-passive">No key yet. Turning the switch on makes one.</p>
			)}
			{(failure ?? (status.isError ? apiErrorMessage(status.error) : null)) && (
				<p role="alert" className="text-[11.5px] leading-[1.5] [overflow-wrap:anywhere] text-error">
					{failure ?? apiErrorMessage(status.error)}
				</p>
			)}
			<p className="text-[11.5px] leading-[1.6] text-passive">
				Putting this on the internet is done with Tailscale Funnel, by you. The switch controls only the web program.
			</p>
		</div>
	);
}
