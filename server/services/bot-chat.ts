/**
 * bot-chat.ts — the bridge between a bot narrator and its conversations.
 *
 * A bot is a narrator whose audience is a chat room rather than its transcript:
 *
 *   person ── post ──▶ bot room ── deliverToBot ──▶ the bot's next input
 *   person ◀───────── bot room ◀── sendAsBot ◀──── Send({ user }) in the bot
 *
 * Inbound is one more `chat:message_created` listener, beside the WebSocket fan-out
 * and the notification center: chat-service only records what a person posted, and
 * this module decides that the room's bot hears it. The delivery takes the same user
 * input admission as the bot's own composer — queued while it works, starting a turn
 * when idle — and is authored by the poster, so the model reads who is speaking from
 * the ordinary sender marker. Outbound is `sendAsBot`, the only way a bot speaks:
 * its replies and tool calls stay in the transcript, which is what the monitor shows.
 *
 * Group chat generalizes both directions without changing their shape: a post goes
 * to every bot in the room except its author, and Send addresses a room.
 */

import { db } from "@server/db";
import { chatAttachments, chatMessages, narrators, users } from "@server/db/schema";
import { AsyncMutex } from "@server/lib/async-mutex";
import { NotFoundError, ValidationError } from "@server/lib/errors";
import { eventBus } from "@server/lib/event-bus";
import { getUserLanguage, getUserReplyInLanguage } from "@server/lib/i18n";
import { logger } from "@server/lib/logger";
import { isBotNarrator } from "@server/lib/narrator-utils";
import { and, eq, sql } from "drizzle-orm";
import { materializeAttachmentsForNarrator, postBotMessage } from "./chat-service";

// ─────────────────────────────────────────────────────────────────────────────
// Outbound: Send({ user }) → the person's bot room
// ─────────────────────────────────────────────────────────────────────────────

export interface BotSendResult {
	userId: string;
	username: string;
	roomId: string;
	messageId: string;
}

/**
 * Deliver a bot's message to one person, by username.
 *
 * The bot check is authoritative here, not in the tool schema: the schema only
 * decides what the model is offered, and a stale or hand-built call must not let an
 * ordinary narrator write into people's conversations.
 */
export async function sendAsBot(input: {
	narratorId: string;
	user: string;
	message: string;
}): Promise<BotSendResult> {
	const bot = await db.query.narrators.findFirst({
		where: eq(narrators.id, input.narratorId),
		columns: { id: true, traits: true },
	});
	if (!bot || !isBotNarrator(bot.traits)) {
		throw new ValidationError("Only bot narrators can send messages to users");
	}
	const recipient = await resolveRecipient(input.user);
	const message = await postBotMessage({
		narratorId: bot.id,
		userId: recipient.id,
		text: input.message,
	});
	return {
		userId: recipient.id,
		username: recipient.username,
		roomId: message.roomId,
		messageId: message.id,
	};
}

/**
 * A username as a model writes it: exact first (the unique index), then a unique
 * case-insensitive match, because "send this to Bob" names the person, not the
 * stored spelling. A leading "@" is a mention habit, not part of the name.
 */
async function resolveRecipient(raw: string): Promise<{ id: string; username: string }> {
	const name = raw.trim().replace(/^@/, "");
	if (!name) throw new ValidationError("user is required");
	const exact = await db.query.users.findFirst({
		where: eq(users.username, name),
		columns: { id: true, username: true },
	});
	if (exact) return exact;
	// Unindexed, but over the account table of a self-hosted deployment, and LIMIT 2 is
	// all an ambiguity test needs.
	const folded = await db
		.select({ id: users.id, username: users.username })
		.from(users)
		.where(sql`lower(${users.username}) = lower(${name})`)
		.limit(2);
	if (folded.length === 1) return folded[0];
	if (folded.length > 1) {
		throw new ValidationError(`"${name}" matches several users; use the exact username`);
	}
	throw new NotFoundError("User", name);
}

// ─────────────────────────────────────────────────────────────────────────────
// Inbound: a person's post → the bot's next input
// ─────────────────────────────────────────────────────────────────────────────

interface PostedEvent {
	roomId: string;
	messageId: string;
	senderUserId: string | null;
	roomKind: string;
	narratorId: string | null;
}

/**
 * One FIFO per bot, so posts reach it in the order they were made: a post carrying
 * an attachment (copied into the bot's worktree first) must not be overtaken by a
 * text-only post that followed it.
 */
const deliveryOrder = new AsyncMutex();

/** Hand a person's post to the room's bot. Exported for tests; production goes through the bus. */
export async function deliverToBot(event: PostedEvent): Promise<void> {
	// The bot's own messages are its output, never its input.
	if (event.roomKind !== "bot" || !event.senderUserId || !event.narratorId) return;
	const userId = event.senderUserId;
	const narratorId = event.narratorId;

	const message = await db.query.chatMessages.findFirst({
		where: and(eq(chatMessages.id, event.messageId), eq(chatMessages.roomId, event.roomId)),
		columns: { id: true, contentText: true, replyToPreview: true, deletedAt: true },
	});
	if (!message || message.deletedAt) return;

	const prompt = [quoteLine(message.replyToPreview), message.contentText.trim()]
		.filter(Boolean)
		.join("\n\n");
	const attachmentHint = await materializeForBot(event, narratorId, userId);
	const input = [prompt, attachmentHint].filter(Boolean).join("\n");
	if (!input) return;

	const [narrator, sender] = await Promise.all([
		db.query.narrators.findFirst({
			where: eq(narrators.id, narratorId),
			columns: { id: true, status: true },
		}),
		db.query.users.findFirst({
			where: eq(users.id, userId),
			columns: { id: true, username: true, avatarColor: true, avatarImageId: true },
		}),
	]);
	if (!narrator) return;
	const { narratorService } = await import("./narrator-service");
	// Talking to a bot is interacting with it, exactly like its composer's auto-unarchive.
	if (narrator.status === "archived") await narratorService.updateStatus(narratorId, "idle");

	const { acceptUserMessage } = await import("./narrator-session");
	await acceptUserMessage(narratorId, input, {
		locale: await getUserLanguage(userId),
		replyInUserLanguage: await getUserReplyInLanguage(userId),
		userId,
		creator: sender ?? null,
	});
}

/**
 * The quoted message, as the model should read a reply. Only the snapshot preview:
 * it is what the person saw quoted, and it is already bounded.
 */
function quoteLine(preview: string | null): string {
	return preview?.trim() ? `> ${preview.trim()}` : "";
}

/**
 * Copy the post's attachments into the bot's working directory and return the
 * `<attached_files>` hint — the same path, and the same wording, as forwarding them
 * from a discussion room. A failure loses the files but never the message.
 */
async function materializeForBot(
	event: PostedEvent,
	narratorId: string,
	userId: string,
): Promise<string> {
	const attachments = await db.query.chatAttachments.findMany({
		where: eq(chatAttachments.messageId, event.messageId),
		columns: { id: true },
	});
	if (attachments.length === 0) return "";
	try {
		const result = await materializeAttachmentsForNarrator({
			roomId: event.roomId,
			userId,
			narratorId,
			attachmentIds: attachments.map((attachment) => attachment.id),
		});
		return result.files.length > 0 ? result.hint : "";
	} catch (error) {
		logger.warn("Bot chat attachments were not delivered", {
			narratorId,
			messageId: event.messageId,
			error: error instanceof Error ? error.message : String(error),
		});
		return "";
	}
}

let registered = false;

/** Register the inbound bridge. Idempotent. */
export function initBotChat(): void {
	if (registered) return;
	registered = true;
	eventBus.on("chat:message_created", (event) => {
		if (event.roomKind !== "bot" || !event.senderUserId || !event.narratorId) return;
		const narratorId = event.narratorId;
		void deliveryOrder
			.acquire(narratorId, () => deliverToBot(event))
			.catch((error) => {
				// The post is already in the room; what is lost is the bot hearing it, so
				// this is an error, not a warning.
				logger.error("Bot chat delivery failed", {
					narratorId,
					roomId: event.roomId,
					messageId: event.messageId,
					error: error instanceof Error ? error.message : String(error),
				});
			});
	});
}
