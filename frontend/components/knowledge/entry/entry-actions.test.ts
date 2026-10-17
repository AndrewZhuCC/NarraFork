import { describe, expect, test } from "bun:test";
import type { KnowledgeEntryViewerCaps } from "../../../lib/api";
import { entryReadActions, groupLinksByType } from "./entry-actions";

const NONE: KnowledgeEntryViewerCaps = {
	canWriteMain: false,
	canEditMeta: false,
	canManageAcl: false,
	canTransferOwner: false,
	canDelete: false,
	canReview: false,
	canManageLinks: true,
	myPersonalEntryId: null,
};

describe("entryReadActions", () => {
	test("a read-only viewer gets one primary personal-version action and no settings", () => {
		expect(entryReadActions(NONE)).toEqual({
			primary: "personal",
			secondaryPersonal: false,
			settings: false,
		});
	});

	test("a direct writer edits as primary and keeps the personal version as secondary", () => {
		// The collection owner case: may write main, may not edit metadata.
		expect(entryReadActions({ ...NONE, canWriteMain: true })).toEqual({
			primary: "edit",
			secondaryPersonal: true,
			settings: false,
		});
	});

	test("any settings capability shows the settings entry point", () => {
		for (const cap of ["canEditMeta", "canManageAcl", "canTransferOwner", "canDelete"] as const) {
			expect(entryReadActions({ ...NONE, [cap]: true }).settings).toBe(true);
		}
	});
});

describe("groupLinksByType", () => {
	test("groups in the stable type order", () => {
		const link = (id: string, linkType: string) =>
			({ id, linkType }) as unknown as Parameters<typeof groupLinksByType>[0][number];
		const groups = groupLinksByType([
			link("a", "custom"),
			link("b", "related"),
			link("c", "depends_on"),
			link("d", "related"),
		]);
		expect(groups.map((g) => [g.type, g.links.map((l) => l.id)])).toEqual([
			["related", ["b", "d"]],
			["depends_on", ["c"]],
			["custom", ["a"]],
		]);
	});
});
