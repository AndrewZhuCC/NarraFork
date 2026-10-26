/**
 * chat-room-kinds.ts — the chat room kinds and the one grouping that matters across them.
 *
 *   - `dm`       — a person's conversation with another person.
 *   - `bot`      — a person's conversation with a bot narrator.
 *   - `narrator` — the discussion room beside a narrator.
 *
 * `dm` and `bot` are a person's own CONVERSATIONS: membership is the ACL, they are
 * listed on the messages page, and their unread counts feed the navigation badge and
 * the notification center. A narrator room belongs to its narrator instead, and its
 * badge lives on that narrator's toolbar.
 *
 * Shared because server listing/badges and the client's cache patches must agree on
 * the grouping exactly, or the badge and the room list drift apart.
 */

export type ChatRoomKind = "dm" | "narrator" | "bot";

/** Whether rooms of this kind are a person's own conversations (see the module note). */
export function isConversationRoomKind(kind: string | null | undefined): boolean {
	return kind === "dm" || kind === "bot";
}
