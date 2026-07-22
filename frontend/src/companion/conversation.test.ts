import { describe, expect, it } from "vitest";
import { parseMessageFrom } from "./conversation";

describe("reading who sent a message", () => {
	// `ao send` stamps the sender onto the body itself — `[from @<project>-<num>]`
	// — so the feed frame already carries both ends of the conversation: the sender
	// in the text, the recipient as the frame's own session. Nothing needs adding to
	// the wire; it needed reading.
	it("takes the sender's session id off the front of the line", () => {
		expect(parseMessageFrom("[from @agent-orchestrator-105] P1 is fixed and CI is green")).toEqual({
			sender: "agent-orchestrator-105",
			body: "P1 is fixed and CI is green",
		});
	});

	it("leaves a message with no sender stamp alone", () => {
		// A human typing into the app sends without the CLI's stamp. There is no
		// second Proc to run to, and that is a real case — not a parse failure.
		expect(parseMessageFrom("could you rebase this onto main")).toEqual({
			sender: undefined,
			body: "could you rebase this onto main",
		});
	});

	it("does not treat a bracket that is not a sender stamp as one", () => {
		expect(parseMessageFrom("[WIP] do not merge yet").sender).toBeUndefined();
	});

	it("survives the sigil being absent, because the id is what matters", () => {
		expect(parseMessageFrom("[from agent-orchestrator-105] ping").sender).toBe("agent-orchestrator-105");
	});

	it("keeps an empty body when the stamp is all there was", () => {
		expect(parseMessageFrom("[from @demo-app-1]")).toEqual({ sender: "demo-app-1", body: "" });
	});
});
