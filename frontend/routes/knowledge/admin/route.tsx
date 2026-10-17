/**
 * Knowledge admin layout `/knowledge/admin` (docs/KNOWLEDGE_UI_REDESIGN.md §4.1, §5.8): header,
 * left sub-navigation (`lib/knowledge-admin-nav.ts`) and the section outlet. Admin-only — the
 * gate reads the current user's role and sends everyone else to `/knowledge`; the endpoints the
 * sections call enforce `requireAdmin` on the server independently.
 */
import {
	Anchor,
	Box,
	Breadcrumbs,
	Button,
	Container,
	Group,
	Loader,
	NavLink,
	ScrollArea,
	Stack,
	Text,
	Title,
} from "@mantine/core";
import { createFileRoute, Link, Navigate, Outlet, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { AnchorLink } from "../../../components/knowledge/router-links";
import { useCurrentUser } from "../../../hooks/useAuth";
import {
	activeKnowledgeAdminSection,
	KNOWLEDGE_ADMIN_SECTIONS,
	knowledgeAdminGate,
} from "../../../lib/knowledge-admin-nav";

export const Route = createFileRoute("/knowledge/admin")({
	component: KnowledgeAdminLayout,
});

function KnowledgeAdminLayout() {
	const { t } = useTranslation("knowledge");
	const { data: user, isLoading } = useCurrentUser();
	const pathname = useRouterState({ select: (s) => s.location.pathname });
	const gate = knowledgeAdminGate(user, isLoading);

	if (gate === "denied") return <Navigate to="/knowledge" replace />;
	if (gate === "loading") {
		return (
			<Group justify="center" py="xl">
				<Loader size="sm" />
			</Group>
		);
	}

	const active = activeKnowledgeAdminSection(pathname);

	return (
		<Container size="xl" py="lg">
			<Breadcrumbs mb="sm" separatorMargin={6}>
				<AnchorLink to="/knowledge" size="sm">
					{t("title")}
				</AnchorLink>
				{active ? (
					<AnchorLink to="/knowledge/admin" size="sm">
						{t("adminTitle")}
					</AnchorLink>
				) : (
					<Anchor component="span" size="sm" c="dimmed">
						{t("adminTitle")}
					</Anchor>
				)}
			</Breadcrumbs>
			<Stack gap={4} mb="md">
				<Title order={2}>{t("adminTitle")}</Title>
				<Text size="sm" c="dimmed">
					{t("accessControlDesc")}
				</Text>
			</Stack>

			{/* Narrow screens: the sections as a scrollable row of buttons above the content. */}
			<ScrollArea hiddenFrom="sm" mb="md" type="never">
				<Group gap="xs" wrap="nowrap" component="nav" aria-label={t("adminTitle")}>
					{KNOWLEDGE_ADMIN_SECTIONS.map((s) => (
						<Button
							key={s.to}
							component={Link}
							to={s.to}
							size="xs"
							variant={active?.to === s.to ? "light" : "subtle"}
						>
							{t(s.labelKey)}
						</Button>
					))}
				</Group>
			</ScrollArea>

			<Group align="flex-start" wrap="nowrap" gap="lg">
				<Box component="nav" aria-label={t("adminTitle")} w={200} miw={200} visibleFrom="sm">
					{KNOWLEDGE_ADMIN_SECTIONS.map((s) => (
						<NavLink
							key={s.to}
							component={Link}
							to={s.to}
							label={t(s.labelKey)}
							active={active?.to === s.to}
							variant="light"
						/>
					))}
				</Box>
				<Box style={{ flex: 1, minWidth: 0 }}>
					<Outlet />
				</Box>
			</Group>
		</Container>
	);
}
