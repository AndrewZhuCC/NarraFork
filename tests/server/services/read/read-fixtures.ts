/**
 * One logical fixture set for the project read adapters, written to BOTH backends.
 *
 * The point of this module is that SQLite and PostgreSQL receive the *same* rows, so a
 * parity failure means the adapters disagree rather than the fixtures differing. Row
 * objects are therefore plain data with no table references: each side inserts them
 * through its own Drizzle table set (`server/db/schema` vs `server/db/postgres-schema`),
 * which share property names column for column.
 *
 * Every positive case in the matrix has non-empty data behind it, and every negative
 * case points at data that genuinely EXISTS but must not be returned — an empty result
 * compared against an empty result proves nothing about an ACL predicate.
 */

/** Projects deliberately share this `updatedAt`, which is the list cursor's leading key. */
export const T_SAME = "2026-02-01T00:00:00.000Z";
export const T_LATER = "2026-03-01T00:00:00.000Z";
export const T1 = "2026-01-01T00:00:00.000Z";

export const USER = {
	owner: "u_owner",
	admin: "u_admin",
	read: "u_read",
	write: "u_write",
	manage: "u_manage",
	/** Holds an acl_grants row whose `domain_kind` is set — never project access. */
	domain: "u_domain",
	/** Exists, has no grant anywhere. */
	none: "u_none",
	other: "u_other",
	bulk: "u_bulk",
	/**
	 * Owns the mixed-case ordering projects, and has a MIXED-CASE id itself.
	 *
	 * The id matters: `owner_user_id = ?` is the ACL's ownership test, and it is an EQUALITY
	 * comparison rather than an ordering one. Under a deterministic collation (glibc
	 * `en_US.utf8` is deterministic, as is `C`) equality is byte equality either way, so this
	 * pins that the `COLLATE "C"` ordering fix did not have to touch the ACL predicates and
	 * that a mixed-case owner id still matches on both backends.
	 */
	caseMix: "u_CaseMix",
} as const;

/** Never inserted: the "unknown principal" case. */
export const GHOST_USER = "u_ghost_not_in_users";

export const PROJECT = {
	pub: "p_pub",
	pub2: "p_pub2",
	priv: "p_priv",
	arch: "p_arch",
	other: "p_other",
	bulk: "p_bulk",
	/** Owned by the mixed-case ordering user; see {@link CASE_MIX_PROJECT_IDS}. */
	caseMix: "p_CaseMix",
} as const;

/**
 * Ids whose ORDER depends on the collation, so a backend that does not order text by bytes
 * is caught instead of agreeing by accident.
 *
 * Every id here shares the list's sort key (`updatedAt`), which means the id tiebreak alone
 * decides the order — the comparison under test is nothing but text collation.
 *
 * Two divergence shapes, because they fail differently:
 *
 *   - `Zeta` / `alpha`: a plain case reversal. `'Z'` (0x5A) precedes `'a'` (0x61) in byte
 *     order, while a case-insensitive locale puts `alpha` first. This is the shape that
 *     reorders whole pages;
 *   - `Aa` / `aA` / `AA` / `aa`: four ids differing ONLY in case. Byte order groups them by
 *     the case of each position; glibc `en_US.utf8` sorts them adjacently, deciding on a
 *     tertiary weight. This is the shape that moves a single row across a page boundary,
 *     which is the one a cursor turns into a skipped or repeated row.
 *
 * These are all-ASCII on purpose: the bug does not need exotic characters, and using them
 * would invite the reading that this is about Unicode rather than about case.
 */
export const CASE_MIX_PROJECT_IDS = [
	"p_cmZeta",
	"p_cmalpha",
	"p_cmAA",
	"p_cmAa",
	"p_cmaA",
	"p_cmaa",
] as const;

/**
 * The order both backends must return the mixed-case group in: BYTE order.
 *
 * Written out literally rather than computed with `.sort()`. A test that sorts the expected
 * value with the same rule it is checking passes whatever the rule is, and JS `.sort()`
 * happens to be codepoint order — so deriving it would silently assert "the backend agrees
 * with JavaScript" while looking like it asserts a specific sequence. Uppercase letters
 * precede lowercase here (`Z` = 0x5A < `a` = 0x61), which is precisely what glibc
 * `en_US.utf8` does NOT do.
 */
export const CASE_MIX_PROJECT_IDS_BYTE_ORDER = [
	"p_cmAA",
	"p_cmAa",
	"p_cmZeta",
	"p_cmaA",
	"p_cmaa",
	"p_cmalpha",
] as const;

/** Above the adapters' 200-row page clamp, so truncation is observable rather than theoretical. */
export const BULK_PROJECT_COUNT = 205;
/**
 * How many of the 205 bulk rows get an UPPERCASE-marked id; the rest get a lowercase one.
 *
 * Chosen so the two halves interleave differently under the two collations at every boundary
 * the adapter actually stops at — page 1 of a 25-row walk and the 200-row clamp. Under byte
 * order the clamped page holds all 103 uppercase ids plus 97 lowercase ones, while a backend
 * ordering text by a locale (glibc `en_US.utf8`) interleaves the two runs and keeps a
 * DIFFERENT SET at the clamp, not merely a reshuffled one.
 *
 * That is what makes the truncation contract testable rather than nominal: the "which rows
 * survive the clamp" comparison fails instead of passing with a reshuffled but equal set.
 */
const BULK_UPPER_COUNT = 103;

export const userRows = [
	{ id: USER.owner, username: "owner", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{ id: USER.admin, username: "admin", passwordHash: "x", role: "admin" as const, createdAt: T1 },
	{ id: USER.read, username: "reader", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{ id: USER.write, username: "writer", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{ id: USER.manage, username: "manager", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{
		id: USER.domain,
		username: "domained",
		passwordHash: "x",
		role: "user" as const,
		createdAt: T1,
	},
	{ id: USER.none, username: "nobody", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{ id: USER.other, username: "other", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{ id: USER.bulk, username: "bulk", passwordHash: "x", role: "user" as const, createdAt: T1 },
	{
		id: USER.caseMix,
		username: "CaseMix",
		passwordHash: "x",
		role: "user" as const,
		createdAt: T1,
	},
];

export const projectRows = [
	{
		id: PROJECT.pub,
		name: "Public project",
		visibility: "public" as const,
		status: "active" as const,
		ownerUserId: USER.owner,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	{
		id: PROJECT.pub2,
		name: "Public project owned by someone else",
		visibility: "public" as const,
		status: "active" as const,
		ownerUserId: USER.other,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	{
		id: PROJECT.priv,
		name: "Private project",
		visibility: "private" as const,
		status: "active" as const,
		ownerUserId: USER.owner,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	{
		id: PROJECT.arch,
		name: "Archived private project",
		visibility: "private" as const,
		status: "archived" as const,
		ownerUserId: USER.owner,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	{
		id: PROJECT.other,
		name: "Project of another owner",
		visibility: "private" as const,
		status: "active" as const,
		ownerUserId: USER.other,
		createdAt: T1,
		updatedAt: T_LATER,
	},
	{
		id: PROJECT.bulk,
		name: "Bulk project",
		visibility: "private" as const,
		status: "active" as const,
		ownerUserId: USER.bulk,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	{
		id: PROJECT.caseMix,
		name: "Mixed-case ordering project",
		visibility: "private" as const,
		status: "active" as const,
		ownerUserId: USER.caseMix,
		createdAt: T1,
		updatedAt: T_SAME,
	},
	// The mixed-case ordering GROUP: six projects differing only in the case of their ids,
	// all sharing `updatedAt`, so the id tiebreak is the whole comparison. `u_CaseMix` owns
	// them, which keeps them out of every other principal's listing.
	...CASE_MIX_PROJECT_IDS.map((id) => ({
		id,
		name: `Case mix ${id}`,
		visibility: "private" as const,
		status: "active" as const,
		ownerUserId: USER.caseMix,
		createdAt: T1,
		updatedAt: T_SAME,
	})),
];

/**
 * `p_bA001`…`p_bA103` then `p_ba001`…`p_ba102` — MIXED CASE, one shared `updatedAt`.
 *
 * These ids used to be all-lowercase (`p_b001`…), which made every paging and truncation
 * comparison in the suite blind to the defect this fixture now catches: with one case only,
 * byte order and a case-insensitive locale produce the SAME sequence, so PostgreSQL agreed
 * with SQLite no matter which collation it used. Real ids come from `nanoid`, whose alphabet
 * is mixed case, so the all-lowercase fixture was the least realistic choice available.
 *
 * The two case runs are interleaved by NUMBER (`bA001`, `ba001`, `bA002`, …), so the two
 * collations disagree from the very first page rather than only deep in the walk. See
 * {@link BULK_UPPER_COUNT} for the effect at the page clamp.
 */
export function bulkProjectRows() {
	return Array.from({ length: BULK_PROJECT_COUNT }, (_, index) => {
		const upper = index < BULK_UPPER_COUNT;
		const ordinal = upper ? index + 1 : index + 1 - BULK_UPPER_COUNT;
		return {
			id: `p_b${upper ? "A" : "a"}${String(ordinal).padStart(3, "0")}`,
			name: `Bulk ${index + 1}`,
			visibility: "private" as const,
			status: "active" as const,
			ownerUserId: USER.bulk,
			createdAt: T1,
			updatedAt: T_SAME,
		};
	});
}

export function bulkProjectIds(): string[] {
	return bulkProjectRows().map((row) => row.id);
}

/**
 * Grants. `g_domain` is the trap: a project-scoped row with `capability = 'read'` whose
 * `domain_kind` is set is a knowledge credential, and reading it as project access is
 * exactly the leak `domain_kind is null` exists to prevent.
 */
export const aclGrantRows = [
	{
		id: "g_read",
		scopeType: "project" as const,
		scopeId: PROJECT.priv,
		principalType: "user" as const,
		principalId: USER.read,
		capability: "read" as const,
		createdAt: T1,
	},
	{
		id: "g_write",
		scopeType: "project" as const,
		scopeId: PROJECT.priv,
		principalType: "user" as const,
		principalId: USER.write,
		capability: "write" as const,
		createdAt: T1,
	},
	{
		id: "g_manage",
		scopeType: "project" as const,
		scopeId: PROJECT.priv,
		principalType: "user" as const,
		principalId: USER.manage,
		capability: "manage" as const,
		createdAt: T1,
	},
	{
		id: "g_domain",
		scopeType: "project" as const,
		scopeId: PROJECT.priv,
		principalType: "user" as const,
		principalId: USER.domain,
		capability: "read" as const,
		domainKind: "clearance" as const,
		domainValue: "secret",
		createdAt: T1,
	},
];

/**
 * Proof that the mixed-case ids really are collation-sensitive, checked WITHOUT a database.
 *
 * A fixture claiming to expose a collation difference is worthless if a later edit quietly
 * removes the property — the suite would keep passing while testing nothing, which is the
 * exact failure mode that let the original defect hide behind all-lowercase ids. So the
 * property is asserted rather than trusted: a case-insensitive ordering (`localeCompare` with
 * `sensitivity: "base"`, standing in for what glibc `en_US.utf8` does at the primary level)
 * must NOT produce the same sequence as byte order.
 *
 * This is a necessary condition, not the real test. Whether PostgreSQL agrees with SQLite is
 * decided against a live server in the parity matrix, on both a musl and a glibc image.
 */
export function caseInsensitiveOrderDiffers(ids: readonly string[]): boolean {
	const byteOrder = [...ids].sort();
	const caseInsensitive = [...ids].sort((a, b) =>
		a.localeCompare(b, "en", { sensitivity: "base" }),
	);
	return byteOrder.join(",") !== caseInsensitive.join(",");
}
