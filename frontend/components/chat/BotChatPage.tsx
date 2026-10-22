/**
 * BotChatPage.tsx — A bot narrator's page: the reader's conversation with it.
 *
 * All a person sees of a bot is this room — their own posts and what the bot sent
 * them with `Send({ user })`. Its transcript (replies, reasoning, tool calls) is the
 * monitor, one click away and never the default. Switching views changes nothing
 * about the bot: it is the same narrator either way, so the monitor is a route
 * state of its page rather than anything stored on it.
 *
 * The header is the one place the bot's work shows through: while it thinks the
 * reader would otherwise stare at a silent room, and when it is waiting on a
 * permission decision only the monitor can answer it.
 */

import { Badge, Box, Button, Group, Text } from "@mantine/core";
import { IconEye } from "@tabler/icons-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useBotChatRoom } from "../../hooks/useChat";
import { type NarratorListWSEvent, useNarratorsListWS } from "../../hooks/useNarratorWS";
import { APP_SHELL_FULL_BLEED_HEIGHT } from "../../lib/safe-area";
import { NarratorAvatar } from "../narrator/header/NarratorAvatar";
import { ChatRoomView } from "./ChatRoomView";
import { CHAT_HEADER_HEIGHT } from "./chat-header";

export interface BotChatPageProps {
	narratorId: string;
	title: string | null | undefined;
	avatarImageId?: string | null;
	/** Status when the page loaded; changes arrive over the narrator list stream. */
	status?: string;
	/** Open the transcript (monitor) view of this bot. */
	onMonitor: () => void;
}

export function BotChatPage({
	narratorId,
	title,
	avatarImageId,
	status: loadedStatus,
	onMonitor,
}: BotChatPageProps) {
	const { t } = useTranslation("chat");
	const { data: room, error } = useBotChatRoom(narratorId);

	const [status, setStatus] = useState(loadedStatus);
	useEffect(() => setStatus(loadedStatus), [loadedStatus]);
	const watched = useMemo(() => [narratorId], [narratorId]);
	const onStatus = useCallback(
		(id: string, event: NarratorListWSEvent) => {
			if (id === narratorId && event.type === "status" && event.status) setStatus(event.status);
		},
		[narratorId],
	);
	useNarratorsListWS(watched, onStatus);

	const name = title?.trim() || t("botFallbackName");
	const waiting = status === "waiting";
	const statusLine = waiting ? t("botWaiting") : status === "working" ? t("botThinking") : null;

	return (
		// Full bleed like the messages page: Main's gutter is cancelled so the header
		// border and the composer reach the edges of the content area.
		<Box
			h={APP_SHELL_FULL_BLEED_HEIGHT}
			mx="calc(var(--mantine-spacing-md) * -1)"
			my="calc(var(--mantine-spacing-md) * -1)"
			style={{ display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden" }}
		>
			<Group
				gap="sm"
				px="md"
				h={CHAT_HEADER_HEIGHT}
				wrap="nowrap"
				style={{ flexShrink: 0, borderBottom: "1px solid var(--mantine-color-default-border)" }}
			>
				<NarratorAvatar
					narratorId={narratorId}
					avatarImageId={avatarImageId}
					title={name}
					size={28}
					showTooltip={false}
				/>
				<Box style={{ flex: 1, minWidth: 0 }}>
					<Group gap={6} wrap="nowrap">
						<Text size="sm" fw={600} lh={1.2} truncate>
							{name}
						</Text>
						<Badge size="xs" variant="light" color="grape" style={{ flexShrink: 0 }}>
							{t("botBadge")}
						</Badge>
					</Group>
					{statusLine ? (
						<Text size="xs" lh={1.2} c={waiting ? "orange" : "dimmed"} truncate>
							{statusLine}
						</Text>
					) : null}
				</Box>
				<Button
					size="compact-sm"
					// A pending approval can only be answered in the monitor, so point at it.
					variant={waiting ? "light" : "subtle"}
					color={waiting ? "orange" : "gray"}
					leftSection={<IconEye size={14} />}
					onClick={onMonitor}
				>
					{t("botMonitor")}
				</Button>
			</Group>
			<Box style={{ flex: 1, minHeight: 0 }}>
				{error ? (
					<Text size="sm" c="red" p="md">
						{error instanceof Error ? error.message : t("roomLoadFailed")}
					</Text>
				) : (
					// Keyed by room for the per-room state reasons given in NarratorUserChatPanel.
					<ChatRoomView
						key={room?.id ?? "pending"}
						roomId={room?.id}
						readOnlyReason={room?.canPost === false ? t("botReadOnly") : undefined}
					/>
				)}
			</Box>
		</Box>
	);
}
