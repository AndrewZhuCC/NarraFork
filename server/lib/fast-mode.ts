import { type FastModeOverride, normalizeFastModeOverride } from "@shared/fast-mode";

/**
 * Compatibility view of the narrator's speed setting for boolean clients.
 * "inherit" follows the user's ordinary Fast default. "on", "off" and
 * "ultrafast" are explicit overrides; Ultrafast eligibility is checked at send time.
 *
 * The legacy `narrators.fastMode` boolean is only kept as a mirror of the
 * explicit override (see `legacyFastModeMirror`) for older readers of the same
 * database and for project-DB exports.
 */
export function resolveFastMode(override: unknown, userDefault: boolean): boolean {
	const normalized = normalizeFastModeOverride(override);
	if (normalized === "inherit") return userDefault;
	return normalized === "on" || normalized === "ultrafast";
}

/** Deprecated-column mirror: only an explicit "on" is recorded as enabled. */
export function legacyFastModeMirror(override: unknown): boolean {
	return normalizeFastModeOverride(override) === "on";
}

/** Coerce a legacy boolean `fastMode` input into the session override. */
export function fastModeOverrideFromLegacyInput(
	override: FastModeOverride | undefined,
	legacy: boolean | undefined,
): FastModeOverride {
	if (override) return override;
	if (legacy === undefined) return "inherit";
	return legacy ? "on" : "off";
}

/**
 * Load database dependencies only when a preference is actually queried, so
 * importing this module (e.g. from a unit test of the pure resolvers above)
 * never opens a database connection.
 */
async function createUserPreferenceQueryDependencies() {
	const [{ eq }, { db }, { userPreferences }] = await Promise.all([
		import("drizzle-orm"),
		import("../db"),
		import("../db/schema"),
	]);
	return { db, eq, userPreferences };
}

let userPreferenceQueryDependencies:
	| ReturnType<typeof createUserPreferenceQueryDependencies>
	| undefined;

function loadUserPreferenceQueryDependencies() {
	userPreferenceQueryDependencies ??= createUserPreferenceQueryDependencies();
	return userPreferenceQueryDependencies;
}

/**
 * The user's "fast mode by default" preference. Null/unknown users (unattended
 * gateway paths) fall back to disabled.
 */
export async function getUserFastModeDefault(userId: string | null | undefined): Promise<boolean> {
	if (!userId) return false;
	const { db, eq, userPreferences } = await loadUserPreferenceQueryDependencies();
	const pref = await db.query.userPreferences.findFirst({
		where: eq(userPreferences.userId, userId),
		columns: { fastModeDefault: true },
	});
	return pref?.fastModeDefault ?? false;
}

/**
 * Acting user for a subagent turn.
 *
 * A subagent has no owner of its own, and several paths start it without a
 * triggering user (planned-update recovery, detached background restarts, tool
 * calls that predate userId plumbing). Falling back to the parent session's
 * current user keeps "inherit" preferences — fast mode in particular — resolving
 * against a real person instead of silently degrading to the anonymous default.
 */
export function resolveSubagentActingUserId(
	turnUserId: string | null | undefined,
	parentSessionUserId: string | null | undefined,
): string | null {
	return turnUserId ?? parentSessionUserId ?? null;
}

/** Resolve the effective fast mode for a narrator row + the acting user. */
export async function resolveFastModeForUser(
	override: unknown,
	userId: string | null | undefined,
): Promise<boolean> {
	const normalized = normalizeFastModeOverride(override);
	// Skip the preference lookup entirely when the session is pinned.
	if (normalized !== "inherit") return resolveFastMode(normalized, false);
	return getUserFastModeDefault(userId);
}
