import { ActionIcon, Group, Stack, Text, TextInput, Tooltip } from "@mantine/core";
import { IconEye, IconEyeOff, IconSearch } from "@tabler/icons-react";
import React, { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ModelProtocolSelect } from "./ModelProtocolSelect";
import { ModelRow } from "./ModelRow";
import type { CustomApiProtocol } from "./types";

export interface ModelListItem {
	value: string;
	label: string;
}

const MAX_VISIBLE_MODEL_ROWS = 200;

export interface ModelListProps {
	models: ModelListItem[];
	hiddenModels: Set<string>;
	onToggleHidden: (modelVal: string) => void;
	onBatchToggleHidden?: (modelValues: string[], hidden: boolean) => void;
	/** 逐模型协议覆盖（`agent.modelProtocols` 的草稿），由页面统一提交。 */
	modelProtocols: Record<string, CustomApiProtocol>;
	onModelProtocolChange: (modelVal: string, protocol: CustomApiProtocol | null) => void;
	onTestModel?: (model: string) => void;
	showContextWindow?: boolean;
	/** 这一列模型可选的协议。省略时是全部四种。 */
	protocols?: readonly CustomApiProtocol[];
	/** Provider-specific control appended to each row (e.g. a protocol picker). */
	renderExtraForModel?: (modelValue: string) => React.ReactNode;
}

export const ModelList = React.memo(function ModelList({
	models,
	hiddenModels,
	onToggleHidden,
	onBatchToggleHidden,
	modelProtocols,
	onModelProtocolChange,
	onTestModel,
	showContextWindow = true,
	protocols,
	renderExtraForModel,
}: ModelListProps) {
	const { t } = useTranslation("settings");
	const [query, setQuery] = useState("");

	const renderProtocolPicker = (modelValue: string) => (
		<ModelProtocolSelect
			value={modelProtocols[modelValue] ?? null}
			onChange={(protocol) => onModelProtocolChange(modelValue, protocol)}
			width={190}
			protocols={protocols}
		/>
	);
	const normalizedQuery = query.trim().toLowerCase();
	const allHidden = useMemo(
		() => models.every((m) => hiddenModels.has(m.value)),
		[models, hiddenModels],
	);
	const sortedModels = useMemo(() => {
		const filtered = normalizedQuery
			? models.filter((model) => {
					const value = model.value.toLowerCase();
					const label = model.label.toLowerCase();
					return value.includes(normalizedQuery) || label.includes(normalizedQuery);
				})
			: models;
		return [...filtered].sort((a, b) => {
			const aHidden = hiddenModels.has(a.value);
			const bHidden = hiddenModels.has(b.value);
			return Number(aHidden) - Number(bHidden);
		});
	}, [models, hiddenModels, normalizedQuery]);
	const visibleModels = sortedModels.slice(0, MAX_VISIBLE_MODEL_ROWS);
	const hiddenRowCount = Math.max(0, sortedModels.length - visibleModels.length);

	if (models.length === 0) return null;

	return (
		<Stack gap="xs" mt="xs">
			{models.length > MAX_VISIBLE_MODEL_ROWS && (
				<TextInput
					size="xs"
					leftSection={<IconSearch size={14} />}
					placeholder={t("modelListSearchPlaceholder")}
					value={query}
					onChange={(event) => setQuery(event.currentTarget.value)}
				/>
			)}
			{onBatchToggleHidden && (
				<Group gap="xs" justify="flex-end">
					<Tooltip label={allHidden ? t("showAllModels") : t("hideAllModels")}>
						<ActionIcon
							variant="subtle"
							color={allHidden ? "gray" : "blue"}
							onClick={() =>
								onBatchToggleHidden(
									models.map((m) => m.value),
									!allHidden,
								)
							}
						>
							{allHidden ? <IconEyeOff size={16} /> : <IconEye size={16} />}
						</ActionIcon>
					</Tooltip>
				</Group>
			)}
			{visibleModels.map((m) => (
				<ModelRow
					key={m.value}
					modelValue={m.value}
					modelLabel={m.label}
					isHidden={hiddenModels.has(m.value)}
					onToggleHidden={() => onToggleHidden(m.value)}
					onTestModel={onTestModel ? () => onTestModel(m.value) : undefined}
					showContextWindow={showContextWindow}
					extra={
						<>
							{renderProtocolPicker(m.value)}
							{renderExtraForModel?.(m.value)}
						</>
					}
				/>
			))}
			{hiddenRowCount > 0 && (
				<Text size="xs" c="dimmed" ta="center">
					{t("modelListItemsTruncated", {
						shown: visibleModels.length,
						hidden: hiddenRowCount,
					})}
				</Text>
			)}
		</Stack>
	);
});
