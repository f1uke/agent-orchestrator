import { useCallback, useEffect, useMemo, useState } from "react";
import type { AnswerBody, PermissionMode } from "../lib/chat-answers";
import { type ChatApi, type ChatImage, type ChatResult, httpChatApi } from "../lib/chat-api";
import { stubChatApi } from "../lib/chat-mock";
import type { ChatConnection } from "./useChatSession";

export type ChatActions = {
	send: (text: string, images: readonly ChatImage[]) => Promise<ChatResult>;
	answer: (body: AnswerBody) => Promise<ChatResult>;
	interrupt: () => Promise<ChatResult>;
	setMode: (mode: PermissionMode) => Promise<ChatResult>;
	setModel: (model: string) => Promise<ChatResult>;
	hostDown: boolean;
};

const defaultApi: ChatApi = import.meta.env.VITE_NO_ELECTRON === "1" ? stubChatApi : httpChatApi;

export function useChatActions(sessionId: string, connection: ChatConnection, api: ChatApi = defaultApi): ChatActions {
	const [hostDown, setHostDown] = useState(false);

	useEffect(() => {
		if (connection === "live") setHostDown(false);
	}, [connection]);

	const run = useCallback(async (call: Promise<ChatResult>) => {
		const result = await call;
		if (!result.ok && result.error.kind === "host_not_running") setHostDown(true);
		return result;
	}, []);

	return useMemo(
		() => ({
			send: (text, images) => run(api.send(sessionId, text, images)),
			answer: (body) => run(api.answer(sessionId, body)),
			interrupt: () => run(api.interrupt(sessionId)),
			setMode: (mode) => run(api.setMode(sessionId, mode)),
			setModel: (model) => run(api.setModel(sessionId, model)),
			hostDown,
		}),
		[api, hostDown, run, sessionId],
	);
}
