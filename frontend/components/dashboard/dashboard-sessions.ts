import type { HumanAttentionItem } from "@shared/human-attention";

/** Fields of a narrator list row the dashboard reads; the list API is loosely typed. */
export interface DashboardNarrator {
	id: string;
	title?: string | null;
	cwd?: string | null;
	status?: string | null;
	substatus?: string | null;
	errorMessage?: string | null;
	lastMessageAt?: string | null;
	updatedAt?: string | null;
}

export type AttentionReason = "decision" | "waiting" | "error";

export interface DashboardSessionRow {
	/** The session the row opens. */
	narratorId: string;
	title: string;
	cwd: string | null;
	status: string;
	time: string | null;
	/** Set only on rows in the attention group. */
	reason?: AttentionReason;
	/** Decision summary or error text. */
	detail?: string;
	/** Decision rows: at least one decision is holding the agent. */
	blocking?: boolean;
	/** Decision rows: number of open decisions in this session. */
	decisionCount?: number;
}

export interface DashboardSessionGroups {
	attention: DashboardSessionRow[];
	running: DashboardSessionRow[];
	recent: DashboardSessionRow[];
}

export const RECENT_SESSION_LIMIT = 5;

const REASON_ORDER: Record<AttentionReason, number> = { decision: 0, waiting: 1, error: 2 };

function toRow(n: DashboardNarrator): DashboardSessionRow {
	return {
		narratorId: n.id,
		title: n.title || n.id.slice(0, 8),
		cwd: n.cwd ?? null,
		status: n.status ?? "idle",
		time: n.lastMessageAt ?? n.updatedAt ?? null,
	};
}

function timeValue(iso: string | null): number {
	const value = iso ? new Date(iso).getTime() : 0;
	return Number.isFinite(value) ? value : 0;
}

/**
 * Split sessions into the three dashboard groups. A session appears in exactly one
 * group, attention winning: a working session with an open decision is shown as
 * needing the user, not as running.
 *
 * Decisions raised by a subagent are attributed to its root session — that is the
 * page that surfaces them (see `groupHumanAttentionByScope`).
 */
export function buildDashboardSessions(input: {
	attentionItems: readonly HumanAttentionItem[];
	working: readonly DashboardNarrator[];
	waiting: readonly DashboardNarrator[];
	recent: readonly DashboardNarrator[];
}): DashboardSessionGroups {
	const known = new Map<string, DashboardNarrator>();
	for (const n of [...input.recent, ...input.waiting, ...input.working]) known.set(n.id, n);

	const attention = new Map<string, DashboardSessionRow>();

	for (const item of input.attentionItems) {
		const ownerId = item.rootNarratorId ?? item.narratorId;
		const existing = attention.get(ownerId);
		if (existing) {
			existing.decisionCount = (existing.decisionCount ?? 1) + 1;
			if (item.blocking && !existing.blocking) {
				existing.blocking = true;
				existing.detail = item.summary || existing.detail;
			}
			continue;
		}
		const narrator = known.get(ownerId);
		const base = narrator
			? toRow(narrator)
			: {
					narratorId: ownerId,
					title: item.narratorTitle || ownerId.slice(0, 8),
					cwd: null,
					status: "waiting",
					time: item.createdAt,
				};
		attention.set(ownerId, {
			...base,
			reason: "decision",
			detail: item.summary || undefined,
			blocking: item.blocking,
			decisionCount: 1,
		});
	}

	for (const n of input.waiting) {
		if (!attention.has(n.id)) attention.set(n.id, { ...toRow(n), reason: "waiting" });
	}

	for (const n of input.recent) {
		if (attention.has(n.id) || !n.errorMessage || n.status === "working") continue;
		attention.set(n.id, { ...toRow(n), reason: "error", detail: String(n.errorMessage) });
	}

	const attentionRows = [...attention.values()].sort(
		(a, b) =>
			REASON_ORDER[a.reason ?? "error"] - REASON_ORDER[b.reason ?? "error"] ||
			Number(Boolean(b.blocking)) - Number(Boolean(a.blocking)) ||
			timeValue(b.time) - timeValue(a.time),
	);

	const running = input.working
		.filter((n) => !attention.has(n.id))
		.map(toRow)
		.sort((a, b) => timeValue(b.time) - timeValue(a.time));
	const shown = new Set([...attention.keys(), ...running.map((r) => r.narratorId)]);

	const recent = input.recent
		.filter((n) => !shown.has(n.id) && n.status !== "working" && n.status !== "waiting")
		.map(toRow)
		.sort((a, b) => timeValue(b.time) - timeValue(a.time))
		.slice(0, RECENT_SESSION_LIMIT);

	return { attention: attentionRows, running, recent };
}

/** Last path segment, for compact directory chips. */
export function directoryLabel(path: string): string {
	const trimmed = path.replace(/[/\\]+$/, "");
	const segment = trimmed.split(/[/\\]/).pop();
	return segment || path;
}
