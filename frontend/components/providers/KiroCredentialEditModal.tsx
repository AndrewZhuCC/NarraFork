import {
	Button,
	Collapse,
	Group,
	Modal,
	NumberInput,
	Select,
	Stack,
	Text,
	Textarea,
	TextInput,
} from "@mantine/core";
import { IconChevronDown, IconChevronRight } from "@tabler/icons-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { KiroCredentialPatch } from "../../lib/api";
import { isKiroApiKeyAuthMethod } from "./types";

/**
 * Credential snapshot shape this modal reads. Mirrors the server
 * `CredentialSnapshot`: secrets are represented by `has*` flags only.
 */
export interface KiroCredentialSnapshotView {
	id: string;
	email?: string;
	displayName?: string;
	priority?: number;
	region?: string;
	authRegion?: string;
	apiRegion?: string;
	profileArn?: string;
	subscriptionTitle?: string;
	startUrl?: string;
	machineId?: string;
	authMethod?: string;
	isApiKey?: boolean;
	hasClientId?: boolean;
	hasClientSecret?: boolean;
	proxyUrl?: string;
	proxyUsername?: string;
	hasProxyPassword?: boolean;
}

/** AWS region shape accepted by the server-side validator. */
const REGION_PATTERN = /^[a-z]{2}(-[a-z]+)+-\d+$/;

type AuthMethodValue = "social" | "idc" | "api_key";

function normalizeAuthMethod(method?: string): AuthMethodValue {
	if (isKiroApiKeyAuthMethod(method)) return "api_key";
	const lower = method?.trim().toLowerCase();
	if (lower === "idc" || lower === "iam" || lower === "builder-id") return "idc";
	return "social";
}

/**
 * Collect the fields that actually changed.
 *
 * Text fields are always sent when they differ from the snapshot (including
 * cleared to `""`, which the server treats as "remove"). Secrets are sent only
 * when non-empty, since the modal never receives their current value.
 */
function buildPatch(
	entry: KiroCredentialSnapshotView,
	form: {
		email: string;
		displayName: string;
		priority: number;
		region: string;
		authRegion: string;
		apiRegion: string;
		profileArn: string;
		subscriptionTitle: string;
		startUrl: string;
		machineId: string;
		authMethod: AuthMethodValue;
		proxyUrl: string;
		proxyUsername: string;
		refreshToken: string;
		apiKey: string;
		clientId: string;
		clientSecret: string;
		proxyPassword: string;
	},
): KiroCredentialPatch {
	const patch: KiroCredentialPatch = {};
	const addText = (
		key: "email" | "displayName" | "region" | "authRegion" | "apiRegion" | "profileArn",
		value: string,
	) => {
		const next = value.trim();
		if (next !== (entry[key] ?? "")) patch[key] = next;
	};
	addText("email", form.email);
	addText("displayName", form.displayName);
	addText("region", form.region);
	addText("authRegion", form.authRegion);
	addText("apiRegion", form.apiRegion);
	addText("profileArn", form.profileArn);
	if (form.subscriptionTitle.trim() !== (entry.subscriptionTitle ?? "")) {
		patch.subscriptionTitle = form.subscriptionTitle.trim();
	}
	if (form.startUrl.trim() !== (entry.startUrl ?? "")) patch.startUrl = form.startUrl.trim();
	if (form.machineId.trim() !== (entry.machineId ?? "")) patch.machineId = form.machineId.trim();
	if (form.proxyUrl.trim() !== (entry.proxyUrl ?? "")) patch.proxyUrl = form.proxyUrl.trim();
	if (form.proxyUsername.trim() !== (entry.proxyUsername ?? "")) {
		patch.proxyUsername = form.proxyUsername.trim();
	}
	if (form.authMethod !== normalizeAuthMethod(entry.authMethod)) {
		patch.authMethod = form.authMethod;
	}
	if (form.priority !== (entry.priority ?? 0)) patch.priority = form.priority;

	// Secrets: empty means "leave unchanged".
	if (form.refreshToken.trim()) patch.refreshToken = form.refreshToken.trim();
	if (form.apiKey.trim()) patch.apiKey = form.apiKey.trim();
	if (form.clientId.trim()) patch.clientId = form.clientId.trim();
	if (form.clientSecret.trim()) patch.clientSecret = form.clientSecret.trim();
	if (form.proxyPassword.trim()) patch.proxyPassword = form.proxyPassword.trim();
	return patch;
}

interface KiroCredentialEditModalProps {
	entry: KiroCredentialSnapshotView | null;
	opened: boolean;
	onClose: () => void;
	onSubmit: (patch: KiroCredentialPatch) => void;
	loading?: boolean;
	error?: string | null;
}

/**
 * Full Kiro credential editor.
 *
 * Region fields are the reason this exists: `region` drives token refresh,
 * `authRegion` overrides it, and `apiRegion` overrides the data-plane endpoint
 * independently. All three are per-credential.
 */
export function KiroCredentialEditModal({
	entry,
	opened,
	onClose,
	onSubmit,
	loading,
	error,
}: KiroCredentialEditModalProps) {
	const { t } = useTranslation("settings");
	const [email, setEmail] = useState("");
	const [displayName, setDisplayName] = useState("");
	const [priority, setPriority] = useState(0);
	const [region, setRegion] = useState("");
	const [authRegion, setAuthRegion] = useState("");
	const [apiRegion, setApiRegion] = useState("");
	const [profileArn, setProfileArn] = useState("");
	const [subscriptionTitle, setSubscriptionTitle] = useState("");
	const [startUrl, setStartUrl] = useState("");
	const [machineId, setMachineId] = useState("");
	const [authMethod, setAuthMethod] = useState<AuthMethodValue>("social");
	const [proxyUrl, setProxyUrl] = useState("");
	const [proxyUsername, setProxyUsername] = useState("");
	const [refreshToken, setRefreshToken] = useState("");
	const [apiKey, setApiKey] = useState("");
	const [clientId, setClientId] = useState("");
	const [clientSecret, setClientSecret] = useState("");
	const [proxyPassword, setProxyPassword] = useState("");
	const [showOptional, setShowOptional] = useState(false);

	useEffect(() => {
		if (!opened) return;
		setEmail(entry?.email ?? "");
		setDisplayName(entry?.displayName ?? "");
		setPriority(entry?.priority ?? 0);
		setRegion(entry?.region ?? "");
		setAuthRegion(entry?.authRegion ?? "");
		setApiRegion(entry?.apiRegion ?? "");
		setProfileArn(entry?.profileArn ?? "");
		setSubscriptionTitle(entry?.subscriptionTitle ?? "");
		setStartUrl(entry?.startUrl ?? "");
		setMachineId(entry?.machineId ?? "");
		setAuthMethod(normalizeAuthMethod(entry?.authMethod));
		setProxyUrl(entry?.proxyUrl ?? "");
		setProxyUsername(entry?.proxyUsername ?? "");
		setRefreshToken("");
		setApiKey("");
		setClientId("");
		setClientSecret("");
		setProxyPassword("");
		setShowOptional(false);
	}, [opened, entry]);

	const wasApiKey = entry?.isApiKey === true || isKiroApiKeyAuthMethod(entry?.authMethod);
	const isApiKey = authMethod === "api_key";
	// Switching auth method must bring the matching secret along, otherwise the
	// credential would be left without usable token material.
	const needsApiKey = isApiKey && !wasApiKey && !apiKey.trim();
	const needsRefreshToken = !isApiKey && wasApiKey && refreshToken.trim().length === 0;
	const refreshTokenTooShort =
		!isApiKey && refreshToken.trim().length > 0 && refreshToken.trim().length < 20;

	const regionError = (value: string) =>
		value.trim() && !REGION_PATTERN.test(value.trim()) ? t("kiroEditRegionInvalid") : undefined;
	const hasRegionError = Boolean(
		regionError(region) || regionError(authRegion) || regionError(apiRegion),
	);

	const submitDisabled =
		!entry || needsApiKey || needsRefreshToken || refreshTokenTooShort || hasRegionError;

	const handleSubmit = () => {
		if (!entry || submitDisabled) return;
		onSubmit(
			buildPatch(entry, {
				email,
				displayName,
				priority,
				region,
				authRegion,
				apiRegion,
				profileArn,
				subscriptionTitle,
				startUrl,
				machineId,
				authMethod,
				proxyUrl,
				proxyUsername,
				refreshToken,
				apiKey,
				clientId,
				clientSecret,
				proxyPassword,
			}),
		);
	};

	return (
		<Modal
			opened={opened}
			onClose={onClose}
			title={t("kiroEditTitle", { id: entry?.id ?? "" })}
			size="lg"
		>
			<Stack gap="sm">
				<Group grow>
					<TextInput
						label={t("kiroEditDisplayName")}
						value={displayName}
						onChange={(e) => setDisplayName(e.currentTarget.value)}
					/>
					<TextInput
						label="Email"
						value={email}
						onChange={(e) => setEmail(e.currentTarget.value)}
					/>
				</Group>

				<Group grow>
					<Select
						label={t("kiroEditAuthMethod")}
						data={[
							{ value: "social", label: "social" },
							{ value: "idc", label: "idc" },
							{ value: "api_key", label: "api_key (KIRO_API_KEY)" },
						]}
						value={authMethod}
						onChange={(value) => setAuthMethod((value as AuthMethodValue) ?? "social")}
						allowDeselect={false}
					/>
					<NumberInput
						label={t("kiroColPriority")}
						description={t("kiroEditPriorityDesc")}
						value={priority}
						onChange={(v) => setPriority(Number(v) || 0)}
						min={0}
						step={1}
					/>
				</Group>

				<Textarea
					label={isApiKey ? t("kiroEditApiKey") : t("kiroEditRefreshToken")}
					description={isApiKey ? t("kiroEditApiKeyDesc") : t("kiroEditSecretKeepHint")}
					value={isApiKey ? apiKey : refreshToken}
					onChange={(e) =>
						isApiKey ? setApiKey(e.currentTarget.value) : setRefreshToken(e.currentTarget.value)
					}
					minRows={2}
					autosize
					error={
						needsApiKey
							? t("kiroEditApiKeyRequired")
							: needsRefreshToken
								? t("kiroEditRefreshTokenRequired")
								: refreshTokenTooShort
									? t("kiroEditRefreshTokenTooShort")
									: undefined
					}
					styles={{ input: { fontFamily: "monospace", fontSize: 12 } }}
				/>

				<Text size="xs" c="dimmed">
					{t("kiroEditRegionsDesc")}
				</Text>
				<Group grow>
					<TextInput
						label={t("kiroEditRegion")}
						description={t("kiroEditRegionDesc")}
						placeholder="us-east-1"
						value={region}
						error={regionError(region)}
						onChange={(e) => setRegion(e.currentTarget.value)}
					/>
					<TextInput
						label={t("kiroEditAuthRegion")}
						description={t("kiroEditAuthRegionDesc")}
						placeholder={region.trim() || "us-east-1"}
						value={authRegion}
						error={regionError(authRegion)}
						onChange={(e) => setAuthRegion(e.currentTarget.value)}
					/>
					<TextInput
						label={t("kiroEditApiRegion")}
						description={t("kiroEditApiRegionDesc")}
						placeholder="us-east-1"
						value={apiRegion}
						error={regionError(apiRegion)}
						onChange={(e) => setApiRegion(e.currentTarget.value)}
					/>
				</Group>

				<Button
					variant="subtle"
					size="compact-sm"
					onClick={() => setShowOptional((value) => !value)}
					leftSection={
						showOptional ? <IconChevronDown size={14} /> : <IconChevronRight size={14} />
					}
					style={{ alignSelf: "flex-start" }}
				>
					{t("kiroEditMoreFields")}
				</Button>
				<Collapse expanded={showOptional}>
					<Stack gap="sm">
						<Group grow>
							<TextInput
								label={t("kiroEditSubscriptionTitle")}
								value={subscriptionTitle}
								onChange={(e) => setSubscriptionTitle(e.currentTarget.value)}
							/>
							<TextInput
								label="Profile ARN"
								value={profileArn}
								onChange={(e) => setProfileArn(e.currentTarget.value)}
							/>
						</Group>
						<TextInput
							label="Start URL"
							description={t("kiroEditStartUrlDesc")}
							value={startUrl}
							onChange={(e) => setStartUrl(e.currentTarget.value)}
						/>
						<Group grow>
							<TextInput
								label="Client ID"
								placeholder={entry?.hasClientId ? t("kiroEditSecretStored") : undefined}
								value={clientId}
								onChange={(e) => setClientId(e.currentTarget.value)}
							/>
							<TextInput
								label="Client Secret"
								placeholder={entry?.hasClientSecret ? t("kiroEditSecretStored") : undefined}
								value={clientSecret}
								onChange={(e) => setClientSecret(e.currentTarget.value)}
							/>
						</Group>
						<TextInput
							label="Machine ID"
							description={t("kiroEditMachineIdDesc")}
							value={machineId}
							onChange={(e) => setMachineId(e.currentTarget.value)}
						/>
						<Group grow>
							<TextInput
								label="Proxy URL"
								value={proxyUrl}
								onChange={(e) => setProxyUrl(e.currentTarget.value)}
							/>
							<TextInput
								label="Proxy Username"
								value={proxyUsername}
								onChange={(e) => setProxyUsername(e.currentTarget.value)}
							/>
							<TextInput
								label="Proxy Password"
								placeholder={entry?.hasProxyPassword ? t("kiroEditSecretStored") : undefined}
								value={proxyPassword}
								onChange={(e) => setProxyPassword(e.currentTarget.value)}
							/>
						</Group>
					</Stack>
				</Collapse>

				<Text size="xs" c="dimmed">
					{t("kiroEditSecretKeepHint")}
				</Text>
				{error && (
					<Text size="sm" c="red">
						{error}
					</Text>
				)}
				<Group justify="flex-end" mt="sm">
					<Button variant="default" onClick={onClose}>
						{t("kiroEditCancel")}
					</Button>
					<Button onClick={handleSubmit} loading={loading} disabled={submitDisabled}>
						{t("kiroEditSave")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}
