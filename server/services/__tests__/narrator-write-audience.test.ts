/**
 * The WRITE audience axis — the second half of narrator sharing.
 *
 * `visibility` answers "who may watch"; `writeAudience` answers "who may drive". They
 * are separate columns, and every property below is one that a plausible-looking
 * implementation gets wrong silently:
 *
 *  1. **A read audience never confers write.** Making a session public shares a view
 *     of the work, not the right to approve a Bash call. This is the invariant the new
 *     axis had to preserve while removing the "click every colleague" friction.
 *  2. **The `project` tier requires project WRITE.** A project read member is defined
 *     as "cannot change the project"; letting them drive a session inside it (running
 *     commands, editing files) would go around that line. Critically this must hold
 *     even when the project is `public`, because `canWriteProject` deliberately does
 *     not consult project visibility — a predicate that copied the read gate's
 *     `p.visibility = 'public'` clause would hand write to every signed-in user.
 *  3. **The `project` tier grants nothing without a project.** `resolveProjectGate`
 *     reports `{read: true, write: true}` for a narrator that belongs to none, meaning
 *     "no gate at this level" rather than "unrestricted", so a bare `gate.write` test
 *     would turn this tier into "anyone may write" on standalone sessions.
 *  4. **"Which project" has exactly one definition** — `contextProjectId` first, then
 *     the primary link the project index derived from the cwd; an additional link never
 *     counts. Both the row check and the SQL predicate must use it; the rows below are
 *     the only automated way to catch a divergence.
 *  5. **Write implies read, and never the reverse.** Someone who may drive a session
 *     must be able to see it, but the write audience must NOT be hoisted above the
 *     project gate the way `public` visibility is — that would make read wider than
 *     write and turn the axis into a way to leak transcripts past the gate.
 *  6. **The SQL predicate agrees with the row check** on every one of these, or lists
 *     offer sessions that 404 on open (or silently hide ones that should appear).
 *  7. **Unknown values fail closed.**
 *
 * Run: NARRAFORK_ALLOW_MULTIPLE=1 NARRAFORK_HOME=$(mktemp -d) \
 *      bun test server/services/__tests__/narrator-write-audience.test.ts
 */
import { beforeAll, describe, expect, test } from "bun:test";
import { and, eq, sql } from "drizzle-orm";
import { db } from "../../db";
import { aclGrants, narratorProjectLinks, narrators, projects, users } from "../../db/schema";
import { generateId } from "../../lib/id";
import {
	canReadNarrator,
	canWriteNarrator,
	defaultWriteAudienceForNarrator,
	type NarratorPrincipal,
	narratorReadableWhere,
} from "../narrator-acl";

const TAG = Date.now();

/** Owns the narrators under test. */
let owner: string;
/** Holds project `write` — the tier the `project` write audience requires. */
let writeMember: string;
/** Holds project `read` only — must be able to watch but never drive. */
let readMember: string;
/** In no project at all. */
let outsider: string;

let privateProject: string;
let publicProject: string;

const asUser = (userId: string): NarratorPrincipal => ({ userId, isAdmin: false });
const asAdmin = (): NarratorPrincipal => ({ userId: `wa-admin-${TAG}`, isAdmin: true });

async function makeUser(label: string): Promise<string> {
	const id = generateId();
	await db.insert(users).values({
		id,
		username: `${label}-${TAG}-${generateId(6)}`,
		passwordHash: "x",
		role: "user",
		createdAt: new Date().toISOString(),
	});
	return id;
}

async function makeProject(visibility: "private" | "public"): Promise<string> {
	const id = generateId();
	const now = new Date().toISOString();
	await db.insert(projects).values({
		id,
		name: `wa-project-${TAG}-${generateId(6)}`,
		gitPath: `/tmp/wa-${id}`,
		ownerUserId: owner,
		visibility,
		createdAt: now,
		updatedAt: now,
	});
	return id;
}

/**
 * Insert a narrator, defaulting `visibility` to the narrowest one that keeps the pair
 * LEGAL for the requested write audience.
 *
 * The write audience is nested inside the read audience, so a fixture that defaulted
 * visibility to `private` while asking for `writeAudience: "project"` would be building
 * a combination the service layer refuses to create — the test would then be asserting
 * behaviour for a state that cannot exist in production. Deriving the default keeps each
 * case focused on the write tier it is actually about.
 *
 * `visibility` can still be passed explicitly, including illegal pairs: a few tests need
 * them precisely to prove nothing depends on that state being reachable.
 *
 * `indexedProjectId` places the narrator in a project through its working directory: a
 * link the project index derived from its cwd (`linkRole`, primary by default).
 */
async function makeNarrator(options: {
	writeAudience?: string;
	visibility?: string;
	indexedProjectId?: string | null;
	linkRole?: "primary" | "additional";
	contextProjectId?: string | null;
	ownerUserId?: string | null;
}): Promise<string> {
	const id = generateId();
	const now = new Date().toISOString();
	const writeAudience = options.writeAudience ?? "owner";
	const visibility =
		options.visibility ??
		{ owner: "private", project: "project", public: "public" }[writeAudience] ??
		"private";
	await db.insert(narrators).values({
		id,
		title: `wa-${TAG}`,
		ownerUserId: options.ownerUserId === undefined ? owner : options.ownerUserId,
		cwd: options.indexedProjectId ? `/tmp/wa-${options.indexedProjectId}/work` : null,
		contextProjectId: options.contextProjectId ?? null,
		// Casts: the suite deliberately covers out-of-enum values to prove the checks
		// fail closed, which the columns' TS enums would otherwise forbid.
		visibility: visibility as "private",
		writeAudience: writeAudience as "owner",
		createdAt: now,
		updatedAt: now,
	});
	if (options.indexedProjectId)
		await db.insert(narratorProjectLinks).values({
			narratorId: id,
			projectId: options.indexedProjectId,
			role: options.linkRole ?? "primary",
			source: "cwd",
			directory: `/tmp/wa-${options.indexedProjectId}/work`,
			updatedAt: now,
		});
	return id;
}

/**
 * A narrator explicitly in the public project whose working directory is ALSO indexed
 * into the private one, as an additional link.
 *
 * The resolution rule says the explicit project is primary and an additional link
 * confers nothing, so the private project's members must get no write through it. If SQL
 * and the row check disagree here, they resolve DIFFERENT projects for the same
 * narrator, and no other fixture can catch it.
 */
async function makeAdditionallyLinkedNarrator(): Promise<string> {
	return await makeNarrator({
		writeAudience: "project",
		contextProjectId: publicProject,
		indexedProjectId: privateProject,
		linkRole: "additional",
	});
}

async function grantProject(projectId: string, userId: string, capability: "read" | "write") {
	await db.insert(aclGrants).values({
		id: generateId(),
		scopeType: "project",
		scopeId: projectId,
		principalType: "user",
		principalId: userId,
		capability,
		createdAt: new Date().toISOString(),
	});
}

async function rowOf(narratorId: string) {
	const row = await db.query.narrators.findFirst({ where: eq(narrators.id, narratorId) });
	if (!row) throw new Error(`missing narrator ${narratorId}`);
	return row;
}

/** Ids this principal may read according to the SQL predicate, restricted to `ids`. */
async function readableIdsViaSql(
	principal: NarratorPrincipal,
	ids: string[],
): Promise<Set<string>> {
	const rows = await db
		.select({ id: narrators.id })
		.from(narrators)
		.where(and(sql`${narrators.id} IN ${ids}`, narratorReadableWhere(principal)));
	return new Set(rows.map((row) => row.id));
}

beforeAll(async () => {
	owner = await makeUser("wa-owner");
	writeMember = await makeUser("wa-write");
	readMember = await makeUser("wa-read");
	outsider = await makeUser("wa-outsider");

	privateProject = await makeProject("private");
	await grantProject(privateProject, writeMember, "write");
	await grantProject(privateProject, readMember, "read");

	publicProject = await makeProject("public");
});

// ─── 1. A read audience never confers write ──────────────────────────────

describe("read audience does not grant write", () => {
	test("public visibility with owner-only write stays undrivable", async () => {
		const row = await rowOf(
			await makeNarrator({
				visibility: "public",
				writeAudience: "owner",
				indexedProjectId: privateProject,
			}),
		);
		expect(await canReadNarrator(row, asUser(outsider))).toBe(true);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
	});

	test("the owner and admins always retain write", async () => {
		const row = await rowOf(await makeNarrator({ indexedProjectId: privateProject }));
		expect(await canWriteNarrator(row, asUser(owner))).toBe(true);
		expect(await canWriteNarrator(row, asAdmin())).toBe(true);
	});
});

// ─── 2. The `project` tier requires project WRITE ─────────────────────────

describe("writeAudience=project", () => {
	test("a project write member may drive it", async () => {
		const row = await rowOf(
			await makeNarrator({ writeAudience: "project", indexedProjectId: privateProject }),
		);
		expect(await canWriteNarrator(row, asUser(writeMember))).toBe(true);
		// And can therefore see it, without any narrator-level grant.
		expect(await canReadNarrator(row, asUser(writeMember))).toBe(true);
	});

	test("a project READ member may not — that tier is 'cannot change the project'", async () => {
		const row = await rowOf(
			await makeNarrator({ writeAudience: "project", indexedProjectId: privateProject }),
		);
		expect(await canWriteNarrator(row, asUser(readMember))).toBe(false);
	});

	test("someone outside the project may not", async () => {
		const row = await rowOf(
			await makeNarrator({ writeAudience: "project", indexedProjectId: privateProject }),
		);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
	});

	test("a PUBLIC project does not hand write to every signed-in user", async () => {
		// The read gate treats `p.visibility = 'public'` as passage; the write gate must
		// not. Copying that clause across is the mistake this guards.
		//
		// `visibility` is left project-scoped so the outsider CAN see it: that isolates
		// the assertion to the write tier rather than passing merely because the session
		// was unreadable anyway.
		const row = await rowOf(
			await makeNarrator({
				visibility: "project",
				writeAudience: "project",
				indexedProjectId: publicProject,
			}),
		);
		expect(await canReadNarrator(row, asUser(outsider))).toBe(true);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
		// The project's own write members still can, so the tier is not simply broken.
		expect(await canWriteNarrator(row, asUser(owner))).toBe(true);
	});
});

// ─── 3 & 4. "Which project" has one definition ───────────────────────────

describe("project resolution for the write audience", () => {
	test("grants nothing on a narrator that belongs to no project", async () => {
		// resolveProjectGate(null) reports write:true, meaning "no gate here". Testing it
		// bare would make this tier equivalent to "anyone may write".
		const row = await rowOf(await makeNarrator({ writeAudience: "project" }));
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
		expect(await canWriteNarrator(row, asUser(writeMember))).toBe(false);
	});

	test("honours contextProjectId when the directory is in no project", async () => {
		// Externally provisioned standalone sessions carry the project this way. If
		// `hasProject` used a narrower "indexed directory only" rule, this tier could never
		// work for them while the gate still judged them against the project.
		const row = await rowOf(
			await makeNarrator({ writeAudience: "project", contextProjectId: privateProject }),
		);
		expect(await canWriteNarrator(row, asUser(writeMember))).toBe(true);
		expect(await canWriteNarrator(row, asUser(readMember))).toBe(false);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
	});

	test("an additional directory link confers no project write; the explicit project decides", async () => {
		const row = await rowOf(await makeAdditionallyLinkedNarrator());
		expect(await canWriteNarrator(row, asUser(writeMember))).toBe(false);
		// Control: the same directory as the PRIMARY link does put it in that project.
		const primary = await rowOf(
			await makeNarrator({ writeAudience: "project", indexedProjectId: privateProject }),
		);
		expect(await canWriteNarrator(primary, asUser(writeMember))).toBe(true);
	});
});

// ─── 5. `public` write audience is still behind the gate ─────────────────

describe("writeAudience=public", () => {
	test("anyone who can reach the project may drive it", async () => {
		const row = await rowOf(
			await makeNarrator({ writeAudience: "public", indexedProjectId: publicProject }),
		);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(true);
		expect(await canWriteNarrator(row, asUser(readMember))).toBe(true);
	});

	test("a narrator with no project is drivable by any signed-in user", async () => {
		const row = await rowOf(await makeNarrator({ writeAudience: "public" }));
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(true);
	});

	test("a session in an unreachable project is neither drivable NOR readable", async () => {
		// Both halves matter: the project gate bounds the `public` write tier, so someone
		// who cannot reach the project gets neither the transcript nor the ability to
		// drive — even though the audience says "everyone".
		const row = await rowOf(
			await makeNarrator({
				visibility: "public",
				writeAudience: "public",
				indexedProjectId: privateProject,
			}),
		);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
		// `public` visibility is deliberately wider than the project, so read IS granted
		// here. That is the one place the two levels part company, and it is safe in this
		// direction: seeing the work without being able to change it.
		expect(await canReadNarrator(row, asUser(outsider))).toBe(true);
	});
});

// ─── 6. SQL predicate agrees with the row check ──────────────────────────

describe("list predicate matches single-row decisions", () => {
	test("every write-audience combination decides the same way in SQL and in memory", async () => {
		// The rows that discriminate the easy-to-get-wrong branches, plus controls.
		const publicWaPrivateProject = await makeNarrator({
			visibility: "public",
			writeAudience: "public",
			indexedProjectId: privateProject,
		});
		const projectWaPublicProject = await makeNarrator({
			writeAudience: "project",
			indexedProjectId: publicProject,
		});
		const projectWaContextProject = await makeNarrator({
			writeAudience: "project",
			contextProjectId: privateProject,
		});
		const projectWaAdditionalLink = await makeAdditionallyLinkedNarrator();
		const publicWaNoProject = await makeNarrator({ writeAudience: "public" });
		const ownerOnly = await makeNarrator({ indexedProjectId: privateProject });
		const ids = [
			publicWaPrivateProject,
			projectWaPublicProject,
			projectWaContextProject,
			projectWaAdditionalLink,
			publicWaNoProject,
			ownerOnly,
		];

		for (const principal of [
			asUser(writeMember),
			asUser(readMember),
			asUser(outsider),
			asUser(owner),
		]) {
			const viaSql = await readableIdsViaSql(principal, ids);
			for (const id of ids) {
				const viaRow = await canReadNarrator(await rowOf(id), principal);
				expect({ user: principal.userId, id, sql: viaSql.has(id) }).toEqual({
					user: principal.userId,
					id,
					sql: viaRow,
				});
			}
		}

		// Sanity: the fixture really is mixed, so the agreement above means something.
		const outsiderSees = await readableIdsViaSql(asUser(outsider), ids);
		expect(outsiderSees.has(publicWaNoProject)).toBe(true);
		// `public` visibility reaches past the project gate by design.
		expect(outsiderSees.has(publicWaPrivateProject)).toBe(true);
		expect(outsiderSees.has(ownerOnly)).toBe(false);
	});
});

// ─── 7. Fail closed, and defaults ────────────────────────────────────────

describe("unknown values and defaults", () => {
	test("an unrecognized writeAudience grants nothing, in SQL too", async () => {
		const id = await makeNarrator({
			writeAudience: "everyone-lol",
			indexedProjectId: privateProject,
		});
		const row = await rowOf(id);
		expect(await canWriteNarrator(row, asUser(writeMember))).toBe(false);
		expect(await canWriteNarrator(row, asUser(outsider))).toBe(false);
		expect((await readableIdsViaSql(asUser(outsider), [id])).has(id)).toBe(false);
	});

	test("the default write audience is the widest the read audience permits", () => {
		// Derived from visibility rather than from the project, so a new narrator can
		// never start out with a write audience wider than its read audience.
		expect(defaultWriteAudienceForNarrator("project")).toBe("project");
		expect(defaultWriteAudienceForNarrator("private")).toBe("owner");
		expect(defaultWriteAudienceForNarrator("public")).toBe("public");
		// Unknown visibility falls back to the narrowest tier.
		expect(defaultWriteAudienceForNarrator("nonsense")).toBe("owner");
	});
});
