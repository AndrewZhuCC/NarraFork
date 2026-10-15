/**
 * KnowledgeRead error classification (agent tool).
 *
 * "The entry does not exist" and "you may not read it" must be INDISTINGUISHABLE here: `getEntry`
 * maps a missing row, an ACL denial and a cross-project access to the same `NotFoundError`, so any
 * difference in the tool's answer would leak the existence of entries the caller cannot see.
 *
 * Everything ELSE that can go wrong on this path is a real failure, and must not be dressed up as
 * that answer: the agent would stop looking, and the upstream message can carry SQL, bound
 * parameters and entry bodies that the read ACL may be withholding from this very caller. Those
 * failures report the stable `KNOWLEDGE_READ_FAILED` marker, and the detail stays in a bounded,
 * content-free server log line.
 *
 * Isolation: the shared test preload points NARRAFORK_HOME at a temp directory, so the database
 * used here is a throwaway — never the real one. Run:
 *   env -u NARRAFORK_HOME -u NF_DATABASE_URL -u DATABASE_URL NF_DATABASE_BACKEND=sqlite \
 *     bun test --isolate server/services/__tests__/knowledge-read-tool.test.ts
 */
import { beforeAll, describe, expect, spyOn, test } from "bun:test";
import { eq } from "drizzle-orm";
import { db } from "../../db";
import {
	knowledgeCollections,
	knowledgeDrafts,
	knowledgeEntries,
	knowledgeRevisions,
	knowledgeTags,
	users,
} from "../../db/schema";
import { knowledgeReadTool } from "../../lib/agent/tools/knowledge";
import { knowledgeEditTool } from "../../lib/agent/tools/knowledge-edit";
import type { ToolContext } from "../../lib/agent/types";
import { AppError, ForbiddenError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { logger } from "../../lib/logger";
import { knowledgeBranchService } from "../knowledge-branch-service";
import { knowledgeService } from "../knowledge-service";

const TAG = Date.now();

const READABLE_TITLE = `read-tool-open-${TAG}`;
const READABLE_BODY = "# Open entry\n\nbody readable by anyone in the collection";
const DENIED_TITLE = `read-tool-sealed-${TAG}`;
const DENIED_BODY = "# Sealed entry\n\nbody behind a controlled tag";

/** Markers deliberately embedded in stubbed failures: none may reach the model or the log. */
const DRIVER_LEAK = "SQLITE_ERROR: no such column: hidden_col -- params: ['secret body text']";
const NON_ERROR_LEAK = "raw-non-error-SECRET-payload";

/** One captured `logger.error(message, data)` call. */
type LoggedCall = [string, Record<string, unknown>];

let readerUserId: string;
let adminUserId: string;
let collectionId: string;
let readableEntryId: string;
/** Exists, but requires a controlled tag the reader does not hold. */
let sealedEntryId: string;

// biome-ignore lint/suspicious/noExplicitAny: tests replace real service methods wholesale
type AnyFn = (...args: any[]) => any;

/** Minimal ToolContext, mirroring the other knowledge-*.test.ts helpers. */
function ctxFor(userId: string | null, narratorId = `narr-read-${TAG}`): ToolContext {
	return {
		narratorId,
		cwd: "/tmp",
		signal: new AbortController().signal,
		locale: "en",
		userId,
		currentToolUseId: `tu-${generateId(6)}`,
		requestPermission: async () => ({ behavior: "allow" }),
	};
}

/**
 * Replace one service method for the duration of `fn`, restoring it in `finally`.
 *
 * The restore cannot be left to `afterEach`: a failing assertion must not leave a stub behind for
 * the rest of this file (or, without `--isolate`, for later files).
 */
async function withStub<R>(
	target: object,
	method: string,
	stub: { resolve?: unknown; reject?: unknown },
	fn: () => Promise<R>,
): Promise<R> {
	const spy = spyOn(target as Record<string, AnyFn>, method);
	if ("reject" in stub) spy.mockRejectedValue(stub.reject);
	else spy.mockResolvedValue(stub.resolve);
	try {
		return await fn();
	} finally {
		spy.mockRestore();
	}
}

/** Capture `logger.error` calls for the duration of `fn`, restoring `logger` in `finally`. */
async function captureLogs<R>(fn: (calls: LoggedCall[]) => Promise<R>): Promise<R> {
	const spy = spyOn(logger, "error").mockImplementation(() => {});
	try {
		return await fn(spy.mock.calls as unknown as LoggedCall[]);
	} finally {
		spy.mockRestore();
	}
}

async function makeUser(role: "admin" | "user", label: string): Promise<string> {
	const id = generateId();
	await db.insert(users).values({
		id,
		username: `${label}-${TAG}-${generateId(4)}`,
		passwordHash: "x",
		role,
		createdAt: new Date().toISOString(),
	});
	return id;
}

/** Create an entry (with a current revision) whose readability is gated by `controlledTagsJson`. */
async function makeEntry(opts: {
	title: string;
	content: string;
	controlledTagId?: string;
}): Promise<string> {
	const entryId = generateId();
	const revisionId = generateId();
	const now = new Date().toISOString();
	await db.insert(knowledgeEntries).values({
		id: entryId,
		collectionId,
		title: opts.title,
		slug: `${opts.title.toLowerCase().replace(/\s+/g, "-")}-${generateId(4)}`,
		currentRevisionId: revisionId,
		currentContent: opts.content,
		tagsJson: [],
		controlledTagsJson: opts.controlledTagId ? [opts.controlledTagId] : null,
		status: "active",
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(knowledgeRevisions).values({
		id: revisionId,
		entryId,
		version: 1,
		format: "markdown",
		content: opts.content,
		contentHash: "x",
		createdAt: now,
	});
	return entryId;
}

beforeAll(async () => {
	readerUserId = await makeUser("user", "kread-plain");
	adminUserId = await makeUser("admin", "kread-admin");

	const collection = await db
		.insert(knowledgeCollections)
		.values({
			id: generateId(),
			name: `read-tool-${TAG}`,
			slug: `read-tool-${TAG}`,
			defaultLevel: "public",
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString(),
		})
		.returning();
	collectionId = collection[0].id;

	// Controlled tag the reader is NOT granted: the seal on `sealedEntryId`. Nothing needs to grant
	// it — `canRead` denies on the missing tag grant, which is the real ACL path for a denial.
	const controlledTagId = generateId();
	await db.insert(knowledgeTags).values({
		id: controlledTagId,
		collectionId,
		name: `seal-${TAG}`,
		controlled: true,
		createdAt: new Date().toISOString(),
	});

	readableEntryId = await makeEntry({ title: READABLE_TITLE, content: READABLE_BODY });
	sealedEntryId = await makeEntry({
		title: DENIED_TITLE,
		content: DENIED_BODY,
		controlledTagId,
	});
});

describe("unpublished personal knowledge", () => {
	test("create, read, save and meta work without publishing", async () => {
		const personal = await knowledgeBranchService.createStandalone(
			{ userId: readerUserId, role: "user" },
			{ title: "Private note", content: "original private body" },
		);
		let result = await knowledgeReadTool.execute(
			{ personalEntryId: personal.id },
			ctxFor(readerUserId),
		);
		expect(result.isError).toBeUndefined();
		expect(result.output).toContain("original private body");
		expect(result.metadata).toMatchObject({
			personalEntryId: personal.id,
			linkedEntryId: null,
			isDraft: true,
		});
		const saved = await knowledgeEditTool.execute(
			{ action: "save", personalEntryId: personal.id, content: "edited private body" },
			ctxFor(readerUserId),
		);
		expect(saved.isError).toBeUndefined();
		const meta = await knowledgeEditTool.execute(
			{
				action: "update_meta",
				personalEntryId: personal.id,
				title: "Renamed private",
				keywords: ["private-token"],
			},
			ctxFor(readerUserId),
		);
		expect(meta.isError).toBeUndefined();
		result = await knowledgeReadTool.execute(
			{ personalEntryId: personal.id },
			ctxFor(readerUserId),
		);
		expect(result.output).toContain("Renamed private");
		expect(result.output).toContain("edited private body");
		expect(result.metadata).toMatchObject({ keywords: ["private-token"] });
		for (const userId of [null, adminUserId]) {
			const denied = await knowledgeReadTool.execute(
				{ personalEntryId: personal.id },
				ctxFor(userId),
			);
			expect(denied.isError).toBe(true);
			expect(denied.output).not.toContain("edited private body");
		}
	});

	test("identifier validation and global-only flag fail explicitly", async () => {
		for (const args of [
			{},
			{ entryId: readableEntryId, personalEntryId: "personal" },
			{ personalEntryId: "personal", useDraft: false },
		]) {
			expect((await knowledgeReadTool.execute(args, ctxFor(readerUserId))).isError).toBe(true);
		}
	});

	test("personal content is bounded and linked entries still require main ACL", async () => {
		const personal = await knowledgeBranchService.createStandalone(
			{ userId: readerUserId, role: "user" },
			{ title: "Long private", content: "x".repeat(25000) },
		);
		const result = await knowledgeReadTool.execute(
			{ personalEntryId: personal.id },
			ctxFor(readerUserId),
		);
		expect(result.metadata).toMatchObject({ truncated: true });
		expect(result.output.length).toBeLessThan(25000);
		await db
			.update(knowledgeDrafts)
			.set({ entryId: sealedEntryId })
			.where(eq(knowledgeDrafts.id, personal.id));
		const denied = await knowledgeReadTool.execute(
			{ personalEntryId: personal.id },
			ctxFor(readerUserId),
		);
		expect(denied.isError).toBe(true);
		expect(denied.output).toContain("not found or not accessible");
	});

	test("target collection respects narrator project scope", async () => {
		const personal = await knowledgeBranchService.createStandalone(
			{ userId: readerUserId, role: "user" },
			{ title: "Scoped private", content: "scoped content", targetCollectionId: collectionId },
		);
		const spy = spyOn(knowledgeService, "getCollection").mockResolvedValue({
			projectId: "other-project",
		} as Awaited<ReturnType<typeof knowledgeService.getCollection>>);
		try {
			const result = await knowledgeReadTool.execute(
				{ personalEntryId: personal.id },
				{ ...ctxFor(readerUserId), projectId: "this-project" },
			);
			expect(result.isError).toBe(true);
			expect(result.output).not.toContain("scoped content");
		} finally {
			spy.mockRestore();
		}
	});
});

describe("KnowledgeRead success path", () => {
	test("returns title and body for an entry the principal may read", async () => {
		const res = await knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId));
		expect(res.isError).toBeUndefined();
		expect(res.output).toContain(READABLE_TITLE);
		expect(res.output).toContain(READABLE_BODY);
		expect(res.metadata).toMatchObject({
			tool: "KnowledgeRead",
			isDraft: false,
			truncated: false,
		});
	});

	test("a readable entry still reads through the personal-version lookup", async () => {
		// The default (useDraft) path consults getDraftDrift; with no personal version it must add no
		// banner and must not report an error.
		const res = await knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId));
		expect(res.isError).toBeUndefined();
		expect(res.output).not.toContain("personal version");
		expect(res.metadata).toMatchObject({ drift: { drifted: false, versionsBehind: 0 } });
	});
});

describe("KnowledgeRead not-found / not-accessible", () => {
	test("same id: ACL denial and a genuinely missing row are byte-identical", async () => {
		const readerCtx = ctxFor(readerUserId);

		// State 1: the entry EXISTS, the reader lacks its controlled tag.
		const denied = await knowledgeReadTool.execute({ entryId: sealedEntryId }, readerCtx);
		expect(denied.isError).toBe(true);
		expect(denied.output).toContain(`not found or not accessible: ${sealedEntryId}`);

		// Proof the denial was ACL and not a broken fixture: an admin reads the very same row.
		const asAdmin = await knowledgeReadTool.execute(
			{ entryId: sealedEntryId },
			ctxFor(adminUserId),
		);
		expect(asAdmin.isError).toBeUndefined();
		expect(asAdmin.output).toContain(DENIED_BODY);

		// State 2: the row is gone for everyone.
		await db.delete(knowledgeRevisions).where(eq(knowledgeRevisions.entryId, sealedEntryId));
		await db.delete(knowledgeEntries).where(eq(knowledgeEntries.id, sealedEntryId));

		const missing = await knowledgeReadTool.execute({ entryId: sealedEntryId }, readerCtx);
		expect(missing.isError).toBe(true);
		expect(missing.output).toBe(denied.output);

		// The deletion really happened: an admin now gets the same answer instead of the body.
		const adminMissing = await knowledgeReadTool.execute(
			{ entryId: sealedEntryId },
			ctxFor(adminUserId),
		);
		expect(adminMissing.isError).toBe(true);
		expect(adminMissing.output).toBe(denied.output);
	});

	test("a ForbiddenError folds into the same answer, without its message", async () => {
		// Defensive branch: nothing on this path throws it today, but if one ever does, it must not
		// become a second, distinguishable answer.
		const forbiddenSecret = "forbidden-SECRET-reason-body";
		const res = await withStub(
			knowledgeService,
			"getEntry",
			{ reject: new ForbiddenError(forbiddenSecret) },
			() => knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
		);
		expect(res.isError).toBe(true);
		expect(res.output).toContain(`not found or not accessible: ${readableEntryId}`);
		expect(res.output).not.toContain(forbiddenSecret);
		expect(res.output).not.toContain("KNOWLEDGE_READ_FAILED");
	});
});

describe("KnowledgeRead unclassified failures", () => {
	test("a database-class failure reports KNOWLEDGE_READ_FAILED, not 'not found'", async () => {
		const res = await withStub(
			knowledgeService,
			"getEntry",
			{ reject: new Error(DRIVER_LEAK) },
			() => knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
		);
		expect(res.isError).toBe(true);
		expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
		// The two things a caller must not be handed: driver detail, and a false access verdict.
		expect(res.output).not.toContain("SQLITE_ERROR");
		expect(res.output).not.toContain("hidden_col");
		expect(res.output).not.toContain("secret body text");
		expect(res.output).not.toContain("not found or not accessible");
	});

	test("the failure log carries bounded context and none of the error text", async () => {
		const actorId = generateId();
		await captureLogs(async (calls) => {
			const res = await withStub(
				knowledgeService,
				"getEntry",
				{ reject: new Error(DRIVER_LEAK) },
				() =>
					knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId, actorId)),
			);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");

			const readLog = calls.find(([message]) => message.includes("KnowledgeRead"));
			expect(readLog?.[1]).toMatchObject({
				stage: "entry",
				entryId: readableEntryId,
				narratorId: actorId,
			});
			// Driver text, bound parameters and bodies stay out of the log too.
			const logged = JSON.stringify(calls);
			expect(logged).not.toContain("SQLITE_ERROR");
			expect(logged).not.toContain("secret body text");
			expect(logged).not.toContain(READABLE_BODY);
		});
	});

	test.each([
		`secret-id:${"x".repeat(10_000)}`,
		"invalid/id",
	])("bounds diagnostics for invalid IDs and omits arbitrary error identity fields", async (invalidId) => {
		const error = new AppError(DRIVER_LEAK, 500, `secret-code:${"x".repeat(10_000)}`);
		error.name = `secret-name:${"x".repeat(10_000)}`;
		await captureLogs(async (calls) => {
			const res = await withStub(knowledgeService, "getEntry", { reject: error }, () =>
				knowledgeReadTool.execute({ entryId: invalidId }, ctxFor(readerUserId, invalidId)),
			);
			expect(res.isError).toBe(true);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
			expect(res.output.length).toBeLessThan(512);
			expect(res.output).not.toContain(invalidId);
			const readLogs = calls.filter(([message]) => message === "KnowledgeRead failed");
			expect(readLogs).toHaveLength(1);
			expect(readLogs[0][1]).toEqual({
				stage: "entry",
				entryId: "[invalid-id]",
				narratorId: "[invalid-id]",
				errorType: "Error",
			});
			const logged = JSON.stringify(readLogs);
			expect(logged.length).toBeLessThan(512);
			expect(logged).not.toContain("secret-");
			expect(logged).not.toContain(DRIVER_LEAK);
		});
	});

	test("a non-Error rejection is classified the same way", async () => {
		await captureLogs(async () => {
			const res = await withStub(knowledgeService, "getEntry", { reject: NON_ERROR_LEAK }, () =>
				knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
			);
			expect(res.isError).toBe(true);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
			expect(res.output).not.toContain(NON_ERROR_LEAK);
			expect(res.output).not.toContain("not found or not accessible");
		});
	});

	test("a personal-version drift lookup that throws is not reported as 'not found'", async () => {
		await captureLogs(async (calls) => {
			const res = await withStub(
				knowledgeBranchService,
				"getDraftDrift",
				{ reject: new Error(DRIVER_LEAK) },
				() => knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
			);
			expect(res.isError).toBe(true);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
			expect(res.output).not.toContain("not found or not accessible");
			expect(res.output).not.toContain(READABLE_BODY);
			const readLog = calls.find(([message]) => message.includes("KnowledgeRead"));
			expect(readLog?.[1]).toMatchObject({ stage: "draft_drift" });
		});
	});

	test("a personal-version diff that throws is not reported as 'not found'", async () => {
		await captureLogs(async (calls) => {
			const res = await withStub(
				knowledgeBranchService,
				"getDraftDrift",
				{ resolve: { hasDraft: true, drifted: true, draftId: `draft-${TAG}`, versionsBehind: 2 } },
				() =>
					withStub(knowledgeBranchService, "getDraftDiff", { reject: new Error(DRIVER_LEAK) }, () =>
						knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
					),
			);
			expect(res.isError).toBe(true);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
			expect(res.output).not.toContain("not found or not accessible");
			const readLog = calls.find(([message]) => message.includes("KnowledgeRead"));
			expect(readLog?.[1]).toMatchObject({ stage: "draft_diff" });
		});
	});

	test("a personal-version content lookup that throws is not reported as 'not found'", async () => {
		await captureLogs(async (calls) => {
			const res = await withStub(
				knowledgeBranchService,
				"getDraftDrift",
				{ resolve: { hasDraft: true, drifted: false, draftId: `draft-${TAG}`, versionsBehind: 0 } },
				() =>
					withStub(knowledgeBranchService, "getMyDraft", { reject: new Error(DRIVER_LEAK) }, () =>
						knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId)),
					),
			);
			expect(res.isError).toBe(true);
			expect(res.output).toContain("KNOWLEDGE_READ_FAILED");
			expect(res.output).not.toContain("not found or not accessible");
			const readLog = calls.find(([message]) => message.includes("KnowledgeRead"));
			expect(readLog?.[1]).toMatchObject({ stage: "draft" });
		});
	});

	test("service stubs are restored after every case", async () => {
		// Guards the `finally`-restore contract itself: a leaked stub would make this real read fail.
		const res = await knowledgeReadTool.execute({ entryId: readableEntryId }, ctxFor(readerUserId));
		expect(res.isError).toBeUndefined();
		expect(res.output).toContain(READABLE_TITLE);
	});
});
