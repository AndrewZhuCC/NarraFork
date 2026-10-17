/**
 * Knowledge admin area `/knowledge/admin/*` (docs/KNOWLEDGE_UI_REDESIGN.md §4.1, §5.8): the
 * sub-navigation and the admin gate, kept pure so both are unit-tested.
 *
 * Only sections that exist are listed — the ACL audit and knowledge-pack pages (P3) join this
 * list when they are built, never as placeholder links.
 */

export const KNOWLEDGE_ADMIN_SECTIONS = [
	{ to: "/knowledge/admin/levels", labelKey: "adminNavLevels" },
	{ to: "/knowledge/admin/tags", labelKey: "adminNavTags" },
	{ to: "/knowledge/admin/users", labelKey: "adminNavUsers" },
] as const;

export type KnowledgeAdminSection = (typeof KNOWLEDGE_ADMIN_SECTIONS)[number];

/** Where `/knowledge/admin` itself lands. */
export const KNOWLEDGE_ADMIN_DEFAULT_SECTION = "/knowledge/admin/levels" as const;

/** The section a pathname belongs to (exact or nested), or null. */
export function activeKnowledgeAdminSection(pathname: string): KnowledgeAdminSection | null {
	const path = pathname.replace(/\/+$/, "");
	return KNOWLEDGE_ADMIN_SECTIONS.find((s) => path === s.to || path.startsWith(`${s.to}/`)) ?? null;
}

export type KnowledgeAdminGate = "loading" | "allowed" | "denied";

/**
 * Admin-only UI is decided from the current user's role (§8.1 allows `isAdmin` for admin-only
 * pages); the server enforces `requireAdmin` on every endpoint these pages call regardless.
 * While the user is loading nothing is decided; a missing user (error / signed out) is denied.
 */
export function knowledgeAdminGate(
	user: { role?: string | null } | null | undefined,
	isLoading: boolean,
): KnowledgeAdminGate {
	if (user?.role === "admin") return "allowed";
	if (isLoading) return "loading";
	return "denied";
}
