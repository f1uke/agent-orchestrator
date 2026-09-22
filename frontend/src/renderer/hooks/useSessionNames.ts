import { useMemo } from "react";
import type { SessionNames } from "../lib/crew";
import { useWorkspaceQuery } from "./useWorkspaceQuery";

/**
 * Every session this machine knows, by id, under the name the BOARD gives it.
 *
 * An exclusive resource names its holder by session id - a simulator lease is
 * the one that reaches the screen today - and an id is the worst of both things
 * a label can be: long enough to truncate the row it sits on, and silent about
 * what that session is actually doing. `@nter-ios-app-77` against "OA landing
 * advisor" for the same session is the whole difference.
 *
 * The listing behind this spans EVERY project, not just the one being looked at,
 * which is what makes it worth reading at all: a simulator is a machine-wide
 * resource, so the session holding one is very often working on something else
 * entirely, and that is exactly the holder an id fails to explain.
 *
 * A session with no display name of its own resolves to its id upstream, and
 * those are left OUT rather than passed through: a caller that finds no name
 * falls back to `@id`, and the `@` is what says "this is an id, not a name".
 */
export function useSessionNames(): SessionNames {
	const workspaces = useWorkspaceQuery().data;
	return useMemo(() => {
		const names = new Map<string, string>();
		for (const workspace of workspaces ?? []) {
			for (const session of workspace.sessions) {
				if (session.title && session.title !== session.id) names.set(session.id, session.title);
			}
		}
		return names;
	}, [workspaces]);
}
