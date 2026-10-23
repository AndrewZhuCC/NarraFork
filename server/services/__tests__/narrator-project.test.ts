/**
 * Which project a narrator belongs to.
 *
 * This existed in several copies that disagreed, and each disagreement is pinned here
 * because project membership gates access — a resolver that answers "no project" where
 * one exists reads as "nothing to enforce".
 *
 *  1. contextProjectId → that project (explicit)
 *  2. no contextProjectId, a primary link derived from the cwd → the linked project
 *  3. contextProjectId AND a link to another project → the explicit project wins
 *  4. only an additional link → null: additional links never confer membership
 *  5. neither → null, meaning "no project gate applies", never "unrestricted"
 *
 * The row resolver, the by-id resolver and the SQL column must agree on all of them.
 *
 * Run: NARRAFORK_ALLOW_MULTIPLE=1 NARRAFORK_HOME=$(mktemp -d) \
 *      bun test server/services/__tests__/narrator-project.test.ts
 */
import { beforeAll, describe, expect, test } from "bun:test";
import { eq } from "drizzle-orm";
import { db } from "../../db";
import { narratorProjectLinks, narrators, projects } from "../../db/schema";
import { generateId } from "../../lib/id";
import {
	narratorProjectIdColumn,
	resolveNarratorProjectId,
	resolveProjectIdForNarratorId,
} from "../narrator-project";

const TAG = Date.now();

let projectA: string;
let projectB: string;

async function makeNarrator(fields: {
	contextProjectId?: string | null;
	link?: { projectId: string; role: "primary" | "additional" };
}): Promise<string> {
	const id = generateId();
	const now = new Date().toISOString();
	const directory = fields.link ? `/tmp/proj-${TAG}-${fields.link.projectId}` : null;
	await db.insert(narrators).values({
		id,
		cwd: directory,
		contextProjectId: fields.contextProjectId ?? null,
		createdAt: now,
		updatedAt: now,
	});
	if (fields.link)
		await db.insert(narratorProjectLinks).values({
			narratorId: id,
			projectId: fields.link.projectId,
			role: fields.link.role,
			source: "cwd",
			directory,
			updatedAt: now,
		});
	return id;
}

async function rowOf(id: string) {
	const row = await db.query.narrators.findFirst({
		where: eq(narrators.id, id),
		columns: { id: true, contextProjectId: true },
	});
	if (!row) throw new Error(`missing narrator ${id}`);
	return row;
}

beforeAll(async () => {
	const now = new Date().toISOString();
	projectA = generateId();
	projectB = generateId();
	for (const [id, name] of [
		[projectA, `proj-a-${TAG}`],
		[projectB, `proj-b-${TAG}`],
	] as const) {
		await db.insert(projects).values({
			id,
			name,
			gitPath: `/tmp/${name}`,
			createdAt: now,
			updatedAt: now,
		});
	}
});

describe("resolveNarratorProjectId", () => {
	test("a narrator with contextProjectId resolves to it", async () => {
		// An old copy read only the other source of membership, so this returned null and
		// any project-scoped decision silently found nothing to enforce.
		const id = await makeNarrator({ contextProjectId: projectB });
		expect(await resolveNarratorProjectId(await rowOf(id))).toBe(projectB);
	});

	test("a narrator whose cwd was indexed resolves through its primary link", async () => {
		const id = await makeNarrator({ link: { projectId: projectA, role: "primary" } });
		expect(await resolveNarratorProjectId(await rowOf(id))).toBe(projectA);
	});

	test("the explicit project wins over a project derived from the directory", async () => {
		const id = await makeNarrator({
			contextProjectId: projectB,
			link: { projectId: projectA, role: "additional" },
		});
		expect(await resolveNarratorProjectId(await rowOf(id))).toBe(projectB);
	});

	test("an additional link alone confers no project", async () => {
		const id = await makeNarrator({ link: { projectId: projectA, role: "additional" } });
		expect(await resolveNarratorProjectId(await rowOf(id))).toBeNull();
	});

	test("a narrator with neither resolves to null", async () => {
		const id = await makeNarrator({});
		expect(await resolveNarratorProjectId(await rowOf(id))).toBeNull();
		expect(await resolveNarratorProjectId({ id, contextProjectId: null })).toBeNull();
	});
});

describe("resolveProjectIdForNarratorId", () => {
	test("resolves an indexed narrator by id", async () => {
		const id = await makeNarrator({ link: { projectId: projectA, role: "primary" } });
		expect(await resolveProjectIdForNarratorId(id)).toBe(projectA);
	});

	test("resolves an explicit narrator by id", async () => {
		const id = await makeNarrator({ contextProjectId: projectB });
		expect(await resolveProjectIdForNarratorId(id)).toBe(projectB);
	});

	test("an unknown narrator id resolves to null instead of throwing", async () => {
		expect(await resolveProjectIdForNarratorId("no-such-narrator")).toBeNull();
	});

	test("the row, id and SQL variants agree for every shape", async () => {
		// The entry points must never diverge — that is how the copies happened.
		const shapes = [
			{ contextProjectId: projectB },
			{ link: { projectId: projectA, role: "primary" as const } },
			{ contextProjectId: projectB, link: { projectId: projectA, role: "additional" as const } },
			{ link: { projectId: projectA, role: "additional" as const } },
			{},
		];
		for (const shape of shapes) {
			const id = await makeNarrator(shape);
			const fromRow = await resolveNarratorProjectId(await rowOf(id));
			const sqlRow = await db
				.select({ projectId: narratorProjectIdColumn })
				.from(narrators)
				.where(eq(narrators.id, id))
				.get();
			expect({ shape, byId: await resolveProjectIdForNarratorId(id) }).toEqual({
				shape,
				byId: fromRow,
			});
			expect({ shape, sql: sqlRow?.projectId ?? null }).toEqual({ shape, sql: fromRow });
		}
	});
});
