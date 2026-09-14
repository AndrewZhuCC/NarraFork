import { Button, Group, Input, Stack, Text } from "@mantine/core";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { readNarratorShortcuts, useNarratorShortcuts } from "../../hooks/useNarratorShortcuts";
import {
	findShortcutConflict,
	formatShortcut,
	isMacShortcutPlatform,
	normalizeShortcut,
	SHORTCUT_ACTIONS,
	type ShortcutAction,
} from "../../lib/narrator-shortcuts";

export function NarratorShortcutSettings() {
	const { t } = useTranslation("settings");
	const { bindings, setBinding, reset } = useNarratorShortcuts();
	const isMac = isMacShortcutPlatform();
	const id = useId();
	const [recording, setRecording] = useState<ShortcutAction | null>(null);
	const [error, setError] = useState<"invalid" | ShortcutAction>();
	const actionLabel = (action: ShortcutAction) =>
		action.startsWith("narrator")
			? t("narratorShortcuts.numbered", { number: action.slice("narrator".length) })
			: t(`narratorShortcuts.${action}`);

	return (
		<Stack gap="xs" data-narrator-shortcut-settings>
			<Text size="sm" fw={500}>
				{t("narratorShortcuts.title")}
			</Text>
			{SHORTCUT_ACTIONS.map((action) => (
				<Input.Wrapper
					key={action}
					id={`${id}-${action}`}
					label={actionLabel(action)}
					error={
						recording === action && error
							? error === "invalid"
								? t("narratorShortcuts.invalid")
								: t("narratorShortcuts.conflict", { action: actionLabel(error) })
							: undefined
					}
				>
					<Group gap="xs" wrap="nowrap">
						<Button
							id={`${id}-${action}`}
							variant={recording === action ? "light" : "default"}
							aria-pressed={recording === action}
							aria-describedby={recording === action && error ? `${id}-${action}-error` : undefined}
							onClick={(event) => {
								event.currentTarget.focus();
								setRecording(action);
								setError(undefined);
							}}
							onBlur={() => setRecording(null)}
							onKeyDown={(event) => {
								if (recording !== action) return;
								if (event.key === "Tab" && !event.ctrlKey && !event.metaKey && !event.altKey) {
									setRecording(null);
									return;
								}
								event.preventDefault();
								event.stopPropagation();
								if (event.nativeEvent.isComposing || event.repeat) return;
								if (event.key === "Escape") {
									setRecording(null);
									return;
								}
								if (
									["Control", "Meta", "Alt", "Shift", "AltGraph", "Dead", "Process"].includes(
										event.key,
									)
								)
									return;
								const value = normalizeShortcut(
									[
										event.ctrlKey && "Control",
										event.metaKey && "Meta",
										event.altKey && "Alt",
										event.shiftKey && "Shift",
										event.code || event.key,
									]
										.filter(Boolean)
										.join("+"),
								);
								const conflict =
									value === null
										? "invalid"
										: findShortcutConflict(readNarratorShortcuts(), action, value, isMac);
								setError(conflict);
								if (value === null || conflict) return;
								setBinding(action, value);
								setRecording(null);
							}}
						>
							{recording === action
								? t("narratorShortcuts.recording")
								: formatShortcut(bindings[action], isMac) || t("narratorShortcuts.disabled")}
						</Button>
						<Button
							variant="subtle"
							size="xs"
							disabled={!bindings[action]}
							aria-label={t("narratorShortcuts.clearAction", { action: actionLabel(action) })}
							onClick={() => {
								setBinding(action, "");
								setRecording(null);
							}}
						>
							{t("narratorShortcuts.clear")}
						</Button>
					</Group>
				</Input.Wrapper>
			))}
			<Button
				variant="light"
				style={{ alignSelf: "flex-start" }}
				onClick={() => {
					reset();
					setRecording(null);
				}}
			>
				{t("narratorShortcuts.reset")}
			</Button>
		</Stack>
	);
}
