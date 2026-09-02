import { apiClient, apiErrorMessage } from "./api-client";

/**
 * Ending a session, and the one way it can be refused.
 *
 * The daemon will not tear down a worktree that still holds work no pull request
 * carries. That refusal used to be invisible - a 200 with `freed:false`, over a
 * row nothing had touched - so the board's "Move to Done" closed its menu,
 * refetched, and left the card exactly where it was with nothing said.
 *
 * Now it is a 409 carrying the FILES, and this module is the one place that
 * turns it back into something a component can render: `UndeliveredWorkError`
 * with the list. Every surface that ends a session goes through here so none of
 * them can quietly grow its own idea of what success means.
 */

/** One file in a worktree that ending the session would destroy. */
export type UncommittedFile = {
	path: string;
	/** modified | added | deleted | renamed | untracked | conflicted | changed */
	status: string;
};

/** The daemon's machine code for "this session still holds undelivered work". */
export const UNDELIVERED_WORK_CODE = "SESSION_HAS_UNDELIVERED_WORK";

/**
 * The refusal, as an Error a mutation can throw and a dialog can read. It
 * carries the daemon's own sentence AND the file list, because a person deciding
 * whether to throw work away is deciding about the files, not about a count.
 */
export class UndeliveredWorkError extends Error {
	readonly files: UncommittedFile[];

	constructor(message: string, files: UncommittedFile[]) {
		super(message);
		this.name = "UndeliveredWorkError";
		this.files = files;
	}
}

export type KillResult = {
	/** The session ended. Not implied by `freed`, and not the same question. */
	terminated: boolean;
	/** The worktree is gone from disk. */
	freed: boolean;
	/** What a deliberate discard destroyed, and where it was captured first. */
	discarded: UncommittedFile[];
	preservedRef: string;
};

/**
 * End a session.
 *
 * Without `discardUncommitted` this is also the PREVIEW: it destroys nothing on
 * a session holding undelivered work, and the refusal it throws carries the list
 * to show before asking again. That is why the dialog can promise a person they
 * are seeing what they are about to lose.
 */
export async function killSession(
	sessionId: string,
	options: { discardUncommitted?: boolean } = {},
): Promise<KillResult> {
	const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/kill", {
		params: { path: { sessionId } },
		body: { discardUncommitted: options.discardUncommitted ?? false },
	});
	if (error) {
		const files = undeliveredWorkFrom(error);
		if (files) throw new UndeliveredWorkError(apiErrorMessage(error, "Unable to end this session"), files);
		throw new Error(apiErrorMessage(error, "Unable to end this session"));
	}
	return {
		terminated: data?.terminated ?? false,
		freed: data?.freed ?? false,
		discarded: (data?.discarded ?? []).map((f) => ({ path: f.path, status: f.status })),
		preservedRef: data?.preservedRef ?? "",
	};
}

/**
 * Recognise the refusal and pull its file list out of the error envelope's
 * `details`. Returns null for every other failure, so a caller can tell "the
 * daemon deliberately refused" from "something went wrong" - the distinction the
 * old 200 destroyed.
 *
 * A refusal whose details are missing or malformed still returns a list (empty):
 * it IS the refusal, and the dialog says so rather than reporting a generic
 * error over a session it knows perfectly well why it cannot end.
 */
export function undeliveredWorkFrom(error: unknown): UncommittedFile[] | null {
	if (error instanceof UndeliveredWorkError) return error.files;
	if (typeof error !== "object" || error === null) return null;
	const body = error as { code?: unknown; details?: unknown };
	if (body.code !== UNDELIVERED_WORK_CODE) return null;
	const details = body.details as { files?: unknown } | undefined;
	if (!Array.isArray(details?.files)) return [];
	return details.files.flatMap((entry): UncommittedFile[] => {
		if (typeof entry !== "object" || entry === null) return [];
		const file = entry as { path?: unknown; status?: unknown };
		if (typeof file.path !== "string" || file.path === "") return [];
		return [{ path: file.path, status: typeof file.status === "string" ? file.status : "changed" }];
	});
}
