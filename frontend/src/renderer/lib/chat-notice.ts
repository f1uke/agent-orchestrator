import type { NoticeItem } from "./chat-fold";
import { describeTurnError } from "./chat-turn-error";

const shortId = (id: string) => (id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id);

export function noticeText(notice: NoticeItem): string {
	switch (notice.notice) {
		case "spawn":
			return "Session started";
		case "resumed":
			return `Resumed claude session ${shortId(notice.text)}`;
		case "exit":
			return `Claude exited · ${notice.text}`;
		case "compacted":
			return "Conversation compacted";
		case "interrupted":
			return "Interrupted";
		case "rewound":
			return notice.text === "files" ? "Rewound here · files restored" : "Rewound here · later messages dropped";
		case "api_error":
			return describeTurnError("", notice.text).label;
	}
}
