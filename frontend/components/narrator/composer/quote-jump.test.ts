import { describe, expect, test } from "bun:test";
import { NodeFilter, parseHTML } from "linkedom";
import { revealComposerQuote } from "./quote-jump";

describe("revealComposerQuote", () => {
	test("highlights the panel that contains the click, not the first panel", async () => {
		const { document: doc, window } = parseHTML(`<!doctype html><html><body>
			<div data-narrator-panel id="first">
				<div data-nf-row-key="tool-tool1234">
					<button id="first-header" data-nf-card-header aria-expanded="false"></button>
					<span>schema value</span>
				</div>
			</div>
			<div data-narrator-panel id="second">
				<div data-nf-row-key="tool-tool1234">
					<button id="second-header" data-nf-card-header aria-expanded="false"></button>
					<span>schema value</span>
				</div>
			</div>
		</body></html>`);
		const clicks: string[] = [];
		for (const id of ["first-header", "second-header"]) {
			doc.getElementById(id)?.addEventListener("click", () => clicks.push(id));
		}
		const rangeProto = doc.createRange().constructor.prototype as {
			setStart?: (node: Node, offset: number) => void;
			setEnd?: (node: Node, offset: number) => void;
		};
		const previousStart = rangeProto.setStart;
		const previousEnd = rangeProto.setEnd;
		rangeProto.setStart = () => {};
		rangeProto.setEnd = () => {};
		const globals = globalThis as { NodeFilter?: unknown; CSS?: unknown; HTMLElement?: unknown };
		const previous = {
			NodeFilter: globals.NodeFilter,
			CSS: globals.CSS,
			HTMLElement: globals.HTMLElement,
		};
		Object.defineProperty(globalThis, "NodeFilter", { configurable: true, value: NodeFilter });
		Object.defineProperty(globalThis, "HTMLElement", {
			configurable: true,
			value: window.HTMLElement,
		});
		if (!globals.CSS) {
			Object.defineProperty(globalThis, "CSS", {
				configurable: true,
				value: { escape: (value: string) => value },
			});
		}
		try {
			revealComposerQuote(
				null,
				{ label: "schema value", text: "schema value", anchor: "t:tool1234" },
				doc.getElementById("second"),
			);
			await new Promise((resolve) => setTimeout(resolve, 80));
			expect(clicks).toEqual(["second-header"]);
		} finally {
			if (previousStart) rangeProto.setStart = previousStart;
			else delete rangeProto.setStart;
			if (previousEnd) rangeProto.setEnd = previousEnd;
			else delete rangeProto.setEnd;
			if (previous.NodeFilter)
				Object.defineProperty(globalThis, "NodeFilter", {
					configurable: true,
					value: previous.NodeFilter,
				});
			else Reflect.deleteProperty(globalThis, "NodeFilter");
			if (!previous.CSS) Reflect.deleteProperty(globalThis, "CSS");
			if (previous.HTMLElement)
				Object.defineProperty(globalThis, "HTMLElement", {
					configurable: true,
					value: previous.HTMLElement,
				});
			else Reflect.deleteProperty(globalThis, "HTMLElement");
		}
	});
});
