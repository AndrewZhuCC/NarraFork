/**
 * Personal-version page payloads (KNOWLEDGE_UI_REDESIGN §5.4 / §5.5).
 *
 *  - `GET /personal-entries/:id` (`getMineForViewer`): `viewerCaps` ⇔ the edit / meta / rebase /
 *    delete / submit endpoints — each flag is true EXACTLY when the corresponding call is
 *    accepted, over linked / standalone × active / archived / drifted / open-request states.
 *    Every mutating probe runs on a FRESH fixture.
 *  - ACL: the linked entry's title, current main and drift (and the standalone target's
 *    collection name) are only returned while the owner can still READ them — on the detail
 *    and on the list (`listMineForViewer`).
 *  - Owner-only: anyone else gets 404 (no existence leak).
 */
import { beforeAll, describe, expect, test } from "bun:test";
import { Hono } from "hono";
import { db } from "../../db";
import { users } from "../../db/schema";
import { AppError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { knowledgeRoutes } from "../../routes/knowledge";
import { knowledgeAcl, type Principal } from "../knowledge-acl";
import {
	knowledgeBranchService,
	personalEntrySubmitBlockers,
	personalEntrySubmitPreconditionBlocker,
} from "../knowledge-branch-service";
import { knowledgeService } from "../knowledge-service";

const TAG = `pev-${Date.now()}`;
const author: Principal = { userId: "", role: "user" };
const stranger: Principal = { userId: "", role: "user" };
const admin: Principal = { userId: "", role: "admin" };

beforeAll(async () => {
	const now = new Date().toISOString();
	for (const [p, role, label] of [
		[author, "user", "author"],
		[stranger, "user", "stranger"],
		[admin, "admin", "admin"],
	] as const) {
		const id = generateId();
		await db
			.insert(users)
			.values({ id, username: `${TAG}-${label}`, passwordHash: "x", role, createdAt: now });
		p.userId = id;
	}
});

type State =
	| "linked"
	| "linkedDrifted"
	| "linkedPending"
	| "linkedArchived"
	| "standalone"
	| "standaloneNoTarget"
	| "standalonePending";
const STATES: State[] = [
	"linked",
	"linkedDrifted",
	"linkedPending",
	"linkedArchived",
	"standalone",
	"standaloneNoTarget",
	"standalonePending",
];

async function makeFixture(state: State) {
	const col = await knowledgeService.createCollection({ name: `${TAG}-col-${generateId()}` });
	if (state.startsWith("standalone")) {
		const draft = await knowledgeBranchService.createStandalone(author, {
			title: `${TAG}-standalone`,
			content: "mine\n",
			...(state === "standaloneNoTarget" ? {} : { targetCollectionId: col.id }),
		});
		if (state === "standalonePending") {
			await knowledgeBranchService.submitForReview(author, draft.id, {});
		}
		return { draftId: draft.id, collectionId: col.id, entryId: null as string | null };
	}
	const entry = await knowledgeService.createEntry({
		collectionId: col.id,
		title: `${TAG}-entry-${generateId(4)}`,
		content: "line A\n",
	});
	const draft = await knowledgeBranchService.createDraft(author, entry.id, {});
	await knowledgeBranchService.updateDraft(author, draft.id, { content: "line A\nline B\n" });
	if (state === "linkedDrifted") {
		await knowledgeService.addRevision(entry.id, { content: "line Z\nline A\n" });
	}
	if (state === "linkedPending") {
		await knowledgeBranchService.submitForReview(author, draft.id, {});
	}
	if (state === "linkedArchived") {
		await knowledgeBranchService.deletePersonalEntry(author, draft.id);
	}
	return { draftId: draft.id, collectionId: col.id, entryId: entry.id as string | null };
}

async function accepted(fn: () => Promise<unknown>): Promise<boolean> {
	try {
		await fn();
		return true;
	} catch {
		return false;
	}
}

describe("personal-version viewerCaps ⇔ endpoints", () => {
	for (const state of STATES) {
		test(state, async () => {
			const probe = async (fn: (draftId: string) => Promise<unknown>) => {
				const f = await makeFixture(state);
				return accepted(() => fn(f.draftId));
			};
			const f = await makeFixture(state);
			const view = await knowledgeBranchService.getMineForViewer(author, f.draftId);
			const caps = view.viewerCaps;

			expect(caps.canEdit).toBe(
				await probe((id) => knowledgeBranchService.updateDraft(author, id, { content: "x\n" })),
			);
			expect(caps.canEditMeta).toBe(
				await probe((id) =>
					knowledgeBranchService.updateStandaloneMeta(author, id, { title: `${TAG}-renamed` }),
				),
			);
			expect(caps.canRebase).toBe(
				await probe((id) => knowledgeBranchService.rebaseDraft(author, id)),
			);
			expect(caps.canSubmit).toBe(
				await probe((id) => knowledgeBranchService.submitForReview(author, id, {})),
			);
			// Delete of an archived entry is an accepted no-op; the cap means "something to delete".
			expect(caps.canDelete).toBe(view.status === "active");
		});
	}

	test("blocked reasons name every failing precondition", async () => {
		const pending = await makeFixture("standalonePending");
		const view = await knowledgeBranchService.getMineForViewer(author, pending.draftId);
		expect(view.viewerCaps.submitBlockers).toEqual(["open_request"]);
		expect(view.openSubmissions.map((s) => s.status)).toEqual(["pending"]);

		const noTarget = await makeFixture("standaloneNoTarget");
		expect(
			(await knowledgeBranchService.getMineForViewer(author, noTarget.draftId)).viewerCaps
				.submitBlockers,
		).toEqual(["missing_target"]);

		const archived = await makeFixture("linkedArchived");
		const av = await knowledgeBranchService.getMineForViewer(author, archived.draftId);
		expect(av.viewerCaps.submitBlockers).toEqual(["archived"]);
		// The delete closed the open request, so nothing is left for a delete to close.
		expect(av.openSubmissions).toEqual([]);
	});

	test("pure blocker helpers agree with submitForReview's order", () => {
		const base = { status: "active", entryId: null, targetCollectionId: null, title: " " };
		expect(personalEntrySubmitPreconditionBlocker(base)).toBe("missing_target");
		expect(personalEntrySubmitBlockers(base, ["changes_requested"])).toEqual([
			"missing_target",
			"missing_title",
		]);
		expect(
			personalEntrySubmitBlockers({ ...base, entryId: "e", status: "archived" }, ["conflict"]),
		).toEqual(["archived", "open_request"]);
	});
});

describe("personal-version display fields are ACL-checked", () => {
	test("linked: title, main and drift shown while readable, withheld after access is lost", async () => {
		const f = await makeFixture("linkedDrifted");
		const entryId = f.entryId as string;
		const before = await knowledgeBranchService.getMineForViewer(author, f.draftId);
		expect(before.entryTitle).toStartWith(`${TAG}-entry-`);
		expect(before.currentMainContent).toBe("line Z\nline A\n");
		expect(before.drift).toEqual({ drifted: true, versionsBehind: 1 });
		expect(before.viewerCaps.canRebase).toBe(true);
		const listed = (await knowledgeBranchService.listMineForViewer(author)).find(
			(r) => r.id === f.draftId,
		);
		expect(listed?.entryTitle).toBe(before.entryTitle);
		expect(listed?.drifted).toBe(true);
		expect("content" in (listed ?? {})).toBe(false);

		await knowledgeService.updateEntryAcl(entryId, { classificationLevel: "confidential" });
		const after = await knowledgeBranchService.getMineForViewer(author, f.draftId);
		expect(after.entryId).toBe(entryId);
		expect(after.entryTitle).toBeNull();
		expect(after.currentMainContent).toBeNull();
		expect(after.drift).toBeNull();
		// Rebase reads main → refused, so the cap is off too.
		expect(after.viewerCaps.canRebase).toBe(false);
		expect(await accepted(() => knowledgeBranchService.rebaseDraft(author, f.draftId))).toBe(false);
		// The owner's own body is still theirs.
		expect(after.content).toBe("line A\nline B\n");
		const relisted = (await knowledgeBranchService.listMineForViewer(author)).find(
			(r) => r.id === f.draftId,
		);
		expect(relisted?.entryTitle).toBeNull();

		// An admin (who can read it) sees the title on the same personal entry.
		const adminView = await knowledgeBranchService.getMineForViewer(admin, f.draftId);
		expect(adminView.entryTitle).toBe(before.entryTitle);
	});

	test("standalone: target collection name only while the collection is readable", async () => {
		const f = await makeFixture("standalone");
		const view = await knowledgeBranchService.getMineForViewer(author, f.draftId);
		expect(view.targetCollectionName).toStartWith(`${TAG}-col-`);
		expect(view.entryTitle).toBeNull();
		expect(view.drift).toBeNull();

		await knowledgeAcl.updateCollectionAcl(f.collectionId, { classificationLevel: "confidential" });
		const hidden = await knowledgeBranchService.getMineForViewer(author, f.draftId);
		expect(hidden.targetCollectionId).toBe(f.collectionId);
		expect(hidden.targetCollectionName).toBeNull();
		const listed = (await knowledgeBranchService.listMineForViewer(author)).find(
			(r) => r.id === f.draftId,
		);
		expect(listed?.targetCollectionName).toBeNull();
	});

	test("owner-only: another user gets not found", async () => {
		const f = await makeFixture("linked");
		expect(await accepted(() => knowledgeBranchService.getMineForViewer(stranger, f.draftId))).toBe(
			false,
		);
		expect(
			(await knowledgeBranchService.listMineForViewer(stranger)).some((r) => r.id === f.draftId),
		).toBe(false);
	});
});

/** The real knowledge router behind a fake session principal. */
function appFor(principal: Principal) {
	const app = new Hono();
	app.use("*", async (c, next) => {
		c.set("user", { sub: principal.userId, role: principal.role, iat: 0, exp: 4102444800 });
		await next();
	});
	app.onError((error) => {
		if (error instanceof AppError) {
			return new Response(JSON.stringify({ error: error.message }), {
				status: error.statusCode,
			});
		}
		throw error;
	});
	app.route("/knowledge", knowledgeRoutes);
	return app;
}

describe("HTTP", () => {
	test("GET /personal-entries[/:id] serve the viewer payloads", async () => {
		const f = await makeFixture("linkedPending");
		const detail = await appFor(author).request(`/knowledge/personal-entries/${f.draftId}`);
		expect(detail.status).toBe(200);
		const body = (await detail.json()) as {
			entryTitle: string | null;
			viewerCaps: { canSubmit: boolean; submitBlockers: string[] };
			openSubmissions: { status: string }[];
		};
		expect(body.entryTitle).toStartWith(`${TAG}-entry-`);
		expect(body.viewerCaps).toMatchObject({ canSubmit: false, submitBlockers: ["open_request"] });
		expect(body.openSubmissions.map((s) => s.status)).toEqual(["pending"]);

		const list = await appFor(author).request("/knowledge/personal-entries?status=active");
		const rows = (await list.json()) as { id: string; entryTitle: string | null }[];
		expect(rows.find((r) => r.id === f.draftId)?.entryTitle).toBe(body.entryTitle);

		const strangerRes = await appFor(stranger).request(`/knowledge/personal-entries/${f.draftId}`);
		expect(strangerRes.status).toBe(404);
	});
});
