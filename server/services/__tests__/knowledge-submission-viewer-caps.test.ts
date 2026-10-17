/**
 * Submission `viewerCaps` ⇔ action-endpoint consistency (request page, KNOWLEDGE_UI_REDESIGN §5.7).
 *
 * The request page shows reviewer / author actions from `viewerCaps` on
 * `GET /submissions/:id` instead of guessing from role and identity. That is only safe if each
 * flag is true EXACTLY when the corresponding call is accepted. This pins the equivalence over
 * a principal matrix (admin, entry owner, collection owner, write-grant holder, review-tag
 * holder, plain reader, the submitter) × request states (pending, conflict, changes requested,
 * standalone pending). A principal who cannot view the request (404) must have every action
 * refused too.
 *
 * Every mutating probe runs against a FRESH fixture, so one probe cannot change what the next
 * sees. Grants are collection-scoped so nothing leaks into other test files.
 */
import { beforeAll, describe, expect, test } from "bun:test";
import { Hono } from "hono";
import { db } from "../../db";
import { knowledgeTags, users } from "../../db/schema";
import { AppError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { knowledgeRoutes } from "../../routes/knowledge";
import { knowledgeAcl, type Principal } from "../knowledge-acl";
import { knowledgeBranchService } from "../knowledge-branch-service";
import { knowledgeService } from "../knowledge-service";

const TAG = `svc-${Date.now()}`;

type Role =
	| "admin"
	| "entryOwner"
	| "collectionOwner"
	| "writer"
	| "reviewer"
	| "reader"
	| "submitter";
const ROLES: Role[] = [
	"admin",
	"entryOwner",
	"collectionOwner",
	"writer",
	"reviewer",
	"reader",
	"submitter",
];
type State = "pending" | "conflict" | "changes_requested" | "standalone";
const STATES: State[] = ["pending", "conflict", "changes_requested", "standalone"];

const ids = {} as Record<Role, string>;
const usernames = {} as Record<Role, string>;

function principalFor(role: Role): Principal {
	return { userId: ids[role], role: role === "admin" ? "admin" : "user" };
}

function nowIso(): string {
	return new Date().toISOString();
}

async function makeUser(role: "admin" | "user", label: string) {
	const id = generateId();
	const username = `${TAG}-${label}-${generateId(4)}`;
	await db.insert(users).values({ id, username, passwordHash: "x", role, createdAt: nowIso() });
	return { id, username };
}

interface Fixture {
	submissionId: string;
	entryId: string | null;
	entryTitle: string | null;
	collectionName: string;
}

/**
 * A public collection (owned by collectionOwner) with one entry (owned by entryOwner, one review
 * tag), the writer's write grant and the reviewer's review grant scoped to it, and a publish
 * request from `submitter` in the requested state.
 */
async function makeFixture(state: State): Promise<Fixture> {
	const reviewTagId = generateId();
	await db.insert(knowledgeTags).values({
		id: reviewTagId,
		name: `${TAG}-review-${generateId(4)}`,
		controlled: false,
		createdAt: nowIso(),
	});
	const collectionName = `${TAG}-col-${generateId()}`;
	const col = await knowledgeService.createCollection({
		name: collectionName,
		ownerUserId: ids.collectionOwner,
	});
	const entryTitle = `${TAG}-entry-${generateId(4)}`;
	const entry = await knowledgeService.createEntry({
		collectionId: col.id,
		title: entryTitle,
		content: "line A\n",
		ownerUserId: ids.entryOwner,
	});
	await knowledgeService.updateEntryAcl(entry.id, { reviewTags: [reviewTagId] });
	await knowledgeAcl.createGrant({
		collectionId: col.id,
		principalType: "user",
		principalId: ids.writer,
		grantType: "clearance",
		clearanceLevel: "public",
		canWrite: true,
	});
	await knowledgeAcl.createGrant({
		collectionId: col.id,
		principalType: "user",
		principalId: ids.reviewer,
		grantType: "review",
		tagId: reviewTagId,
	});

	const author = principalFor("submitter");
	const admin = principalFor("admin");
	if (state === "standalone") {
		const draft = await knowledgeBranchService.createStandalone(author, {
			title: `${TAG}-proposal-${generateId(4)}`,
			content: "new body\n",
			targetCollectionId: col.id,
		});
		const sub = await knowledgeBranchService.submitForReview(author, draft.id, {});
		return { submissionId: sub.id, entryId: null, entryTitle: null, collectionName };
	}

	const draft = await knowledgeBranchService.createDraft(author, entry.id, {});
	await knowledgeBranchService.updateDraft(author, draft.id, { content: "line B\n" });
	const sub = await knowledgeBranchService.submitForReview(author, draft.id, {});
	if (state === "conflict") {
		// Main moves in a way the proposal's patch cannot apply to → approve lands in conflict.
		await knowledgeService.addRevision(entry.id, {
			content: "line C\n",
			authorUserId: ids.entryOwner,
			principal: principalFor("entryOwner"),
		});
		const res = await knowledgeBranchService.review(admin, sub.id, { verdict: "approve" });
		expect(res.status).toBe("conflict");
	} else if (state === "changes_requested") {
		await knowledgeBranchService.review(admin, sub.id, { verdict: "request_changes" });
	}
	return { submissionId: sub.id, entryId: entry.id, entryTitle, collectionName };
}

async function accepts(fn: () => Promise<unknown>): Promise<boolean> {
	try {
		await fn();
		return true;
	} catch {
		return false;
	}
}

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

interface Caps {
	isSubmitter: boolean;
	canReview: boolean;
	canApprove: boolean;
	canResolveConflict: boolean;
	canWithdraw: boolean;
	canResubmit: boolean;
}

const NO_CAPS: Caps = {
	isSubmitter: false,
	canReview: false,
	canApprove: false,
	canResolveConflict: false,
	canWithdraw: false,
	canResubmit: false,
};

async function detailVia(role: Role, submissionId: string) {
	const res = await appFor(principalFor(role)).request(`/knowledge/submissions/${submissionId}`);
	const text = await res.text();
	return { status: res.status, text, body: res.status === 200 ? JSON.parse(text) : null };
}

/** Caps the page would act on: a 404 means no actions at all. */
async function capsVia(role: Role, submissionId: string): Promise<Caps> {
	const d = await detailVia(role, submissionId);
	if (d.status === 404) return NO_CAPS;
	expect(d.status).toBe(200);
	return d.body.viewerCaps as Caps;
}

beforeAll(async () => {
	for (const role of ROLES) {
		const u = await makeUser(role === "admin" ? "admin" : "user", role);
		ids[role] = u.id;
		usernames[role] = u.username;
	}
});

describe("submission viewerCaps ⇔ action endpoints", () => {
	for (const state of STATES) {
		for (const role of ROLES) {
			test(`${state} × ${role}: every flag matches whether its endpoint accepts`, async () => {
				const p = principalFor(role);

				const fReview = await makeFixture(state);
				expect((await capsVia(role, fReview.submissionId)).canReview).toBe(
					await accepts(() =>
						knowledgeBranchService.review(p, fReview.submissionId, { verdict: "comment_only" }),
					),
				);

				const fApprove = await makeFixture(state);
				expect((await capsVia(role, fApprove.submissionId)).canApprove).toBe(
					await accepts(() =>
						knowledgeBranchService.review(p, fApprove.submissionId, { verdict: "approve" }),
					),
				);

				const fResolve = await makeFixture(state);
				expect((await capsVia(role, fResolve.submissionId)).canResolveConflict).toBe(
					await accepts(() =>
						knowledgeBranchService.resolveConflict(p, fResolve.submissionId, {
							resolvedContent: "merged\n",
						}),
					),
				);

				const fWithdraw = await makeFixture(state);
				expect((await capsVia(role, fWithdraw.submissionId)).canWithdraw).toBe(
					await accepts(() => knowledgeBranchService.withdrawSubmission(p, fWithdraw.submissionId)),
				);

				const fResubmit = await makeFixture(state);
				expect((await capsVia(role, fResubmit.submissionId)).canResubmit).toBe(
					await accepts(() => knowledgeBranchService.resubmit(p, fResubmit.submissionId)),
				);
			});
		}
	}
});

describe("submission detail: visibility and display metadata", () => {
	test("the matrix discriminates: reviewer acts, submitter withdraws, reader gets 404", async () => {
		const f = await makeFixture("pending");
		const reviewer = await capsVia("reviewer", f.submissionId);
		expect(reviewer).toMatchObject({ canReview: true, canApprove: true, canWithdraw: false });
		const author = await capsVia("submitter", f.submissionId);
		expect(author).toMatchObject({
			isSubmitter: true,
			canReview: false,
			canApprove: false,
			canWithdraw: true,
			canResubmit: false,
		});
		// An admin may both review (self-review exemption is per-admin) and clean up.
		const admin = await capsVia("admin", f.submissionId);
		expect(admin).toMatchObject({ isSubmitter: false, canReview: true, canWithdraw: true });
	});

	test("unviewable requests are 404 with no title, body or change note in the response", async () => {
		const f = await makeFixture("pending");
		for (const role of ["reader", "writer"] as const) {
			const d = await detailVia(role, f.submissionId);
			expect(d.status).toBe(404);
			expect(d.text).not.toContain(f.entryTitle as string);
			expect(d.text).not.toContain("line B");
		}
		// Standalone: a review-tag holder who can read but not write the target collection used
		// to get a 400 naming the permission; any refusal on the read path is now a 404.
		const s = await makeFixture("standalone");
		expect((await detailVia("reviewer", s.submissionId)).status).toBe(404);
		expect((await detailVia("reader", s.submissionId)).status).toBe(404);
	});

	test("linked detail carries entry title, collection name, usernames, contents and drift", async () => {
		const f = await makeFixture("conflict");
		const d = await detailVia("reviewer", f.submissionId);
		expect(d.status).toBe(200);
		expect(d.body).toMatchObject({
			entryTitle: f.entryTitle,
			collectionName: f.collectionName,
			submitterUsername: usernames.submitter,
			reviewerUsername: usernames.admin,
			baseContent: "line A\n",
			currentMainContent: "line C\n",
			proposedContent: "line B\n",
			drift: { drifted: true, versionsBehind: 1 },
		});
		expect(d.body.viewerCaps).toMatchObject({ canResolveConflict: true, canApprove: false });
		expect(d.body.history.map((h: { id: string }) => h.id)).toContain(f.submissionId);
	});

	test("resubmit rounds show up in each other's history", async () => {
		const f = await makeFixture("changes_requested");
		const next = await knowledgeBranchService.resubmit(principalFor("submitter"), f.submissionId);
		const d = await detailVia("submitter", next.id);
		expect(d.body.round).toBe(2);
		expect(d.body.previousSubmissionId).toBe(f.submissionId);
		const ids = d.body.history.map((h: { id: string }) => h.id);
		expect(ids).toEqual([next.id, f.submissionId]);
	});

	test("standalone detail names the target collection and has no main", async () => {
		const f = await makeFixture("standalone");
		const d = await detailVia("collectionOwner", f.submissionId);
		expect(d.status).toBe(200);
		expect(d.body).toMatchObject({
			entryId: null,
			entryTitle: null,
			collectionName: f.collectionName,
			currentMainContent: null,
			drift: null,
		});
		expect(d.body.viewerCaps).toMatchObject({ canApprove: true, canResolveConflict: false });
	});

	test("inbox rows carry ACL-checked display metadata", async () => {
		const f = await makeFixture("pending");
		const res = await appFor(principalFor("reviewer")).request(
			`/knowledge/submissions?entryId=${f.entryId}`,
		);
		const rows = (await res.json()) as Array<Record<string, unknown>>;
		const row = rows.find((r) => r.id === f.submissionId);
		expect(row).toMatchObject({
			entryTitle: f.entryTitle,
			collectionName: f.collectionName,
			submitterUsername: usernames.submitter,
			drifted: false,
		});
	});
});
