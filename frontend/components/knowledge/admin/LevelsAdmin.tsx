/**
 * Knowledge admin › Levels (`/knowledge/admin/levels`): the classification hierarchy with
 * per-level clearance counts, inline edit, and create. Split out of the former AclAdminPanel
 * (docs/KNOWLEDGE_UI_REDESIGN.md §8.5) with behaviour unchanged.
 */
import {
	ActionIcon,
	Badge,
	Button,
	Group,
	Loader,
	NumberInput,
	Paper,
	Progress,
	Stack,
	Text,
	TextInput,
	Tooltip,
} from "@mantine/core";
import { IconPencil, IconPlus, IconTrash } from "@tabler/icons-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useCreateKnowledgeLevel,
	useDeleteKnowledgeLevel,
	useKnowledgeGrants,
	useKnowledgeLevels,
	useUpdateKnowledgeLevel,
} from "../../../hooks/useKnowledge";

export function LevelsAdmin() {
	const { t } = useTranslation("knowledge");
	const levels = useKnowledgeLevels();
	const createLevel = useCreateKnowledgeLevel();
	const deleteLevel = useDeleteKnowledgeLevel();
	const updateLevel = useUpdateKnowledgeLevel();
	const grants = useKnowledgeGrants();

	const [form, setForm] = useState({ name: "", rank: 0, label: "" });
	const [editing, setEditing] = useState<string | null>(null);
	const [editForm, setEditForm] = useState({ name: "", rank: 0, label: "" });

	// Count users at each clearance level
	const userCountByLevel = useMemo(() => {
		const m = new Map<string, number>();
		for (const g of grants.data ?? []) {
			if (g.grantType === "clearance" && g.clearanceLevel) {
				m.set(g.clearanceLevel, (m.get(g.clearanceLevel) ?? 0) + 1);
			}
		}
		return m;
	}, [grants.data]);

	const maxRank = Math.max(...(levels.data ?? []).map((l) => l.rank), 1);

	const startEdit = (l: { id: string; name: string; rank: number; label: string | null }) => {
		setEditing(l.id);
		setEditForm({ name: l.name, rank: l.rank, label: l.label ?? "" });
	};

	const saveEdit = (id: string) => {
		updateLevel.mutate(
			{
				id,
				name: editForm.name.trim() || undefined,
				rank: editForm.rank,
				label: editForm.label.trim() || null,
			},
			{ onSuccess: () => setEditing(null) },
		);
	};

	return (
		<Stack gap="md">
			<Text size="xs" c="dimmed">
				{t("levelsDesc")}
			</Text>

			{levels.isLoading ? (
				<Loader size="sm" />
			) : (levels.data?.length ?? 0) === 0 ? (
				<Text size="sm" c="dimmed">
					{t("noLevels")}
				</Text>
			) : (
				<Stack gap="xs">
					{levels.data?.map((l) =>
						editing === l.id ? (
							<Paper key={l.id} withBorder p="sm">
								<Group gap="xs" align="flex-end">
									<TextInput
										size="xs"
										label={t("levelName")}
										value={editForm.name}
										onChange={(e) => {
											const v = e.currentTarget.value;
											setEditForm((f) => ({ ...f, name: v }));
										}}
										style={{ flex: 1 }}
									/>
									<NumberInput
										size="xs"
										w={80}
										label={t("levelRank")}
										value={editForm.rank}
										onChange={(v) =>
											setEditForm((f) => ({
												...f,
												rank: typeof v === "number" ? v : 0,
											}))
										}
									/>
									<TextInput
										size="xs"
										label={t("levelLabel")}
										value={editForm.label}
										onChange={(e) => {
											const v = e.currentTarget.value;
											setEditForm((f) => ({ ...f, label: v }));
										}}
										style={{ flex: 1 }}
									/>
									<Button size="xs" onClick={() => saveEdit(l.id)} loading={updateLevel.isPending}>
										{t("save")}
									</Button>
									<Button size="xs" variant="default" onClick={() => setEditing(null)}>
										{t("cancel")}
									</Button>
								</Group>
							</Paper>
						) : (
							<Paper key={l.id} withBorder p="sm">
								<Group justify="space-between" wrap="nowrap">
									<Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
										<Badge size="lg" variant="filled" color={levelColor(l.rank, maxRank)} w={44}>
											{l.rank}
										</Badge>
										<div style={{ flex: 1, minWidth: 0 }}>
											<Group gap={6}>
												<Text size="sm" fw={600}>
													{l.name}
												</Text>
												{l.label ? (
													<Text size="xs" c="dimmed">
														({l.label})
													</Text>
												) : null}
											</Group>
											<Progress
												size="xs"
												value={(l.rank / maxRank) * 100}
												color={levelColor(l.rank, maxRank)}
												mt={4}
											/>
										</div>
									</Group>
									<Group gap="xs" wrap="nowrap">
										<Tooltip
											label={t("levelUserCount", {
												count: userCountByLevel.get(l.name) ?? 0,
											})}
										>
											<Badge size="sm" variant="light" color="gray">
												{userCountByLevel.get(l.name) ?? 0} {t("levelUsers")}
											</Badge>
										</Tooltip>
										<ActionIcon variant="subtle" size="sm" onClick={() => startEdit(l)}>
											<IconPencil size={14} />
										</ActionIcon>
										{l.name === "public" ? null : (
											<ActionIcon
												variant="subtle"
												color="red"
												size="sm"
												onClick={() => deleteLevel.mutate(l.id)}
											>
												<IconTrash size={14} />
											</ActionIcon>
										)}
									</Group>
								</Group>
							</Paper>
						),
					)}
				</Stack>
			)}

			<Paper withBorder p="sm" bg="var(--mantine-color-body)">
				<Group gap="xs" align="flex-end">
					<TextInput
						size="xs"
						label={t("levelName")}
						placeholder="e.g. confidential"
						value={form.name}
						onChange={(e) => {
							const v = e.currentTarget.value;
							setForm((f) => ({ ...f, name: v }));
						}}
						style={{ flex: 1 }}
					/>
					<NumberInput
						size="xs"
						w={80}
						label={t("levelRank")}
						value={form.rank}
						onChange={(v) => setForm((f) => ({ ...f, rank: typeof v === "number" ? v : 0 }))}
					/>
					<TextInput
						size="xs"
						label={t("levelLabel")}
						placeholder={t("levelLabelPlaceholder")}
						value={form.label}
						onChange={(e) => {
							const v = e.currentTarget.value;
							setForm((f) => ({ ...f, label: v }));
						}}
						style={{ flex: 1 }}
					/>
					<Button
						size="xs"
						leftSection={<IconPlus size={14} />}
						loading={createLevel.isPending}
						disabled={!form.name.trim()}
						onClick={() =>
							createLevel.mutate(
								{
									name: form.name.trim(),
									rank: form.rank,
									label: form.label.trim() || undefined,
								},
								{ onSuccess: () => setForm({ name: "", rank: 0, label: "" }) },
							)
						}
					>
						{t("createLevel")}
					</Button>
				</Group>
			</Paper>
		</Stack>
	);
}

function levelColor(rank: number, max: number): string {
	const ratio = rank / max;
	if (ratio <= 0.25) return "green";
	if (ratio <= 0.5) return "blue";
	if (ratio <= 0.75) return "orange";
	return "red";
}
