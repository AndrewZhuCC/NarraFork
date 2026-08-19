/**
 * comment-draft-store.ts — session-scoped selection-comment drafts.
 *
 * Spec markdown and chat messages both feed the same draft list; the composer
 * packs them into the next user message. Drafts are NOT written to spec://
 * files, NOT persisted to the DB, and live only for this tab session.
 *
 * WHY A MODULE-LEVEL STORE
 *
 * Chat (NarratorPanel) and Spec (SpecPanel) are dockview siblings — they do
 * not share a React parent we can wrap without also wrapping terminals,
 * git, browser, etc. Workspace shards one dock surface across many narrators,
 * and the mobile page mounts the two panels without NarratorDockProvider.
 * A store keyed by narratorId is reachable from every entry point without
 * inflating dock context.
 */

import { useSyncExternalStore } from "react";

export type CommentDraftSource = "spec" | "chat";

export type CommentDraft = {
	id: string;
	source: CommentDraftSource;
	excerpt: string;
	comment: string;
	uri?: string;
	revisionId?: string;
	headingPath?: string[];
	messageId?: string;
	messageSeq?: number;
	role?: "user" | "assistant" | "system";
	blockId?: string;
	blockIndex?: number;
};

export type CommentDraftInput = Omit<CommentDraft, "id"> & { id?: string };

type Listener = () => void;

const EMPTY_DRAFTS: CommentDraft[] = [];
const shards = new Map<string, CommentDraft[]>();
const listeners = new Map<string, Set<Listener>>();

function notify(narratorId: string): void {
	const subscribers = listeners.get(narratorId);
	if (!subscribers) return;
	for (const listener of subscribers) listener();
}

function subscribe(narratorId: string, listener: Listener): () => void {
	let subscribers = listeners.get(narratorId);
	if (!subscribers) {
		subscribers = new Set();
		listeners.set(narratorId, subscribers);
	}
	subscribers.add(listener);
	return () => {
		subscribers?.delete(listener);
		if (subscribers && subscribers.size === 0) listeners.delete(narratorId);
	};
}

function write(narratorId: string, next: CommentDraft[]): void {
	if (!narratorId) return;
	const current = shards.get(narratorId);
	if (current === next) return;
	if (next.length === 0) shards.delete(narratorId);
	else shards.set(narratorId, next);
	notify(narratorId);
}

/** Prefer `crypto.randomUUID` when the engine has it; fall back for HTTP / Safari 14. */
export function createCommentDraftId(): string {
	return (
		globalThis.crypto?.randomUUID?.() ??
		`cmt_${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`
	);
}

export function createCommentDraft(input: CommentDraftInput): CommentDraft {
	return {
		...input,
		id: input.id || createCommentDraftId(),
		excerpt: input.excerpt,
		comment: input.comment,
		source: input.source,
	};
}

export function getCommentDrafts(narratorId: string): CommentDraft[] {
	if (!narratorId) return EMPTY_DRAFTS;
	return shards.get(narratorId) ?? EMPTY_DRAFTS;
}

export function addCommentDraft(narratorId: string, input: CommentDraftInput): CommentDraft | null {
	if (!narratorId) return null;
	const created = createCommentDraft(input);
	write(narratorId, [...getCommentDrafts(narratorId), created]);
	return created;
}

export function removeCommentDraft(narratorId: string, id: string): void {
	if (!narratorId || !id) return;
	const current = getCommentDrafts(narratorId);
	const next = current.filter((draft) => draft.id !== id);
	if (next.length === current.length) return;
	write(narratorId, next);
}

export function setCommentDrafts(narratorId: string, drafts: CommentDraft[]): void {
	if (!narratorId) return;
	write(narratorId, drafts.length === 0 ? EMPTY_DRAFTS : drafts.slice());
}

/** Clear and return the previous list so a failed send can restore it. */
export function clearCommentDrafts(narratorId: string): CommentDraft[] {
	const previous = getCommentDrafts(narratorId);
	if (previous.length === 0) return previous;
	write(narratorId, EMPTY_DRAFTS);
	return previous;
}

/** Test helper: drop every narrator's drafts. */
export function clearAllCommentDrafts(): void {
	if (shards.size === 0) return;
	const affected = [...shards.keys()];
	shards.clear();
	for (const narratorId of affected) notify(narratorId);
}

function quoteExcerpt(excerpt: string): string {
	return excerpt
		.split("\n")
		.map((line) => `> ${line}`)
		.join("\n");
}

function formatSpecHeader(draft: CommentDraft): string {
	const uri = draft.uri?.trim() || "spec://";
	const heading = draft.headingPath?.filter(Boolean).join(" > ");
	return heading ? `[spec] ${uri} · ${heading}` : `[spec] ${uri}`;
}

function formatChatHeader(draft: CommentDraft): string {
	const role = draft.role?.trim() || "message";
	return draft.messageSeq != null ? `[chat] ${role} · seq ${draft.messageSeq}` : `[chat] ${role}`;
}

function formatOneDraft(draft: CommentDraft): string {
	const header = draft.source === "spec" ? formatSpecHeader(draft) : formatChatHeader(draft);
	return `${header}\n${quoteExcerpt(draft.excerpt)}\n${draft.comment}`;
}

/** Serialize drafts into the `<comments>` block sent with the user message. */
export function formatCommentDraftsForSend(drafts: readonly CommentDraft[]): string {
	if (drafts.length === 0) return "";
	return `<comments>\n${drafts.map(formatOneDraft).join("\n\n")}\n</comments>`;
}

/**
 * Prepend the comments block to the typed body. Either side may be empty:
 * comments-only is a valid turn, and a body with no drafts is returned as-is.
 */
export function composeUserMessageWithComments(
	text: string,
	drafts: readonly CommentDraft[],
): string {
	const packed = formatCommentDraftsForSend(drafts);
	const body = text.trim();
	if (!packed) return body;
	if (!body) return packed;
	return `${packed}\n\n${body}`;
}

export function useCommentDrafts(narratorId: string | undefined): CommentDraft[] {
	return useSyncExternalStore(
		(listener) => (narratorId ? subscribe(narratorId, listener) : () => {}),
		() => (narratorId ? getCommentDrafts(narratorId) : EMPTY_DRAFTS),
		() => EMPTY_DRAFTS,
	);
}
