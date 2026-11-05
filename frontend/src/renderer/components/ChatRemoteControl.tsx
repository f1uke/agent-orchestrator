import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { aoBridge } from "../lib/bridge";
import type { RemoteControl } from "../lib/chat-fold";
import { qrCode, REMOTE_LABEL } from "../lib/chat-remote";
import { COPIED_FEEDBACK_MS } from "./CopyButton";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { SimpleTooltip } from "./ui/tooltip";

function Qr({ url }: { url: string }) {
	const { size, path } = qrCode(url);
	return (
		<div className="chat-remote__qr">
			<svg
				aria-label="QR code for the session link"
				role="img"
				shapeRendering="crispEdges"
				viewBox={`0 0 ${size} ${size}`}
			>
				<path d={path} fill="currentColor" />
			</svg>
		</div>
	);
}

function CopyLink({ url }: { url: string }) {
	const [copied, setCopied] = useState(false);
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(timer.current), []);
	const copy = () =>
		void aoBridge.clipboard
			.writeText(url)
			.then(() => {
				setCopied(true);
				clearTimeout(timer.current);
				timer.current = setTimeout(() => setCopied(false), COPIED_FEEDBACK_MS);
			})
			.catch((error) => console.warn("Unable to copy the Remote Control link", error));
	return (
		<Button onClick={copy} size="sm" variant="secondary">
			{copied ? <Check className="chat-icon" aria-hidden="true" /> : <Copy className="chat-icon" aria-hidden="true" />}
			{copied ? "Copied" : "Copy link"}
		</Button>
	);
}

/**
 * The Remote Control status in the chat's status line: a chip while the bridge is
 * not off. With a session URL it opens a popover to copy the link or scan it from a
 * phone. A failed bridge shows claude's reason as its tooltip.
 */
export function RemoteControlChip({ remote }: { remote: RemoteControl }) {
	if (remote.state === "off") return null;
	const label = REMOTE_LABEL[remote.state];
	const chip = (
		<>
			<span className="chat-statusline__dot" data-remote={remote.state} />
			{label}
		</>
	);
	if (remote.state === "failed") {
		const reason = remote.detail || "Claude gave no reason";
		return (
			<SimpleTooltip label={reason}>
				<span aria-label={`${label}: ${reason}`} className="chat-remote" data-remote={remote.state} tabIndex={0}>
					{chip}
				</span>
			</SimpleTooltip>
		);
	}
	if (remote.url === "") {
		return (
			<span className="chat-remote" data-remote={remote.state}>
				{chip}
			</span>
		);
	}
	return (
		<Popover>
			<PopoverTrigger aria-label={label} className="chat-remote chat-meter" data-remote={remote.state} type="button">
				{chip}
			</PopoverTrigger>
			<PopoverContent aria-label="Remote Control" align="start" className="chat-meter-pop chat-remote__pop" side="top">
				<p className="chat-remote__lead">Control this session from claude.ai/code or the Claude app.</p>
				<Qr url={remote.url} />
				<code className="chat-remote__url">{remote.url}</code>
				<CopyLink url={remote.url} />
			</PopoverContent>
		</Popover>
	);
}
