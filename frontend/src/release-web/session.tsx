import { Button } from "../renderer/components/ui/button";

/** Reloads to the sign-in page once the server stops honouring the session (a rotated key, an expired cookie). */
export function signOutOnUnauthorized(): void {
	const fetchOnce = window.fetch.bind(window);
	window.fetch = async (input, init) => {
		const res = await fetchOnce(input, init);
		if (res.status === 401) window.location.replace("/");
		return res;
	};
}

export function SignOut() {
	return (
		<form method="post" action="/auth/signout" className="flex h-8 shrink-0 items-center justify-end border-b px-3">
			<Button type="submit" variant="ghost" size="sm">
				Sign out
			</Button>
		</form>
	);
}
