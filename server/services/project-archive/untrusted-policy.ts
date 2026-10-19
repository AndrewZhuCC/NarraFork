import {
	parseRuntimeRegistration,
	RUNTIME_REGISTRATION_MAX_BYTES,
} from "../../lib/validators/runtime-registration";
import { sanitizeUntrustedReviewBoundaryIds } from "../narrator-review-boundary";
import type { ArchiveValue } from "./main-store";

/** Keep every policy/deny level; external IDs must not carry server boundary provenance. */
export function sanitizeLegacyPolicyRow(row: Record<string, ArchiveValue>): void {
	projectPortableRuntimeRegistration(row);
	for (const field of ["chapter_settings", "oauth_policy_snapshot_json"]) {
		const raw = row[field];
		if (raw == null) continue;
		if (typeof raw !== "string") throw new Error("Invalid legacy policy JSON");
		row[field] = JSON.stringify(sanitizeUntrustedReviewBoundaryIds(JSON.parse(raw)));
	}
}

/** Validate before projection: an invalid or oversized run must not disappear silently. */
export function projectPortableRuntimeRegistration(row: Record<string, ArchiveValue>): void {
	const raw = row.runtime_registration_json;
	if (raw == null) return;
	if (typeof raw !== "string" || Buffer.byteLength(raw, "utf-8") > RUNTIME_REGISTRATION_MAX_BYTES)
		throw new Error("Invalid project runtime registration JSON or byte limit exceeded");
	const registration = parseRuntimeRegistration(JSON.parse(raw));
	const historyPlacement =
		registration.historyPlacement ??
		(registration.run?.deliverTo ? { toolUseId: registration.run.deliverTo.toolUseId } : undefined);
	// A restored archive is historical evidence, never a restoration of execution authority.
	row.runtime_registration_json = JSON.stringify(
		parseRuntimeRegistration({
			version: 1,
			...(registration.modelPool ? { modelPool: registration.modelPool } : {}),
			...(registration.controller ? { controller: registration.controller } : {}),
			...(historyPlacement ? { historyPlacement } : {}),
		}),
	);
}

/** The only portable JSON relationship; run recipient/cancellation/principal are not portable. */
export function portableRuntimeControllerId(row: Record<string, ArchiveValue>): string | undefined {
	const raw = row.runtime_registration_json;
	if (raw == null) return undefined;
	if (typeof raw !== "string" || Buffer.byteLength(raw, "utf-8") > RUNTIME_REGISTRATION_MAX_BYTES)
		throw new Error("Invalid project runtime registration JSON or byte limit exceeded");
	return parseRuntimeRegistration(JSON.parse(raw)).controller?.narratorId;
}
