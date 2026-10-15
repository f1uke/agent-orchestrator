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

export function Avatar({ accountId, name, size = 22 }: { accountId: string; name: string; size?: number }) {
	const src = hasTrustedApiBaseUrl()
		? `${getApiBaseUrl()}/api/v1/release-trains/avatars/${encodeURIComponent(accountId)}`
		: "";
	const { url } = useObjectUrl(src);
	const style = { width: size, height: size };
	return url ? (
		<img className="av" src={url} alt="" style={style} data-account={accountId} />
	) : (
		<span className="av" style={style} data-account={accountId} aria-hidden="true">
			{initials(name)}
		</span>
	);
}
