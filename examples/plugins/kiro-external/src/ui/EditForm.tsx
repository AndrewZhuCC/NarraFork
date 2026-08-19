/** @jsxImportSource ./shim */

/**
 * Credential edit form, mirroring the host's `KiroCredentialEditModal`.
 *
 * Two field classes with opposite empty-value semantics, which is the whole reason this form
 * needs care:
 *
 * - **Text fields** are sent whenever they differ from the snapshot, *including* when cleared.
 *   An empty string means "remove the stored value".
 * - **Secrets** are never shown (the snapshot only carries `has*` flags) and are sent only
 *   when non-empty. Empty means "keep what is stored".
 *
 * Sending an unchanged field would be harmless but noisy; sending an empty secret would wipe a
 * working credential, so the two must not share a code path.
 *
 * Auth method uses `SegmentedControl` rather than `Select`: a `Select` renders a dropdown that
 * the panel's fixed-height frame would clip.
 */

import { MantineCore, useState } from "./host-runtime";
import { PanelOverlay } from "./Overlay";
import type { CredentialEntry, CredentialPatch } from "./panel-types";
import { t } from "./strings";

const {
	Alert,
	Button,
	Group,
	NumberInput,
	SegmentedControl,
	Stack,
	Text,
	TextInput,
	PasswordInput,
} = MantineCore;

type AuthMethod = "social" | "idc" | "api_key";

function normalizeAuthMethod(method?: string): AuthMethod {
	const lower = method?.trim().toLowerCase();
	if (lower === "api_key" || lower === "apikey") return "api_key";
	if (lower === "idc" || lower === "iam" || lower === "builder-id") return "idc";
	return "social";
}

/** Text fields, in the order the host's modal shows them. */
const TEXT_FIELDS: Array<{ key: keyof CredentialEntry & string; label: string }> = [
	{ key: "displayName", label: t("editDisplayName") },
	{ key: "email", label: t("editEmail") },
	{ key: "region", label: t("editRegion") },
	{ key: "authRegion", label: t("editAuthRegion") },
	{ key: "apiRegion", label: t("editApiRegion") },
	{ key: "profileArn", label: t("editProfileArn") },
	{ key: "subscriptionTitle", label: t("editSubscriptionTitle") },
	{ key: "startUrl", label: t("editStartUrl") },
	{ key: "machineId", label: t("editMachineId") },
	{ key: "proxyUrl", label: t("editProxyUrl") },
	{ key: "proxyUsername", label: t("editProxyUsername") },
];

export interface EditFormProps {
	entry: CredentialEntry;
	loading?: boolean;
	error?: string;
	onSubmit: (patch: CredentialPatch) => void;
	onCancel: () => void;
}

export function EditForm({ entry, loading, error, onSubmit, onCancel }: EditFormProps) {
	const [text, setText] = useState<Record<string, string>>(() => {
		const initial: Record<string, string> = {};
		for (const { key } of TEXT_FIELDS) {
			const value = entry[key];
			initial[key] = typeof value === "string" ? value : "";
		}
		return initial;
	});
	const [secrets, setSecrets] = useState<Record<string, string>>({
		refreshToken: "",
		apiKey: "",
		clientId: "",
		clientSecret: "",
		proxyPassword: "",
	});
	const [authMethod, setAuthMethod] = useState<AuthMethod>(normalizeAuthMethod(entry.authMethod));
	const [priority, setPriority] = useState<number>(entry.priority ?? 0);
	const [localError, setLocalError] = useState<string | undefined>();

	const isApiKey = authMethod === "api_key";

	/** Secret fields, with a marker for the ones that already hold a value. */
	const secretFields: Array<{ key: string; label: string; stored: boolean }> = [
		{ key: "refreshToken", label: t("editRefreshToken"), stored: !entry.isApiKey },
		{ key: "apiKey", label: t("editApiKey"), stored: Boolean(entry.isApiKey) },
		{ key: "clientId", label: t("editClientId"), stored: Boolean(entry.hasClientId) },
		{ key: "clientSecret", label: t("editClientSecret"), stored: Boolean(entry.hasClientSecret) },
		{
			key: "proxyPassword",
			label: t("editProxyPassword"),
			stored: Boolean(entry.hasProxyPassword),
		},
	];

	const submit = () => {
		const patch: CredentialPatch = {};
		for (const { key } of TEXT_FIELDS) {
			const next = text[key].trim();
			const current = typeof entry[key] === "string" ? (entry[key] as string) : "";
			// Cleared to empty is a real change: it removes the stored value.
			if (next !== current) patch[key] = next;
		}
		for (const { key } of secretFields) {
			const next = secrets[key].trim();
			if (next.length > 0) patch[key] = next;
		}
		if (authMethod !== normalizeAuthMethod(entry.authMethod)) patch.authMethod = authMethod;
		if (priority !== (entry.priority ?? 0)) patch.priority = priority;

		if (Object.keys(patch).length === 0) {
			setLocalError(t("editNoChanges"));
			return;
		}
		setLocalError(undefined);
		onSubmit(patch);
	};

	return (
		<PanelOverlay
			title={t("editTitle", { id: entry.id })}
			onClose={onCancel}
			footer={
				<Group justify="flex-end" gap="xs">
					<Button size="xs" variant="default" onClick={onCancel}>
						{t("editCancel")}
					</Button>
					<Button size="xs" loading={loading} onClick={submit}>
						{t("editSave")}
					</Button>
				</Group>
			}
		>
			<Stack gap="xs">
				{(error ?? localError) ? (
					<Alert color="red" variant="light">
						{error ?? localError}
					</Alert>
				) : null}
				<Text size="xs" c="dimmed">
					{t("editSecretHint")}
				</Text>

				<Stack gap={4}>
					<Text size="xs" fw={500}>
						{t("editAuthMethod")}
					</Text>
					<SegmentedControl
						size="xs"
						value={authMethod}
						onChange={(value: string) => setAuthMethod(value as AuthMethod)}
						data={[
							{ value: "social", label: "social" },
							{ value: "idc", label: "idc" },
							{ value: "api_key", label: "api_key" },
						]}
					/>
				</Stack>

				<NumberInput
					size="xs"
					label={t("editPriority")}
					description={t("editPriorityDesc")}
					value={priority}
					min={0}
					max={10000}
					onChange={(value: string | number) => setPriority(Number(value) || 0)}
				/>

				{TEXT_FIELDS.map(({ key, label }) => (
					<TextInput
						key={key}
						size="xs"
						label={label}
						value={text[key]}
						onChange={(event: { currentTarget: { value: string } }) =>
							setText((prev) => ({ ...prev, [key]: event.currentTarget.value }))
						}
					/>
				))}

				{secretFields.map(({ key, label, stored }) => (
					<PasswordInput
						key={key}
						size="xs"
						label={stored ? `${label} ●` : label}
						placeholder={stored ? t("editSecretStored") : undefined}
						value={secrets[key]}
						// Switching to api_key makes the OAuth secrets irrelevant and vice versa;
						// greying them out is clearer than accepting a value that will be ignored.
						disabled={
							isApiKey
								? key === "refreshToken" || key === "clientId" || key === "clientSecret"
								: key === "apiKey"
						}
						onChange={(event: { currentTarget: { value: string } }) =>
							setSecrets((prev) => ({ ...prev, [key]: event.currentTarget.value }))
						}
					/>
				))}
			</Stack>
		</PanelOverlay>
	);
}
