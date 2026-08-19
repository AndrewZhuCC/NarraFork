/**
 * What counts as sendable content in the narrator composer.
 *
 * Attachments are content on their own: an image-only or text-file-only draft is a
 * complete turn (the backend substitutes a "[user sent image(s)]" placeholder and
 * injects an `<attached_files>` hint), so the send path must not require typed text.
 *
 * Selection comments are the same class of content: a comments-only draft is a
 * complete turn (the composer packs them into a `<comments>` block ahead of the
 * body), so an empty textarea must not block send.
 *
 * Centralizing this keeps the three consumers in agreement — the send handler's
 * early return, the send/queue button's disabled state, and the Enter-key binding
 * that would otherwise be stolen by a pending permission prompt.
 */

export interface ComposerContentState {
	/** Raw textarea value; trimmed here so callers cannot forget to. */
	text: string;
	imageCount: number;
	textFileCount: number;
	/** Staged selection comments (Spec + chat) waiting to ride the next send. */
	commentCount: number;
}

/** True when at least one attachment is staged. */
export function hasComposerAttachments(state: ComposerContentState): boolean {
	return state.imageCount > 0 || state.textFileCount > 0;
}

/** True when the composer has typed text (ignoring whitespace-only input). */
export function hasComposerText(state: ComposerContentState): boolean {
	return state.text.trim().length > 0;
}

/** True when at least one selection-comment draft is staged. */
export function hasComposerComments(state: ComposerContentState): boolean {
	return state.commentCount > 0;
}

/**
 * True when the composer holds anything worth sending. This is the single gate for
 * "may this draft be submitted", covering text-only, attachment-only, comment-only
 * and mixed drafts.
 */
export function hasSendableComposerContent(state: ComposerContentState): boolean {
	return hasComposerText(state) || hasComposerAttachments(state) || hasComposerComments(state);
}
