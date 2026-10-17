import { describe, expect, test } from "bun:test";
import {
	activeKnowledgeAdminSection,
	KNOWLEDGE_ADMIN_DEFAULT_SECTION,
	KNOWLEDGE_ADMIN_SECTIONS,
	knowledgeAdminGate,
} from "./knowledge-admin-nav";

describe("knowledge admin sub-navigation", () => {
	test("lists only the implemented sections, the default first", () => {
		expect(KNOWLEDGE_ADMIN_SECTIONS.map((s) => s.to)).toEqual([
			"/knowledge/admin/levels",
			"/knowledge/admin/tags",
			"/knowledge/admin/users",
		]);
		expect(KNOWLEDGE_ADMIN_SECTIONS[0].to).toBe(KNOWLEDGE_ADMIN_DEFAULT_SECTION);
	});

	test("active section matches exact, trailing-slash and nested paths", () => {
		expect(activeKnowledgeAdminSection("/knowledge/admin/tags")?.to).toBe("/knowledge/admin/tags");
		expect(activeKnowledgeAdminSection("/knowledge/admin/users/")?.to).toBe(
			"/knowledge/admin/users",
		);
		expect(activeKnowledgeAdminSection("/knowledge/admin/levels/x")?.to).toBe(
			"/knowledge/admin/levels",
		);
		expect(activeKnowledgeAdminSection("/knowledge/admin")).toBeNull();
		expect(activeKnowledgeAdminSection("/knowledge/admin/tagsx")).toBeNull();
	});
});

describe("knowledgeAdminGate", () => {
	test("admins are allowed, even while a refetch is in flight", () => {
		expect(knowledgeAdminGate({ role: "admin" }, false)).toBe("allowed");
		expect(knowledgeAdminGate({ role: "admin" }, true)).toBe("allowed");
	});

	test("nothing is decided while the user is loading", () => {
		expect(knowledgeAdminGate(undefined, true)).toBe("loading");
	});

	test("non-admins and a missing user are denied", () => {
		expect(knowledgeAdminGate({ role: "user" }, false)).toBe("denied");
		expect(knowledgeAdminGate(undefined, false)).toBe("denied");
		expect(knowledgeAdminGate(null, false)).toBe("denied");
	});
});
