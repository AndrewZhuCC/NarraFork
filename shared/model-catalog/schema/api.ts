import type { ModelCard } from "../card";
import type { ModelQuery, RawCatalog, RawLocalState } from "./catalog";

/** Common wire shapes. API roots differ between the two applications. */
export interface CatalogUpdateStatus {
	activeVersion: string;
	bundledVersion: string;
	lastCheckedAt?: string;
	pendingVersion?: string;
	lastError?: string;
	autoApply: boolean;
	pinnedVersion: string | null;
	history: Array<{ catalogVersion: string; publishedAt: string }>;
	protectedFieldCount?: number;
	pendingDiff?: {
		added: string[];
		changed: string[];
		removed: string[];
		/** Metadata leaf differences; absence of before/after means inheritance. */
		fields?: Array<{
			target: "model" | "variant";
			id: string;
			path: string;
			before?: unknown;
			after?: unknown;
		}>;
	};
}
/** Complete source metadata plus the local layer, no derived defaults. */
export interface ModelCardSnapshot {
	schemaVersion: 2;
	catalog: RawCatalog;
	local: RawLocalState;
	update: CatalogUpdateStatus;
}
/** GET root/v2 -> snapshot; POST root/v2/mutate -> ModelCardMutation -> snapshot.
 * POST root/v2/resolve -> {query} | {model} -> card (includes actual discovery).
 * POST root/updates/check -> snapshot.
 * POST root/updates/apply -> {version?: string} -> snapshot (staged checked version only).
 * POST root/updates/rollback -> {version: string} -> snapshot (retained version only).
 * PATCH root/updates/settings -> {autoApply?: boolean,pinnedVersion?: string|null} -> snapshot.
 * Stale baseRevision -> HTTP 409. Failed validation -> HTTP 400. Admin writes only.
 */
export interface ModelCatalogResolveRequest {
	query?: ModelQuery;
	model?: string;
}
export type ModelCatalogResolveResponse = ModelCard;
