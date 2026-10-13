/**
 * RenderSubagent.live-output-chars.test.tsx — the running subagent card's live
 * output counter badge.
 *
 * The server streams the child's reply length as a bare numeric `tool_output`
 * payload (the ContextAsk convention); the adapter surfaces it as
 * `liveOutputChars` and the card paints it on the fixed header badge row — so
 * it must appear only while the child is still working, and never move the
 * card's measured height.
 */

import { afterAll, describe, expect, it } from "bun:test";
import { MantineProvider } from "@mantine/core";
import { parseHTML } from "linkedom";
import { renderToStaticMarkup } from "react-dom/server";
import { measureSubagentCard, type SubagentCardData } from "../measure/measure-subagent";
import { installCanvasStub } from "../measure/test-canvas-stub";
import { RenderSubagent } from "./RenderSubagent";

const disposeCanvasStub = installCanvasStub();
afterAll(() => disposeCanvasStub());

const WIDTH = 700;

function renderCard(opts: { isActive: boolean; liveOutputChars?: number }) {
	const data: SubagentCardData = {
		agentType: "explore",
		description: "explore the repo",
		...(opts.isActive ? {} : { isTerminal: true }),
	};
	const measured = measureSubagentCard(data, WIDTH, 5, { isActive: opts.isActive });
	const html = renderToStaticMarkup(
		<MantineProvider defaultColorScheme="dark">
			<RenderSubagent
				measured={measured}
				description={data.description}
				agentType={data.agentType}
				isActive={opts.isActive}
				status={opts.isActive ? "running" : "success"}
				liveOutputChars={opts.liveOutputChars}
			/>
		</MantineProvider>,
	);
	const { document } = parseHTML(`<!doctype html><html><body><div id="r">${html}</div></body>`);
	return document as unknown as Document;
}

describe("RenderSubagent live output counter badge", () => {
	it("paints the count on the header badge row while the child is working", () => {
		const doc = renderCard({ isActive: true, liveOutputChars: 1240 });
		expect(doc.querySelector('[data-testid="subagent-output-chars"]')?.textContent).toBe(
			"1240 chars",
		);
	});

	it("is absent on a terminal card even with a stale count", () => {
		const doc = renderCard({ isActive: false, liveOutputChars: 1240 });
		expect(doc.querySelector('[data-testid="subagent-output-chars"]')).toBeNull();
	});

	it("is absent before the first delta arrives", () => {
		const doc = renderCard({ isActive: true });
		expect(doc.querySelector('[data-testid="subagent-output-chars"]')).toBeNull();
	});

	it("is height-neutral: the header row reserves the same height either way", () => {
		const withBadge = measureSubagentCard(
			{ agentType: "explore", description: "d", liveOutputChars: 1240 },
			WIDTH,
			5,
			{ isActive: true },
		);
		const without = measureSubagentCard({ agentType: "explore", description: "d" }, WIDTH, 5, {
			isActive: true,
		});
		expect(withBadge.height).toBe(without.height);
	});
});
