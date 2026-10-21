import { ActionIcon, Group, Text, Tooltip } from "@mantine/core";
import { IconEye, IconEyeOff, IconPlayerPlay } from "@tabler/icons-react";
import React from "react";
import { useTranslation } from "react-i18next";
import { ContextWindowField } from "./ContextWindowField";

export interface ModelRowProps {
	modelValue: string;
	modelLabel: string;
	isHidden: boolean;
	onToggleHidden: () => void;
	onTestModel?: () => void;
	/** Set to false to hide the context window input (e.g. Codex). */
	showContextWindow?: boolean;
	/** Provider-specific controls rendered at the end of the row (e.g. protocol picker). */
	extra?: React.ReactNode;
}

export const ModelRow = React.memo(function ModelRow({
	modelValue,
	modelLabel,
	isHidden,
	onToggleHidden,
	onTestModel,
	showContextWindow = true,
	extra,
}: ModelRowProps) {
	const { t } = useTranslation("settings");

	return (
		<Group gap="xs" wrap="wrap" style={isHidden ? { opacity: 0.5 } : undefined}>
			<Text size="xs" ff="monospace" truncate style={{ flex: 1, minWidth: 120 }}>
				{modelValue}
			</Text>
			<Text size="xs" c="dimmed" truncate style={{ flex: 1, minWidth: 120 }}>
				{modelLabel}
			</Text>
			{extra}
			{showContextWindow && <ContextWindowField modelValue={modelValue} />}
			{onTestModel && (
				<Tooltip label={t("modelTestBtn")}>
					<ActionIcon variant="subtle" color="teal" onClick={onTestModel}>
						<IconPlayerPlay size={16} />
					</ActionIcon>
				</Tooltip>
			)}
			<ActionIcon variant="subtle" color={isHidden ? "gray" : "blue"} onClick={onToggleHidden}>
				{isHidden ? <IconEyeOff size={16} /> : <IconEye size={16} />}
			</ActionIcon>
		</Group>
	);
});
