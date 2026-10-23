/**
 * narrator-body-access.guard.test.ts — who touches a message row's body field
 * (docs/BLOCK_STORAGE-DESIGN.md, P3d).
 *
 * The frontend renders a message from its blocks. Readers take the body through the
 * accessors in `shared/narrator-blocks/message-blocks.ts` (`messageBody` for the elements,
 * `messageBlocks` for the blocks), never from the row's storage field, so the row can
 * change how it holds its blocks (P3d step 7) without touching them.
 *
 * `BODY_FIELD_USES` counts what each file still does with `contentJson` directly: the
 * accessor itself, the row type, and code that builds or rewrites rows (the store's merges
 * and patches, streaming, the protocol decoder, rows built for chat and fixtures). The
 * counts only go down:
 *   - a use in an unlisted file, or more than its count, fails — read through
 *     `messageBody` / `messageBlocks` instead;
 *   - fewer than its count fails too — lower it (drop the entry at zero).
 *
 * The scan is lexical over comment-stripped source. A floor, not a proof.
 */

import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

const ROOT = join(import.meta.dir, "..", "..");

/** Direct uses of the body field per file, and why they are there. */
const BODY_FIELD_USES: Record<string, { count: number; why: string }> = {
	"shared/narrator-blocks/message-blocks.ts": { count: 5, why: "the accessor" },
	"frontend/lib/api/types.ts": { count: 1, why: "the row type" },
	"frontend/lib/block-protocol.ts": { count: 6, why: "decodes v2 frames into rows" },
	// The store: merges and patches build new rows.
	"frontend/components/narrator/message/message-tree-utils.ts": {
		count: 33,
		why: "tree patches and streaming children",
	},
	"frontend/components/narrator/vlist/vlist-message-append.ts": { count: 11, why: "row merge" },
	"frontend/components/narrator/narrator-message-helpers.ts": {
		count: 4,
		why: "row merges, streaming tool rows",
	},
	"frontend/components/narrator/vlist/vlist-live-patch.ts": { count: 2, why: "ledger patch" },
	// Streaming: the live row and its handoff to the persisted one.
	"frontend/components/narrator/vlist/streaming-handoff.ts": { count: 20, why: "handoff" },
	"frontend/components/narrator/message/message-segments.ts": {
		count: 7,
		why: "segment cache keys, the streaming row",
	},
	// Rows built outside the narrator store.
	"frontend/components/chat/chat-vlist-adapter.ts": { count: 4, why: "chat rows" },
	"frontend/components/narrator/workspace/workspace-resource.browser-fixture.tsx": {
		count: 1,
		why: "fixture row",
	},
	// Structural row types narrower than the row type.
	"shared/pretext-layout/segment-adapter.ts": { count: 1, why: "AdapterMessage" },
	"frontend/components/narrator/vlist/vlist-tail-meta.ts": { count: 1, why: "TailMetaMessage" },
	"frontend/components/narrator/vlist/vlist-spec-tasks-pin.ts": {
		count: 1,
		why: "SpecTasksMessageLike",
	},
	"frontend/components/narrator/narrator-panel-overrides.ts": { count: 1, why: "Pick of a row" },
};

const SCANNED = ["frontend", "shared/pretext-layout", "shared/narrator-blocks/message-blocks.ts"];

function isExempt(path: string): boolean {
	return (
		/\.test\.tsx?$/.test(path) ||
		path.includes("/__tests__/") ||
		path.includes("/mock/") ||
		path.endsWith("routeTree.gen.ts")
	);
}

/** Line comments and block comments out, string contents kept. */
function stripComments(source: string): string {
	return source
		.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ""))
		.replace(/(^|[^:"'`\\])\/\/.*$/gm, "$1");
}

export function bodyFieldUses(code: string): number {
	return (code.match(/\bcontentJson\b/g) ?? []).length;
}

function sourceFiles(path: string, out: string[]): string[] {
	const absolute = join(ROOT, path);
	if (statSync(absolute).isFile()) {
		out.push(path);
		return out;
	}
	for (const name of readdirSync(absolute)) {
		if (name === "node_modules") continue;
		const child = join(absolute, name);
		const rel = relative(ROOT, child).split(sep).join("/");
		if (statSync(child).isDirectory()) sourceFiles(rel, out);
		else if (/\.tsx?$/.test(name)) out.push(rel);
	}
	return out;
}

describe("narrator body access", () => {
	test("bodyFieldUses counts the field, not comments or other names", () => {
		expect(bodyFieldUses(stripComments("// contentJson\nconst a = row.contentJson;"))).toBe(1);
		expect(bodyFieldUses("const contentJsonBytes = 1; originalContentJson;")).toBe(0);
	});

	test("readers take the body through the accessors; the counts only go down", () => {
		const counts = new Map<string, number>();
		for (const path of SCANNED.flatMap((root) => sourceFiles(root, []))) {
			if (isExempt(path)) continue;
			const uses = bodyFieldUses(stripComments(readFileSync(join(ROOT, path), "utf8")));
			if (uses > 0) counts.set(path, uses);
		}
		const problems: string[] = [];
		for (const [path, uses] of counts) {
			const allowed = BODY_FIELD_USES[path]?.count ?? 0;
			if (uses > allowed)
				problems.push(
					`${path}: ${uses} uses of contentJson, ${allowed} allowed — read through messageBody / messageBlocks`,
				);
		}
		for (const [path, { count }] of Object.entries(BODY_FIELD_USES)) {
			const uses = counts.get(path) ?? 0;
			if (uses < count)
				problems.push(`${path}: ${uses} uses of contentJson, listed as ${count} — lower it`);
		}
		expect(problems).toEqual([]);
	});
});
