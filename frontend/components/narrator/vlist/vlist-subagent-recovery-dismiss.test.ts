/**
 * vlist-subagent-recovery-dismiss.test.ts — the recovery card's close button must be
 * wired end to end in the exact shell.
 *
 * Same source-assertion style as vlist-injection-guard-actions.test.ts (the shell is
 * not unit-mountable); the click-to-callback half is covered by the real DOM test
 * (render/RenderSubagentRecovery.dismiss.test.tsx).
 *
 * The card already offers two resume buttons, so the regressions worth guarding are
 * the ones that leave the reader with no way out at all: a dropped callback between
 * the shell and the row, an API client pointed at the wrong route, or a server route
 * that deletes garbage (the id comes from the client).
 */

import { describe, expect, it } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { shellSource } from "./guard-source";
import { sliceBracketedRegion } from "./source-slice";

const DIR = import.meta.dir;
const SHELL = shellSource();
const DISPATCH = readFileSync(join(DIR, "render-registry.tsx"), "utf8");
const CARD = readFileSync(join(DIR, "render", "RenderSubagentRecovery.tsx"), "utf8");
const ACTIONS = readFileSync(join(DIR, "vlist-subagent-recovery-dismiss.ts"), "utf8");
const LABELS = readFileSync(join(DIR, "useVListLabels.ts"), "utf8");
const ADAPTER = readFileSync(
	join(DIR, "..", "..", "..", "..", "shared", "pretext-layout", "segment-adapter.ts"),
	"utf8",
);
const API = readFileSync(join(DIR, "..", "..", "..", "lib", "api", "narrators.ts"), "utf8");
const ROUTES = readFileSync(
	join(DIR, "..", "..", "..", "..", "server", "routes", "narrators.ts"),
	"utf8",
);
const SERVICE = readFileSync(
	join(DIR, "..", "..", "..", "..", "server", "services", "narrator-messages.ts"),
	"utf8",
);

describe("subagent recovery dismiss — shell wiring", () => {
	it("binds the close handler in the shell and hands it to every row", () => {
		expect(SHELL).toContain("useSubagentRecoveryDismiss(narratorId)");
		const rowProps = sliceBracketedRegion(SHELL, "const rowProps: ExactRowProps = {");
		expect(rowProps).toMatch(/onDismissSubagentRecovery:\s*dismissSubagentRecovery,?\s*\n/);
		// Row identity must be part of the memo comparison, or a re-bound handler would
		// never reach an already-mounted row.
		expect(SHELL).toContain("prev.onDismissSubagentRecovery === next.onDismissSubagentRecovery");
	});

	it("binds the row's dismiss to the card's owning message", () => {
		expect(SHELL).toContain("extra.onDismiss = () => onDismissSubagentRecovery(messageId)");
	});

	it("passes the callback through the render dispatch to the card", () => {
		expect(DISPATCH).toContain("if (spec.opts?.onDismiss) extra.onDismiss = spec.opts.onDismiss");
		expect(DISPATCH).toContain("onDismiss={extra.onDismiss as (() => void) | undefined}");
	});
});

describe("subagent recovery dismiss — card + action module", () => {
	it("paints the close button only when the slot is wired", () => {
		// A share viewer has nothing to dismiss, so the absence of the slot must paint
		// no control at all rather than an inert one.
		expect(CARD).toContain("onDismiss?: () => void");
		expect(CARD).toContain("{onDismiss ? (");
		expect(CARD).toContain("onClick={onDismiss}");
	});

	it("sizes the button from the measured constant, not a Mantine token", () => {
		// The button rides inside the header lane the measure layer reserves; a size
		// token could drift past that reservation without any test noticing.
		expect(CARD).toContain("size={CLOSE_BUTTON_SIZE}");
	});

	it("labels the button from the payload (no render i18n)", () => {
		expect(CARD).toContain("aria-label={data.dismissLabel}");
		expect(CARD).not.toContain("@frontend/lib/api");
		expect(CARD).not.toContain("react-i18next");
	});

	it("dismissal calls the dedicated DELETE and prunes the messages cache", () => {
		expect(ACTIONS).toContain("api.dismissSubagentRecoveryMessage(narratorId, messageId)");
		expect(ACTIONS).toContain("removeMessagesFromCache(qc, narratorId,");
		// Identity must be stable: the shell hands this to every mounted row and the
		// row memo compares it identity-wise.
		expect(ACTIONS).toContain("useCallback<SubagentRecoveryDismiss>");
	});

	it("the API client targets the dedicated DELETE route", () => {
		expect(API).toContain("subagent-recovery-messages/");
		expect(ROUTES).toContain('narratorRoutes.delete("/:id/subagent-recovery-messages/:messageId"');
		expect(ROUTES).toContain("dismissSubagentRecoveryMessage(narratorId, messageId)");
	});

	it("the service refuses anything that is not a display recovery card", () => {
		// The id comes from the client, so the type check is the only thing standing
		// between this route and every other message in the narrator.
		expect(SERVICE).toContain(
			"async dismissSubagentRecoveryMessage(narratorId: string, messageId: string)",
		);
		expect(SERVICE).toContain(
			'throw new ValidationError("Message is not a subagent recovery notice")',
		);
		expect(SERVICE).toContain('if (msg.role !== "disp" || !isSubagentRecovery)');
	});

	it("labels the close button through the adapter (render layer holds no i18n)", () => {
		expect(ADAPTER).toContain('subagentRecoveryDismiss: "Dismiss without restarting"');
		expect(ADAPTER).toContain('dismissLabel: sysLabel(ctx, "subagentRecoveryDismiss")');
		expect(LABELS).toContain('subagentRecoveryDismiss: t("subagentRecoveryDismiss")');
	});
});
