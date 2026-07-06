import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProviderBadge } from "./ProviderBadge";

describe("ProviderBadge", () => {
	it("renders GitLab for provider=gitlab", () => {
		render(<ProviderBadge provider="gitlab" />);
		expect(screen.getByText("GitLab")).toBeInTheDocument();
	});

	it("renders GitHub for provider=github", () => {
		render(<ProviderBadge provider="github" />);
		expect(screen.getByText("GitHub")).toBeInTheDocument();
	});

	it("renders nothing for an undefined provider", () => {
		const { container } = render(<ProviderBadge provider={undefined} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing for an unknown provider", () => {
		const { container } = render(<ProviderBadge provider={"bitbucket" as never} />);
		expect(container).toBeEmptyDOMElement();
	});
});
