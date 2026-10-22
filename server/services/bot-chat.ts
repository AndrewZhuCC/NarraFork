/**
 * bot-chat.ts — the bridge between a bot narrator and its conversations.
 *
 * A bot is a narrator whose audience is a chat room rather than its transcript:
 *
 *   person ── post ──▶ bot room ── admitBotPost ──▶ the bot's next input
 *   person ◀───────── bot room ◀── sendAsBot ◀──── Send({ user }) in the bot
 *
 * Inbound, posting in a bot room is typing into the bot's composer: chat-service
 * hands the post to `admitBotPost` and records it only once the bot has taken it, so
 * a bot that can't (its queue is full) is the poster's answer at once, as in the
 * composer. The input is authored by the poster, so the model reads who is speaking
 * from the ordinary sender marker. Outbound is `sendAsBot`, the only way a bot
 * speaks: its replies and tool calls stay in the transcript, which is what the
 * monitor shows.
 *
 * Group chat keeps both shapes: a post goes to every bot in the room except its
 * author, and Send addresses a room. What changes is admission, since one post then
 * has several bots to reach — see docs/BOT_NARRATOR.md.
 */

import { rmSync } from "node:fs";
import { db } from "@server/db";
import { narrators, users } from "@server/db/schema";
import { buildAttachedFilesHint } from "@server/lib/attached-files";
import { copyChatAttachmentToWorktree } from "@server/lib/chat-attachments";
import { NotFoundError, ValidationError } from "@server/lib/errors";
import { getUserLanguage, getUserReplyInLanguage } from "@server/lib/i18n";
import { isBotNarrator } from "@server/lib/narrator-utils";
import type { TextFileRef } from "@server/lib/uploads";
import { eq, sql } from "drizzle-orm";
import { postBotMessage } from "./chat-service";

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

/** A draft the post claims once recorded, as the copy into the bot's worktree reads it. */
interface PostedFile {
	roomId: string;
	storedName: string;
	filename: string;
	sizeBytes: number;
}

export interface BotPost {
	narratorId: string;
	senderUserId: string;
	text: string;
	/** What the person saw quoted, when the post is a reply. Already bounded. */
	replyToPreview: string | null;
	attachments: readonly PostedFile[];
}

/**
 * Hand a person's post to its bot, before chat-service records it. The bot's own
 * composer path, so the post queues while the bot works and starts a turn when it is
 * idle; it throws when the bot can't take the post (its queue is full, a file could
 * not be copied), and then nothing is recorded and the person still holds it.
 */
export async function admitBotPost(post: BotPost): Promise<void> {
	const [narrator, sender] = await Promise.all([
		db.query.narrators.findFirst({
			where: eq(narrators.id, post.narratorId),
			columns: { id: true, status: true, cwd: true },
		}),
		db.query.users.findFirst({
			where: eq(users.id, post.senderUserId),
			columns: { id: true, username: true, avatarColor: true, avatarImageId: true },
		}),
	]);
	if (!narrator) throw new NotFoundError("Bot", post.narratorId);

	const files = await copyIntoWorktree(narrator.cwd, post.attachments);
	const prompt = [quoteLine(post.replyToPreview), post.text].filter(Boolean).join("\n\n");
	const input = [prompt, buildAttachedFilesHint(files)].filter(Boolean).join("\n");
	try {
		const { narratorService } = await import("./narrator-service");
		// Talking to a bot is interacting with it, exactly like its composer's auto-unarchive.
		if (narrator.status === "archived") await narratorService.updateStatus(narrator.id, "idle");
		const { acceptUserMessage } = await import("./narrator-session");
		await acceptUserMessage(narrator.id, input, {
			locale: await getUserLanguage(post.senderUserId),
			replyInUserLanguage: await getUserReplyInLanguage(post.senderUserId),
			userId: post.senderUserId,
			creator: sender ?? null,
			// Chat is conversation: a post reading "/new …" is something a person said,
			// not a command for the session.
			executionIntent: { literal: true },
		});
	} catch (error) {
		removeCopies(files);
		throw error;
	}
}

/**
 * The quoted message, as the model should read a reply. Only the snapshot preview:
 * it is what the person saw quoted, and it is already bounded.
 */
function quoteLine(preview: string | null): string {
	return preview?.trim() ? `> ${preview.trim()}` : "";
}

/**
 * Copy the post's files into the bot's working directory, where the
 * `<attached_files>` hint names them — the same layout a discussion-room forward
 * uses. All or nothing: a post is the person's whole message, so one file that can't
 * be copied refuses it rather than delivering it without that file.
 */
async function copyIntoWorktree(
	cwd: string | null,
	attachments: readonly PostedFile[],
): Promise<TextFileRef[]> {
	if (attachments.length === 0) return [];
	if (!cwd) throw new ValidationError("This bot has no working directory to receive files");
	const files: TextFileRef[] = [];
	try {
		for (const attachment of attachments) {
			files.push(await copyChatAttachmentToWorktree(cwd, attachment));
		}
	} catch (error) {
		removeCopies(files);
		throw error;
	}
	return files;
}

/** A refused post leaves nothing behind in the bot's worktree. */
function removeCopies(files: readonly TextFileRef[]): void {
	for (const file of files) rmSync(file.filePath, { force: true });
}
