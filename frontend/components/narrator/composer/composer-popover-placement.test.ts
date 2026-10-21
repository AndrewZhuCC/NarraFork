import { describe, expect, it } from "bun:test";
import { resolveComposerPopoverSide } from "./composer-popover-placement";

describe("resolveComposerPopoverSide", () => {
	it("opens above when the composer sits low (the narrator page)", () => {
		expect(resolveComposerPopoverSide({ top: 700, bottom: 760 }, 800, 248)).toBe("above");
	});

	it("opens below when there is no room above and more room below (the dashboard)", () => {
		expect(resolveComposerPopoverSide({ top: 180, bottom: 360 }, 900, 248)).toBe("below");
	});

	it("stays above when neither side fits but above is roomier", () => {
		expect(resolveComposerPopoverSide({ top: 200, bottom: 320 }, 400, 248)).toBe("above");
	});
});
