/**
 * narrator-mount-column.guard.test.ts — The narrator content column must be ONE
 * width for the whole mount.
 *
 * Opening a narrator used to step through three widths, with two visible jumps:
 *
 *   1. an 860px centered skeleton — the exact list seeded `contentWidth` with
 *      NARRATOR_CENTERED_COLUMN_MAX_WIDTH (a 0-guard fallback, not a real width) and
 *      the loading placeholder PAINTED it, whatever the reader's preference;
 *   2. the measured width, 140ms late — the first measurement went through
 *      `resolveWidthSettle`, which saw an ordinary observer callback and deferred for
 *      WIDTH_SETTLE_DELAY_MS;
 *   3. ~15px narrower — the loaded document became taller than the viewport, the
 *      vertical scrollbar appeared, and `clientWidth` (content-box) dropped.
 *
 * Every one of those is a WIRING property: the widths are correct at each step, so no
 * unit test on the pure resolvers can see the sequence. They are asserted on the
 * wiring instead.
 *
 * The message-list lazy fallback and document-loading placeholder share ONE column
 * helper, and the message skeleton itself owns no width, so nothing nests two caps.
 * The whole-panel fallback only shows a spinner: it must not paint a fake header,
 * composer or message layout before the real panel is ready.
 *
 * Zero-runtime, filesystem-only.
 */

import { describe, expect, it } from "bun:test";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

const NARRATOR_DIR = import.meta.dir;
const FRONTEND_ROOT = resolve(NARRATOR_DIR, "..", "..");

function read(...segments: string[]): string {
	return readFileSync(join(...segments), "utf8");
}

const shell = () => read(NARRATOR_DIR, "vlist", "PretextExactMessageList.tsx");
const panel = () => read(NARRATOR_DIR, "NarratorPanel.tsx");
const panelSkeleton = () => read(NARRATOR_DIR, "NarratorPanelSkeleton.tsx");
const messageSkeleton = () => read(NARRATOR_DIR, "NarratorMessageListSkeleton.tsx");
const column = () => read(FRONTEND_ROOT, "lib", "narrator-content-column.ts");

/** The vlist placeholder element's inline style block. */
function placeholderStyle(source: string): string {
	const marker = source.indexOf("data-pretext-exact-status");
	expect(marker).toBeGreaterThan(-1);
	const start = source.indexOf("style={{", marker);
	expect(start).toBeGreaterThan(marker);
	const end = source.indexOf("}}", start);
	expect(end).toBeGreaterThan(start);
	return source.slice(start, end);
}

describe("mount column: the loading placeholder never paints a measured width", () => {
	it("lays the vlist placeholder out from the shared helper, not contentWidth", () => {
		const style = placeholderStyle(shell());
		expect(style).toContain("narratorColumnPlaceholderStyle(centeredColumn)");
		// Reading the measured width here is what painted the sentinel geometry.
		expect(style).not.toContain("contentWidth");
		// And its gutter comes from the helper too, not a local padding.
		expect(style).not.toContain("PAGE_PADDING");
	});

	it("keeps the vlist gutter and the placeholder gutter on ONE constant", () => {
		// A local literal here is how the two would drift: the rows measure against
		// clientWidth minus PAGE_PADDING, the placeholder pads by the shared constant.
		expect(shell()).toMatch(/const\s+PAGE_PADDING\s*=\s*NARRATOR_COLUMN_GUTTER_PX\s*;/);
	});
});

describe("mount column: the scrollbar cannot change the measured width", () => {
	it("always shows the vertical scrollbar and reserves its gutter on the list viewport", () => {
		const source = shell();
		const viewport = source.indexOf("data-pretext-exact-message-list");
		expect(viewport).toBeGreaterThan(-1);
		// The style block precedes the marker attribute on the same element.
		const style = source.slice(source.lastIndexOf("style={{", viewport), viewport);
		expect(style).toContain('overflowX: "auto"');
		expect(style).toContain('overflowY: "scroll"');
		expect(style).toContain('scrollbarGutter: "stable"');
	});
});

describe("mount column: dock hosts wait for their first positioned geometry", () => {
	it("gates narrator content in both focus and workspace adapters", () => {
		for (const [directory, name] of [
			["dock", "ChatDockPanel"],
			["workspace", "NarratorDockPanel"],
		] as const) {
			const source = read(NARRATOR_DIR, directory, "panels.tsx");
			const start = source.indexOf(`function ${name}(`);
			expect(start).toBeGreaterThan(-1);
			const end = source.indexOf("\nfunction ", start + 1);
			const adapter = source.slice(start, end);
			expect(adapter).toContain("usePanelGeometryReady(compactRef)");
			expect(adapter).toContain("<Box ref={ref}");
			expect(adapter).toMatch(/\{geometryReady &&[\s\S]*?<NarratorPanel/);
		}
	});
});

describe("mount column: one owner for the placeholder width", () => {
	it("routes both message placeholder hosts through the same helper", () => {
		// The list's document-loading placeholder and lazy-chunk Suspense fallback.
		expect(shell()).toContain("narratorColumnPlaceholderStyle(centeredColumn)");
		expect(panel()).toContain("narratorColumnPlaceholderStyle(narratorCenteredColumn)");
	});

	it("keeps the whole-panel fallback as a spinner without a fake panel layout", () => {
		const source = panelSkeleton();
		expect(source).toContain("<Center");
		expect(source).toContain("<Loader");
		expect(source).not.toMatch(/<(?:Skeleton|NarratorMessageListSkeleton)\b/);
		expect(source).not.toContain("narratorColumnPlaceholderStyle");
		expect(source).not.toContain("data-git-slot");
	});

	it("keeps the skeleton component itself free of any width", () => {
		// The helper is the SINGLE owner of the cap. A width on the skeleton would nest
		// a second constraint inside its host's, and the two could disagree.
		const source = messageSkeleton();
		expect(source).not.toContain("maxWidth");
		expect(source).not.toContain("narratorColumnPlaceholderStyle");
		expect(source).not.toContain("NARRATOR_CENTERED_COLUMN_MAX_WIDTH");
	});

	it("keeps no Mantine padding on the message-list fallback host", () => {
		// `px="md"` is a rem gutter; the rows measure in px. Identical at the default
		// root font size, divergent at any other — which is a width jump on mount.
		const source = panel();
		const marker = source.indexOf("narratorColumnPlaceholderStyle(narratorCenteredColumn)");
		expect(marker).toBeGreaterThan(-1);
		const start = source.lastIndexOf("<Box", marker);
		expect(start).toBeGreaterThan(-1);
		const end = source.indexOf(">", marker);
		expect(end).toBeGreaterThan(marker);
		const host = source.slice(start, end);
		expect(host).not.toContain('px="md"');
		expect(host).not.toContain('py="sm"');
	});

	it("derives the helper's geometry in px from the shared gutter", () => {
		const source = column();
		const fn = source.slice(source.indexOf("export function narratorColumnPlaceholderStyle("));
		expect(fn).toContain("NARRATOR_CENTERED_COLUMN_MAX_WIDTH + NARRATOR_COLUMN_GUTTER_PX * 2");
		// A CSS `calc` with a spacing token here would reintroduce the rem/px split.
		expect(fn).not.toContain("var(--mantine-spacing");
		expect(fn).not.toContain("calc(");
	});
});
