import {
	Alert,
	Badge,
	Button,
	Group,
	List,
	Loader,
	NumberInput,
	Popover,
	SegmentedControl,
	Stack,
	Text,
	Tooltip,
	UnstyledButton,
} from "@mantine/core";
import type { ModelWindowInfo, ModelWindowSource } from "@shared/model-catalog/schema/api";
import { MAX_MODEL_CONTEXT_WINDOW, MIN_MODEL_CONTEXT_WINDOW } from "@shared/model-context-window";
import { useQueryClient } from "@tanstack/react-query";
import React, { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	modelCatalogKeys,
	useModelCatalog,
	useModelWindow,
	useModelWindows,
	useSetModelWindow,
} from "../../hooks/useModelCatalog";
import { ApiError } from "../../lib/api/client";
import { formatLocaleNumber } from "../../lib/intl-format";

/** Below this (but still saveable) the UI shows a non-blocking warning. */
export const CONTEXT_WINDOW_WARN_BELOW = 4_096;

type Scope = "connection" | "model";

const SOURCE_COLOR: Record<ModelWindowSource, string> = {
	own: "indigo",
	binding: "violet",
	model: "teal",
	discovered: "cyan",
	provider: "orange",
	preset: "gray",
	fallback: "gray",
};

export function formatWindow(value: number): string {
	if (value >= 1_000_000 && value % 1_000 === 0)
		return `${Number((value / 1_000_000).toFixed(3))}M`;
	if (value >= 1_000) return `${Number((value / 1_000).toFixed(1))}k`;
	return String(value);
}

export interface ContextWindowFieldProps {
	/** The full model value as this provider list shows it (`provider:model`). */
	modelValue: string;
	width?: number;
}

/**
 * A provider entry's context window: the value requests use, where it comes from, and an
 * editor that sets it for this provider only or for the model everywhere.
 */
export const ContextWindowField = React.memo(function ContextWindowField({
	modelValue,
	width = 180,
}: ContextWindowFieldProps) {
	const { t } = useTranslation("settings");
	const [opened, setOpened] = useState(false);
	const query = useModelWindow(modelValue);
	const info = query.data?.info;

	if (!info)
		return (
			<Group w={width} gap={4} wrap="nowrap">
				{query.isError ? (
					<Text size="xs" c="dimmed">
						{t("contextWindowUnavailable")}
					</Text>
				) : (
					<Loader size="xs" />
				)}
			</Group>
		);

	return (
		<Popover opened={opened} onChange={setOpened} width={340} position="bottom-end" withArrow>
			<Popover.Target>
				<UnstyledButton
					w={width}
					onClick={() => setOpened((value) => !value)}
					aria-label={t("contextWindow")}
				>
					<Group gap={6} wrap="nowrap" justify="flex-end">
						<Text size="xs" ff="monospace">
							{formatWindow(info.contextWindow)}
						</Text>
						<Badge size="xs" variant="light" color={SOURCE_COLOR[info.source]}>
							{t(`contextWindowSource.${info.source}`)}
						</Badge>
					</Group>
				</UnstyledButton>
			</Popover.Target>
			<Popover.Dropdown>
				{opened && (
					<WindowEditor
						info={info}
						revision={query.data?.revision ?? 0}
						onDone={() => setOpened(false)}
					/>
				)}
			</Popover.Dropdown>
		</Popover>
	);
});

function WindowEditor({
	info,
	revision,
	onDone,
}: {
	info: ModelWindowInfo;
	revision: number;
	onDone: () => void;
}) {
	const { t } = useTranslation("settings");
	const setWindow = useSetModelWindow();
	const queryClient = useQueryClient();
	const [scope, setScope] = useState<Scope>("connection");
	const initial = (next: Scope) =>
		(next === "connection" ? info.own : info.modelWide) ?? info.contextWindow;
	const [value, setValue] = useState<number | "">(initial("connection"));
	const [error, setError] = useState<string>();
	const canModelWide = info.modelId !== undefined;

	const run = async (target: Scope, contextWindow: number | null, key?: string) => {
		setError(undefined);
		try {
			await setWindow.mutateAsync({
				baseRevision: revision,
				scope: target,
				target: key ?? (target === "model" ? (info.modelId as string) : info.model),
				contextWindow,
			});
			onDone();
		} catch (failure) {
			// A stale revision (someone else edited the catalog) refreshes the row's data.
			if (failure instanceof ApiError && failure.status === 409) {
				setError(t("contextWindowConflict"));
				await queryClient.invalidateQueries({ queryKey: modelCatalogKeys.all });
				return;
			}
			setError(failure instanceof Error ? failure.message : t("contextWindowSaveFailed"));
		}
	};
	const valid =
		typeof value === "number" &&
		Number.isInteger(value) &&
		value >= MIN_MODEL_CONTEXT_WINDOW &&
		value <= MAX_MODEL_CONTEXT_WINDOW;
	const small = typeof value === "number" && value < CONTEXT_WINDOW_WARN_BELOW;

	return (
		<Stack gap="xs">
			<Text size="sm" fw={500}>
				{t("contextWindow")}
			</Text>
			<Text size="xs" c="dimmed">
				{t("contextWindowCurrent", {
					value: formatLocaleNumber(info.contextWindow),
					source: t(`contextWindowSource.${info.source}`),
				})}
			</Text>
			<Tooltip
				label={t("contextWindowModelWideUnavailable")}
				disabled={canModelWide}
				multiline
				w={280}
			>
				<SegmentedControl
					size="xs"
					fullWidth
					value={scope}
					onChange={(next) => {
						setScope(next as Scope);
						setValue(initial(next as Scope));
					}}
					data={[
						{ value: "connection", label: t("contextWindowScopeConnection") },
						{
							value: "model",
							label: t("contextWindowScopeModel"),
							disabled: !canModelWide,
						},
					]}
				/>
			</Tooltip>
			<Text size="xs" c="dimmed">
				{scope === "connection"
					? t("contextWindowScopeConnectionHint", { model: info.model })
					: t("contextWindowScopeModelHint", { model: info.modelId })}
			</Text>
			<NumberInput
				size="xs"
				value={value}
				onChange={(next) => setValue(typeof next === "number" ? next : "")}
				min={MIN_MODEL_CONTEXT_WINDOW}
				max={MAX_MODEL_CONTEXT_WINDOW}
				step={1000}
				thousandSeparator=","
				suffix={` ${t("contextWindowSuffix")}`}
				error={small ? t("contextWindowTooSmallWarning") : undefined}
			/>
			{scope === "model" && info.own !== undefined && (
				<Alert color="yellow" p="xs">
					<Text size="xs">
						{t("contextWindowOwnShadowsModel", { value: formatLocaleNumber(info.own) })}
					</Text>
				</Alert>
			)}
			{scope === "model" && info.others.length > 0 && (
				<Alert color="yellow" p="xs">
					<Text size="xs">{t("contextWindowOthersShadowModel")}</Text>
					<List size="xs" mt={4}>
						{info.others.map((other) => (
							<List.Item key={other.model}>
								<Group gap={4} wrap="nowrap">
									<Text size="xs" ff="monospace">
										{other.model} · {formatWindow(other.contextWindow)}
									</Text>
									<Button
										size="compact-xs"
										variant="subtle"
										color="red"
										loading={setWindow.isPending}
										onClick={() => run("connection", null, other.model)}
									>
										{t("contextWindowClear")}
									</Button>
								</Group>
							</List.Item>
						))}
					</List>
				</Alert>
			)}
			{error && (
				<Text size="xs" c="red">
					{error}
				</Text>
			)}
			<Group justify="space-between" gap="xs">
				<Group gap="xs">
					{scope === "connection" && info.own !== undefined && (
						<Button
							size="xs"
							variant="subtle"
							color="red"
							loading={setWindow.isPending}
							onClick={() => run("connection", null)}
						>
							{t("contextWindowClearOwn", { value: formatWindow(info.inherited) })}
						</Button>
					)}
					{scope === "model" && info.modelWide !== undefined && (
						<Button
							size="xs"
							variant="subtle"
							color="red"
							loading={setWindow.isPending}
							onClick={() => run("model", null)}
						>
							{t("contextWindowClearModel")}
						</Button>
					)}
				</Group>
				<Button
					size="xs"
					disabled={!valid}
					loading={setWindow.isPending}
					onClick={() => valid && run(scope, value)}
				>
					{t("contextWindowSave")}
				</Button>
			</Group>
		</Stack>
	);
}

/**
 * For a catalog model's editor: the provider entries that set their own window and so
 * keep it whatever the model-wide value is.
 */
export function ProviderWindowOverrides({ modelId }: { modelId: string }) {
	const { t } = useTranslation("settings");
	const snapshot = useModelCatalog();
	const keys = Object.entries(snapshot.data?.local.connections ?? {})
		.filter(([, metadata]) => typeof metadata.working_context_tokens === "number")
		.map(([model]) => model);
	const results = useModelWindows(keys);
	const own = results
		.map((result) => result.data?.info)
		.filter((info): info is ModelWindowInfo => info?.modelId === modelId && info.own !== undefined);
	if (!own.length) return null;
	return (
		<Alert color="yellow" p="xs">
			<Text size="xs">{t("contextWindowOthersShadowModel")}</Text>
			<List size="xs" mt={4}>
				{own.map((info) => (
					<List.Item key={info.model}>
						<Text size="xs" ff="monospace">
							{info.model} · {formatWindow(info.own as number)}
						</Text>
					</List.Item>
				))}
			</List>
		</Alert>
	);
}
