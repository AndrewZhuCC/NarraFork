/**
 * Knowledge admin › Users (`/knowledge/admin/users`): per-user clearance / controlled-tag /
 * review-tag overview, bulk grant, and the per-user permission editor. Split out of the former
 * AclAdminPanel (docs/KNOWLEDGE_UI_REDESIGN.md §8.5) with behaviour unchanged.
 */
import { Badge, Button, Group, Loader, Stack, Table, Text } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { IconUsersGroup } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useKnowledgeGrants, useKnowledgeLevels } from "../../../hooks/useKnowledge";
import { api } from "../../../lib/api";
import { BulkGrantModal } from "./BulkGrantModal";
import { UserPermissionDetail } from "./UserPermissionDetail";

export function UsersAdmin() {
	const { t } = useTranslation("knowledge");
	const users = useQuery({
		queryKey: ["admin", "users"],
		queryFn: api.listUsers,
	});
	const levels = useKnowledgeLevels();
	const grants = useKnowledgeGrants();
	const [bulkModal, bulkModalH] = useDisclosure(false);

	const [selectedUser, setSelectedUser] = useState<{
		id: string;
		username: string;
	} | null>(null);

	// Aggregate clearance per user from grants
	const userClearance = useMemo(() => {
		const m = new Map<string, string>();
		const levelMap = new Map((levels.data ?? []).map((l) => [l.name, l.rank]));
		for (const g of grants.data ?? []) {
			if (g.grantType === "clearance" && g.clearanceLevel && g.principalType === "user") {
				const current = m.get(g.principalId);
				const currentRank = current ? (levelMap.get(current) ?? 0) : -1;
				const newRank = levelMap.get(g.clearanceLevel) ?? 0;
				if (newRank > currentRank) m.set(g.principalId, g.clearanceLevel);
			}
		}
		return m;
	}, [grants.data, levels.data]);

	// Count controlled tags per user
	const userTagCount = useMemo(() => {
		const m = new Map<string, number>();
		for (const g of grants.data ?? []) {
			if (g.grantType === "tag" && g.principalType === "user") {
				m.set(g.principalId, (m.get(g.principalId) ?? 0) + 1);
			}
		}
		return m;
	}, [grants.data]);

	// Count review tags per user
	const userReviewCount = useMemo(() => {
		const m = new Map<string, number>();
		for (const g of grants.data ?? []) {
			if (g.grantType === "review" && g.principalType === "user") {
				m.set(g.principalId, (m.get(g.principalId) ?? 0) + 1);
			}
		}
		return m;
	}, [grants.data]);

	if (users.isLoading) return <Loader size="sm" />;

	return (
		<Stack gap="md">
			<Group justify="space-between" align="center">
				<Text size="xs" c="dimmed">
					{t("usersTabDesc")}
				</Text>
				<Button
					size="xs"
					variant="light"
					leftSection={<IconUsersGroup size={14} />}
					onClick={bulkModalH.open}
				>
					{t("bulkGrantOpen")}
				</Button>
			</Group>

			<Table highlightOnHover>
				<Table.Thead>
					<Table.Tr>
						<Table.Th>{t("username")}</Table.Th>
						<Table.Th>{t("userRole")}</Table.Th>
						<Table.Th>{t("classificationLevel")}</Table.Th>
						<Table.Th>{t("controlledTags")}</Table.Th>
						<Table.Th>{t("reviewTags")}</Table.Th>
					</Table.Tr>
				</Table.Thead>
				<Table.Tbody>
					{(users.data ?? []).map((u: { id: string; username: string; role: string }) => (
						<Table.Tr
							key={u.id}
							onClick={() => setSelectedUser(u)}
							style={{ cursor: "pointer" }}
							bg={selectedUser?.id === u.id ? "var(--mantine-primary-color-light)" : undefined}
						>
							<Table.Td>
								<Text size="sm" fw={500}>
									{u.username}
								</Text>
							</Table.Td>
							<Table.Td>
								{u.role === "admin" ? (
									<Badge size="sm" color="red" variant="light">
										admin
									</Badge>
								) : (
									<Badge size="sm" color="gray" variant="light">
										user
									</Badge>
								)}
							</Table.Td>
							<Table.Td>
								{u.role === "admin" ? (
									<Text size="xs" c="dimmed" fs="italic">
										{t("adminBypass")}
									</Text>
								) : (
									<Badge
										size="sm"
										variant="light"
										color={userClearance.get(u.id) ? "grape" : "gray"}
									>
										{userClearance.get(u.id) ?? "public"}
									</Badge>
								)}
							</Table.Td>
							<Table.Td>
								<Badge size="sm" variant="light" color="teal">
									{u.role === "admin" ? "∞" : (userTagCount.get(u.id) ?? 0)}
								</Badge>
							</Table.Td>
							<Table.Td>
								<Badge size="sm" variant="light" color="blue">
									{u.role === "admin" ? "∞" : (userReviewCount.get(u.id) ?? 0)}
								</Badge>
							</Table.Td>
						</Table.Tr>
					))}
				</Table.Tbody>
			</Table>

			{selectedUser ? (
				<UserPermissionDetail
					userId={selectedUser.id}
					username={selectedUser.username}
					onClose={() => setSelectedUser(null)}
				/>
			) : (
				<Text size="sm" c="dimmed" ta="center" py="md">
					{t("selectUserHint")}
				</Text>
			)}

			<BulkGrantModal
				opened={bulkModal}
				onClose={bulkModalH.close}
				users={(users.data ?? []) as { id: string; username: string; role: string }[]}
			/>
		</Stack>
	);
}
