import { useState } from "react";
import { getApiBaseUrl, hasTrustedApiBaseUrl } from "../../lib/api-client";
import { useObjectUrl } from "../MediaLightbox";

function initials(name: string): string {
	return name
		.split(/\s+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((w) => w[0]?.toUpperCase())
		.join("");
}

// The team web's CSP allows img-src 'self' but not blob:, so a same-origin route loads straight into the img.
// The app's daemon is a cross-origin loopback URL, so it still goes through fetch into a blob: URL.
function isSameOrigin(src: string): boolean {
	return new URL(src, window.location.href).origin === window.location.origin;
}

export function Avatar({ accountId, name, size = 22 }: { accountId?: string; name: string; size?: number }) {
	const src =
		accountId && hasTrustedApiBaseUrl()
			? `${getApiBaseUrl()}/api/v1/release-trains/avatars/${encodeURIComponent(accountId)}`
			: "";
	const direct = src !== "" && isSameOrigin(src);
	const { url: objectUrl } = useObjectUrl(direct ? "" : src);
	const [failedSrc, setFailedSrc] = useState("");
	const url = direct ? (failedSrc === src ? null : src) : objectUrl;
	const style = { width: size, height: size };
	return url ? (
		<img className="av" src={url} alt="" style={style} data-account={accountId} onError={() => setFailedSrc(src)} />
	) : (
		<span className="av" style={style} data-account={accountId} aria-hidden="true">
			{initials(name)}
		</span>
	);
}
