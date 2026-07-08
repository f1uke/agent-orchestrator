import { useState } from "react";
import { aoBridge } from "../lib/bridge";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";

// NotificationsSection is the Global Settings card for verifying native OS
// notifications. The "Send test notification" button posts a notification down
// the exact same path a real one takes (renderer → aoBridge.notifications.show →
// IPC "notifications:show" → main nativeNotifier), but bypasses the SSE +
// unread-cache dedup that gates real notifications. That makes it a reliable way
// to confirm a macOS banner appears — including while the app window is focused,
// which is the case that regressed (see bugfix/native-noti-always-fire).
export function NotificationsSection() {
	const [sentAt, setSentAt] = useState<number | null>(null);

	const sendTest = () => {
		// Unique id per click so repeats are not collapsed onto one visible toast
		// by the main-process notifier (which closes a prior toast with the same id).
		const id = `test-notification-${crypto.randomUUID()}`;
		void aoBridge.notifications.show({
			id,
			title: "Agent Orchestrator",
			body: "Test notification — if you can see this banner, native notifications are working.",
		});
		setSentAt(Date.now());
	};

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Notifications</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-3">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Send a test banner to confirm macOS notifications are working. The banner should appear whether or not the
					Agent Orchestrator window is focused.
				</p>
				<div className="flex items-center gap-3">
					<Button type="button" variant="outline" onClick={sendTest}>
						Send test notification
					</Button>
					{sentAt ? <span className="text-[12px] text-muted-foreground">Sent. Look for a banner.</span> : null}
				</div>
			</CardContent>
		</Card>
	);
}
