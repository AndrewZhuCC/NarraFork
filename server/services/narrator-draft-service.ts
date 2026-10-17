import { type ComposerDocument, readComposerDocument } from "@shared/composer-document";
import type { FileReference } from "@shared/file-reference";
import { and, eq, inArray, isNotNull, ne, or, sql } from "drizzle-orm";
import { db } from "../db";
import { narratorDrafts } from "../db/schema";
import { copyFileReference } from "../lib/agent/file-reference-projection";
import { narratorDraftLock } from "../lib/async-mutex";
import {
	parseOptionalComposerDocument,
	validateComposerProjection,
} from "../lib/composer-document";
import { generateId } from "../lib/id";

export interface NarratorDraftState {
	hasDraft: boolean;
	text: string;
	fileReferences: FileReference[];
	composerDocument?: ComposerDocument;
	revision: number;
	updatedAt: string | null;
	updatedBy: string | null;
	sourceId: string | null;
}

export interface NarratorDraftUpdate extends NarratorDraftState {
	previousHasDraft: boolean;
}

export interface NarratorDraftConflict {
	conflict: true;
	current: NarratorDraftState;
}

export type NarratorDraftUpdateResult = NarratorDraftUpdate | NarratorDraftConflict;

function emptyDraftState(): NarratorDraftState {
	return {
		hasDraft: false,
		text: "",
		fileReferences: [],
		revision: 0,
		updatedAt: null,
		updatedBy: null,
		sourceId: null,
	};
}

function rowToDraftState(
	row:
		| {
				text: string;
				fileReferencesJson?: string | null;
				composerDocument?: ComposerDocument | null;
				revision: number;
				updatedAt: string;
				sourceId: string | null;
		  }
		| undefined,
	userId: string,
): NarratorDraftState {
	if (!row) return emptyDraftState();
	const fileReferences: FileReference[] = row.fileReferencesJson
		? (JSON.parse(row.fileReferencesJson) as FileReference[]).map(copyFileReference)
		: [];
	const composerDocument = readComposerDocument(row.composerDocument);
	return {
		hasDraft: !!row.text.trim() || fileReferences.length > 0 || !!composerDocument?.assets.length,
		text: row.text,
		fileReferences,
		composerDocument,
		revision: row.revision,
		updatedAt: row.updatedAt,
		updatedBy: userId,
		sourceId: row.sourceId,
	};
}

/**
 * A legacy text-only client cannot move token offsets. Retain only occurrences
 * whose existing, nonempty token still occupies exactly the same input range.
 * Detached chips have no verifiable range and cannot survive an omitted field.
 */
export function retainMatchingDraftFileReferences(
	previousText: string,
	text: string,
	references: readonly FileReference[],
): FileReference[] {
	return references.filter((reference) => {
		const range = reference.inputRange;
		if (!range) return false;
		const [start, end] = range;
		return (
			Number.isInteger(start) &&
			Number.isInteger(end) &&
			start >= 0 &&
			end > start &&
			end <= previousText.length &&
			end <= text.length &&
			previousText.slice(start, end) === text.slice(start, end)
		);
	});
}

const draftColumns = {
	text: narratorDrafts.text,
	fileReferencesJson: narratorDrafts.fileReferencesJson,
	composerDocument: narratorDrafts.composerDocument,
	revision: narratorDrafts.revision,
	updatedAt: narratorDrafts.updatedAt,
	sourceId: narratorDrafts.sourceId,
};

export async function getNarratorDraft(
	userId: string,
	narratorId: string,
): Promise<NarratorDraftState> {
	const row = await db
		.select(draftColumns)
		.from(narratorDrafts)
		.where(and(eq(narratorDrafts.userId, userId), eq(narratorDrafts.narratorId, narratorId)))
		.get();
	return rowToDraftState(row, userId);
}

/** First-paint metadata never materializes draft text or reference JSON. */
export async function getNarratorDraftMetadata(
	userId: string,
	narratorId: string,
): Promise<Pick<NarratorDraftState, "hasDraft" | "revision" | "updatedAt">> {
	const row = await db
		.select({
			hasDraft: sql<number>`CASE WHEN length(trim(${narratorDrafts.text})) > 0 OR
			(${narratorDrafts.fileReferencesJson} IS NOT NULL AND ${narratorDrafts.fileReferencesJson} <> '[]') OR ${narratorDrafts.composerDocument} IS NOT NULL THEN 1 ELSE 0 END`,
			revision: narratorDrafts.revision,
			updatedAt: narratorDrafts.updatedAt,
		})
		.from(narratorDrafts)
		.where(and(eq(narratorDrafts.userId, userId), eq(narratorDrafts.narratorId, narratorId)))
		.get();
	return {
		hasDraft: !!row?.hasDraft,
		revision: row?.revision ?? 0,
		updatedAt: row?.updatedAt ?? null,
	};
}

export async function updateNarratorDraft(
	userId: string,
	narratorId: string,
	text: string,
	sourceId?: string,
	baseRevision?: number,
	fileReferences?: FileReference[],
	composerDocument?: ComposerDocument,
): Promise<NarratorDraftUpdateResult> {
	composerDocument = parseOptionalComposerDocument(composerDocument);
	validateComposerProjection(composerDocument, text, fileReferences);
	// Capture metadata before awaiting the lock: text and references are one CAS value.
	const suppliedReferences = fileReferences?.map(copyFileReference);
	return narratorDraftLock.acquire(`${userId.length}:${userId}:${narratorId}`, async () => {
		const previous = await db
			.select(draftColumns)
			.from(narratorDrafts)
			.where(and(eq(narratorDrafts.userId, userId), eq(narratorDrafts.narratorId, narratorId)))
			.get();
		const current = rowToDraftState(previous, userId);
		const expectedRevision = baseRevision ?? current.revision;
		if (expectedRevision !== current.revision) {
			return { conflict: true, current };
		}

		const now = new Date().toISOString();
		const storedText = text.trim() || composerDocument?.assets.length ? text : "";
		const storedReferences =
			suppliedReferences ??
			retainMatchingDraftFileReferences(current.text, storedText, current.fileReferences);
		const fileReferencesJson = storedReferences.length ? JSON.stringify(storedReferences) : null;
		// Omission keeps the previous source attribution for legacy autosave clients.
		const storedSourceId = sourceId ?? current.sourceId ?? null;
		const nextRevision = current.revision + 1;
		const storedComposerDocument =
			storedText.trim() || storedReferences.length || composerDocument?.assets.length
				? composerDocument
				: undefined;
		const fields = {
			text: storedText,
			fileReferencesJson,
			composerDocument: storedComposerDocument ?? null,
			sourceId: storedSourceId,
			revision: nextRevision,
			updatedAt: now,
		};
		await db
			.insert(narratorDrafts)
			.values({ id: generateId(), userId, narratorId, ...fields })
			.onConflictDoUpdate({
				target: [narratorDrafts.userId, narratorDrafts.narratorId],
				set: fields,
			});

		return {
			previousHasDraft: current.hasDraft,
			hasDraft:
				!!storedText.trim() ||
				storedReferences.length > 0 ||
				!!storedComposerDocument?.assets.length,
			text: storedText,
			fileReferences: storedReferences,
			composerDocument: storedComposerDocument,
			revision: nextRevision,
			updatedAt: now,
			updatedBy: userId,
			sourceId: storedSourceId,
		};
	});
}

// Empty reference lists are always stored as NULL. Presence queries return only
// ids: neither the draft text nor the up-to-64-KiB locator JSON is materialized.
function draftPresent() {
	return or(
		ne(narratorDrafts.text, ""),
		isNotNull(narratorDrafts.fileReferencesJson),
		isNotNull(narratorDrafts.composerDocument),
	);
}

export async function narratorHasDraft(userId: string, narratorId: string): Promise<boolean> {
	const row = await db
		.select({ narratorId: narratorDrafts.narratorId })
		.from(narratorDrafts)
		.where(
			and(
				eq(narratorDrafts.userId, userId),
				eq(narratorDrafts.narratorId, narratorId),
				draftPresent(),
			),
		)
		.get();
	return !!row;
}

export async function getNarratorIdsWithDraft(
	userId: string,
	narratorIds: string[],
): Promise<Set<string>> {
	if (narratorIds.length === 0) return new Set();
	const rows = await db
		.select({ narratorId: narratorDrafts.narratorId })
		.from(narratorDrafts)
		.where(
			and(
				eq(narratorDrafts.userId, userId),
				inArray(narratorDrafts.narratorId, narratorIds),
				draftPresent(),
			),
		);
	return new Set(rows.map((row) => row.narratorId));
}
