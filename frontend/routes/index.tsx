import { Stack, Title } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { DashboardComposer } from "../components/dashboard/DashboardComposer";
import { DashboardSessions, useDashboardSessions } from "../components/dashboard/DashboardSessions";

export const Route = createFileRoute("/")({
	component: DashboardPage,
});

function DashboardPage() {
	const { t } = useTranslation("dashboard");
	const sessions = useDashboardSessions();

	return (
		<Stack maw={760} w="100%" mx="auto" gap={40} pt={{ base: "md", sm: 56 }} pb="xl">
			<Stack gap="md">
				<Title order={2} ta="center" fw={600}>
					{t("heading")}
				</Title>
				{/* 输入即开始：选目录/模型，回车创建独立会话并发送首条消息 */}
				<DashboardComposer recentDirectories={sessions.recentDirectories} />
			</Stack>

			{/* 需要用户处理的会话置顶，其次是正在运行的会话 */}
			<DashboardSessions data={sessions} />
		</Stack>
	);
}
