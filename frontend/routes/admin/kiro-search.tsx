import { Button, Code, Group, Loader, Paper, Stack, Text, TextInput, Title } from "@mantine/core";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { api } from "../../lib/api";

export const Route = createFileRoute("/admin/kiro-search")({
	component: KiroSearchPage,
});

function KiroSearchPage() {
	const { data: user } = useCurrentUser();
	const navigate = useNavigate();
	const { t } = useTranslation("common");
	const [query, setQuery] = useState("");
	const [result, setResult] = useState<Record<string, unknown> | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);

	if (user && user.role !== "admin") {
		navigate({ to: "/" });
		return null;
	}

	const handleSearch = async () => {
		if (!query.trim()) return;
		setLoading(true);
		setError(null);
		setResult(null);
		try {
			const res = await api.kiroMcpSearch(query.trim());
			setResult(res);
		} catch (err: unknown) {
			setError(err instanceof Error ? err.message : t("unknownError"));
		} finally {
			setLoading(false);
		}
	};

	return (
		<Stack>
			<Title order={2}>{t("kiroMcpSearch")}</Title>

			<Paper withBorder p="md">
				<Group align="end">
					<TextInput
						label={t("kiroSearchQuery")}
						placeholder={t("kiroSearchPlaceholder")}
						value={query}
						onChange={(e) => setQuery(e.currentTarget.value)}
						onKeyDown={(e) => e.key === "Enter" && handleSearch()}
						style={{ flex: 1 }}
					/>
					<Button onClick={handleSearch} loading={loading}>
						{t("kiroSearch")}
					</Button>
				</Group>
			</Paper>

			{error && (
				<Paper withBorder p="md">
					<Text c="red">{error}</Text>
				</Paper>
			)}

			{loading && <Loader />}

			{result && (
				<Paper withBorder p="md">
					<Code block style={{ maxHeight: 600, overflow: "auto" }}>
						{JSON.stringify(result, null, 2)}
					</Code>
				</Paper>
			)}
		</Stack>
	);
}
