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
 * The daemon's machine code for "this worker's subagents hold work its branch
 * does not have yet". A discard does not lose that work: each subagent's work
 * is committed onto its own branch, which is kept.
 */
export const UNMERGED_CHILDREN_CODE = "SESSION_HAS_UNMERGED_CHILDREN";

/** One subagent of the worker whose work has not reached its branch. */
export type UndeliveredChild = {
	agentId: string;
	description: string;
	state: string;
	branch: string;
	detail: string;
};

/**
 * The daemon's machine code for "this workspace's scripts store worktree holds
 * work the store does not have": files nobody committed there, or commits the
 * store's main checkout refused. A discard deletes that worktree and its branch.
 */
export const UNPUBLISHED_SCRIPTS_CODE = "SESSION_HAS_UNPUBLISHED_SCRIPTS";

/** The workspace's scripts store worktree, as a refused kill names it. */
export type ScriptsStoreRefusal = {
	path: string;
	branch: string;
	baseBranch: string;
	/** Files in the worktree nobody committed. */
	uncommitted: string[];
	/** Set when teardown's publish was refused: why, and the files it named. */
	publishRefused?: { hold: string; detail: string; files: string[] };
};

/**
 * Everything a refused kill named: the worktree's files, the subagents' work,
 * and the scripts store worktree's.
 */
export type UndeliveredWork = {
	files: UncommittedFile[];
	subagents: UndeliveredChild[];
	scriptsStore?: ScriptsStoreRefusal;
};

/**
 * The refusal, as an Error a mutation can throw and a dialog can read. It
 * carries the daemon's own sentence AND the file list, because a person deciding
 * whether to throw work away is deciding about the files, not about a count.
 */
export class UndeliveredWorkError extends Error {
	readonly files: UncommittedFile[];
	readonly subagents: UndeliveredChild[];
	readonly scriptsStore?: ScriptsStoreRefusal;

	constructor(
		message: string,
		files: UncommittedFile[],
		subagents: UndeliveredChild[] = [],
		scriptsStore?: ScriptsStoreRefusal,
	) {
		super(message);
		this.name = "UndeliveredWorkError";
		this.files = files;
		this.subagents = subagents;
		this.scriptsStore = scriptsStore;
	}

	get work(): UndeliveredWork {
		return { files: this.files, subagents: this.subagents, scriptsStore: this.scriptsStore };
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
		const subagents = unmergedChildrenFrom(error);
		const scriptsStore = scriptsStoreFrom(error);
		if (files || subagents || scriptsStore) {
			throw new UndeliveredWorkError(
				apiErrorMessage(error, "Unable to end this session"),
				files ?? [],
				subagents ?? [],
				scriptsStore ?? undefined,
			);
		}
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

/**
 * Pull the worker's undelivered subagents out of either refusal: the one for
 * them alone, and the files refusal, which carries them alongside so a discard
 * is never confirmed against half the list. Returns null for every other
 * failure.
 */
export function unmergedChildrenFrom(error: unknown): UndeliveredChild[] | null {
	if (typeof error !== "object" || error === null) return null;
	const body = error as { code?: unknown; details?: unknown };
	if (body.code !== UNMERGED_CHILDREN_CODE && body.code !== UNDELIVERED_WORK_CODE) return null;
	const details = body.details as { children?: unknown } | undefined;
	if (!Array.isArray(details?.children)) return [];
	const text = (value: unknown) => (typeof value === "string" ? value : "");
	return details.children.flatMap((entry): UndeliveredChild[] => {
		if (typeof entry !== "object" || entry === null) return [];
		const child = entry as Record<string, unknown>;
		const agentId = text(child.agentId);
		if (agentId === "") return [];
		return [
			{
				agentId,
				description: text(child.description),
				state: text(child.state),
				branch: text(child.branch),
				detail: text(child.detail),
			},
		];
	});
}

/**
 * Pull the scripts store worktree out of any refusal that carries it: the one
 * for it alone, and the files and subagents refusals, which carry it alongside
 * so a discard is never confirmed against part of what it deletes. Returns
 * null for every other failure, and for a refusal that does not name one.
 */
export function scriptsStoreFrom(error: unknown): ScriptsStoreRefusal | null {
	if (typeof error !== "object" || error === null) return null;
	const body = error as { code?: unknown; details?: unknown };
	const codes: unknown[] = [UNPUBLISHED_SCRIPTS_CODE, UNDELIVERED_WORK_CODE, UNMERGED_CHILDREN_CODE];
	if (!codes.includes(body.code)) return null;
	const raw = (body.details as { scriptsStore?: unknown } | undefined)?.scriptsStore;
	const text = (value: unknown) => (typeof value === "string" ? value : "");
	const list = (value: unknown) =>
		Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
	if (typeof raw !== "object" || raw === null) {
		// The refusal over the store alone IS about the store, even unnamed.
		return body.code === UNPUBLISHED_SCRIPTS_CODE ? { path: "", branch: "", baseBranch: "", uncommitted: [] } : null;
	}
	const store = raw as Record<string, unknown>;
	const publish = (typeof store.publish === "object" && store.publish !== null ? store.publish : {}) as Record<
		string,
		unknown
	>;
	return {
		path: text(store.path),
		branch: text(store.branch),
		baseBranch: text(store.baseBranch),
		uncommitted: list(store.uncommitted),
		publishRefused:
			publish.outcome === "refused"
				? { hold: text(publish.hold), detail: text(publish.detail), files: list(publish.files) }
				: undefined,
	};
}
