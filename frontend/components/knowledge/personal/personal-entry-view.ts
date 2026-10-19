/**
 * Pure view logic for "My" (`/knowledge/mine`) and the personal-version page
 * (`/knowledge/mine/$personalEntryId`) — KNOWLEDGE_UI_REDESIGN §5.4 / §5.5.
 *
 * Nothing here decides a permission: what may be submitted / edited / rebased comes from the
 * server's `viewerCaps`. These helpers only shape rows and labels.
 */
import type {
	KnowledgeFinding,
	KnowledgeOpenSubmission,
	KnowledgePersonalEntrySummary,
	KnowledgePersonalSubmitBlocker,
	KnowledgeSubmissionOutcome,
	KnowledgeSubmissionStatus,
} from "../../../lib/api";

export type PersonalEntryKind = "linked" | "standalone";

export function personalEntryKind(p: { entryId: string | null }): PersonalEntryKind {
	return p.entryId ? "linked" : "standalone";
}

/**
 * The display name of a personal version: a linked copy is named by its shared entry (its own
 * `title` column is unused), a standalone entry by its own title. Null when neither is
 * available (e.g. the linked entry is no longer readable) — the caller shows a fallback.
 */
export function personalEntryDisplayTitle(p: {
	entryId: string | null;
	title: string | null;
	entryTitle?: string | null;
}): string | null {
	if (p.entryId) return p.entryTitle ?? null;
	return p.title?.trim() ? p.title : null;
}

/** Archived personal versions are hidden on "My" unless the viewer asks for them (§10.3). */
export function visiblePersonalEntries<T extends { status: "active" | "archived" }>(
	rows: readonly T[],
	showArchived: boolean,
): T[] {
	return showArchived ? [...rows] : rows.filter((r) => r.status !== "archived");
}

/** draftId → the newest in-flight request (the list arrives newest first). */
export function openRequestByDraft(
	open: readonly KnowledgeOpenSubmission[],
): Map<string, KnowledgeOpenSubmission> {
	const m = new Map<string, KnowledgeOpenSubmission>();
	for (const s of open) if (!m.has(s.draftId)) m.set(s.draftId, s);
	return m;
}

export interface MyRequestRow {
	id: string;
	draftId: string;
	status: KnowledgeSubmissionStatus;
	/** createdAt for open requests, reviewedAt (falling back to createdAt) for outcomes. */
	at: string;
	findings: KnowledgeFinding[] | null;
}

/**
 * "My requests" (§5.4): open requests first (pending / conflict / changes requested), then recent
 * outcomes. A `changes_requested` request is both open (it waits on the author) and an outcome
 * (it carries findings): it is listed once, under open, with its findings.
 */
export function myRequestSections(
	open: readonly KnowledgeOpenSubmission[],
	outcomes: readonly KnowledgeSubmissionOutcome[],
): { open: MyRequestRow[]; outcomes: MyRequestRow[] } {
	const findingsById = new Map(outcomes.map((o) => [o.id, o.findingsJson]));
	const openIds = new Set(open.map((s) => s.id));
	return {
		open: open.map((s) => ({
			id: s.id,
			draftId: s.draftId,
			status: s.status,
			at: s.createdAt,
			findings: findingsById.get(s.id) ?? null,
		})),
		outcomes: outcomes
			.filter((o) => !openIds.has(o.id))
			.map((o) => ({
				id: o.id,
				draftId: o.draftId,
				status: o.status,
				at: o.reviewedAt ?? o.createdAt,
				findings: o.findingsJson,
			})),
	};
}

/** Request rows whose personal version is not in the loaded list (e.g. archived ones). */
export function hasUnknownDrafts(
	rows: readonly { draftId: string }[],
	known: readonly Pick<KnowledgePersonalEntrySummary, "id">[],
): boolean {
	const ids = new Set(known.map((k) => k.id));
	return rows.some((r) => !ids.has(r.draftId));
}

/** Search params of `/knowledge/mine`: `archived=1` shows archived personal versions. */
export interface KnowledgeMineSearch {
	archived?: true;
}

export function parseKnowledgeMineSearch(search: Record<string, unknown>): KnowledgeMineSearch {
	const v = search.archived;
	return v === true || v === 1 || v === "1" || v === "true" ? { archived: true } : {};
}

/** i18n key of each blocked-submit reason. */
export function submitBlockerKey(blocker: KnowledgePersonalSubmitBlocker): string {
	return `submitBlocker_${blocker}`;
}
