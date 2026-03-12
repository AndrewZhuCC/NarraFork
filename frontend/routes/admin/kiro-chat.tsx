import { Button, Group, Paper, Select, Stack, Text, Textarea, Title } from "@mantine/core";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useCallback, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { useAllModels } from "../../hooks/useModels";
import { kiroStreamChat } from "../../lib/api";

export const Route = createFileRoute("/admin/kiro-chat")({
	component: KiroChatPage,
});

function KiroChatPage() {
	const { data: user } = useCurrentUser();
	const navigate = useNavigate();
	const { t } = useTranslation("common");
	const [input, setInput] = useState("");
	const [model, setModel] = useState<string>("claude-sonnet-4.5");
	const [output, setOutput] = useState("");
	const [streaming, setStreaming] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const abortRef = useRef<AbortController | null>(null);

	const { groupedModels } = useAllModels();

	const handleSend = useCallback(async () => {
		if (!input.trim() || streaming) return;
		setStreaming(true);
		setError(null);
		setOutput("");
		const ac = new AbortController();
		abortRef.current = ac;
		try {
			for await (const chunk of kiroStreamChat(input.trim(), model, ac.signal)) {
				setOutput((prev) => prev + chunk);
			}
		} catch (err: unknown) {
			const e = err as Error;
			if (e.name !== "AbortError") {
				setError(e.message ?? t("unknownError"));
			}
		} finally {
			setStreaming(false);
			abortRef.current = null;
		}
	}, [input, model, streaming, t]);

	const handleStop = () => {
		abortRef.current?.abort();
	};

	if (user && user.role !== "admin") {
		navigate({ to: "/" });
		return null;
	}

	return (
		<Stack>
			<Title order={2}>{t("kiroChatDebug")}</Title>

			<Paper withBorder p="md">
				<Stack>
					<Textarea
						label={t("kiroMessage")}
						placeholder={t("kiroChatPlaceholder")}
						value={input}
						onChange={(e) => setInput(e.currentTarget.value)}
						minRows={3}
						autosize
					/>
					<Group>
						<Select
							data={groupedModels}
							value={model}
							onChange={(v) => v && setModel(v)}
							label={t("kiroModel")}
							style={{ width: 220 }}
						/>
						{streaming ? (
							<Button color="red" onClick={handleStop} mt="auto">
								{t("kiroStop")}
							</Button>
						) : (
							<Button onClick={handleSend} disabled={!input.trim()} mt="auto">
								{t("send")}
							</Button>
						)}
					</Group>
				</Stack>
			</Paper>

			{error && (
				<Paper withBorder p="md">
					<Text c="red">{error}</Text>
				</Paper>
			)}

			{output && (
				<Paper withBorder p="md">
					<Text style={{ whiteSpace: "pre-wrap", fontFamily: "monospace" }}>{output}</Text>
				</Paper>
			)}
		</Stack>
	);
}
