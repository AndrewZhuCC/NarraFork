import { afterAll, afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { notifications } from "@mantine/notifications";
import type { ComposerDocument } from "@shared/composer-document";
import { parseHTML } from "linkedom";
import { act, type ComponentProps, type ReactNode, useImperativeHandle } from "react";
import { createRoot, type Root } from "react-dom/client";
import { api } from "../../lib/api";
import type { ModelOption } from "../../lib/constants";
import { composerAssetsFromFiles } from "../narrator/composer/composer-document-adapter";
import { loadDraftImageAttachments } from "../narrator/composer/draft-image-attachments";
import type {
	NarratorComposer,
	NarratorComposerHandle,
} from "../narrator/composer/NarratorComposer";

// Keep the real ingestion, attachment persistence, document snapshot, and API call
// boundary. Only external navigation/settings and the editor/UI shell are replaced.
const modules = [
	"@mantine/core",
	"@tanstack/react-query",
	"@tanstack/react-router",
	"react-i18next",
	"../../hooks/useAuth",
	"../../hooks/useModelCatalog",
	"../../hooks/useModels",
	"../../hooks/useNarrator",
	"../../hooks/usePlatform",
	"../../hooks/useProjectDirStatus",
	"../../hooks/useRecentTabs",
	"../../hooks/useUserPreferences",
	"../../hooks/useWorkspaceContext",
	"../common/DeviceDirectoryPicker",
	"../narrator/CreateNarratorLazyModal",
	"../narrator/composer/NarratorComposer",
	"../narrator/interaction/PermissionModePickerMenu",
	"../narrator/interaction/UploadProgressBar",
	"../narrator/model/ModelPickerMenu",
	"../narrator/model/ReasoningEffortPickerMenu",
] as const;
const originals = new Map<string, object>();
for (const id of modules) originals.set(id, await import(id));
const replace = (id: (typeof modules)[number], exports: object) =>
	mock.module(id, () => ({ ...originals.get(id), ...exports }));
const shell = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
const codexModel = "codex:gpt-test";
const otherCodexModel = "relay:gpt-test";
const unlistedModel = "relay:gpt-other";
const autoModel = "auto:gpt-test";
const aggregation = { id: "mix", name: "Mixed", models: [codexModel], routingMode: "priority" };
const aggregationModel = `__agg__:${aggregation.id}`;
const pinnedModel = `${aggregationModel}:${codexModel}`;
const modelOptions: ModelOption[] = [
	{ value: codexModel, label: "GPT Test" },
	{ value: otherCodexModel, label: "GPT Test (Relay)" },
	{ value: unlistedModel, label: "Unlisted Model" },
	{
		value: autoModel,
		label: "GPT Test (Auto)",
		autoSources: [{ value: codexModel, provider: "codex", available: true, subscription: true }],
	},
];
let codexAvailable = false;
let codexUltrafastModels: string[] = [];
let fastModeDefault = false;
const button = ({
	children,
	onClick,
	disabled,
	"aria-label": label,
	"aria-pressed": pressed,
}: {
	children?: ReactNode;
	onClick?: () => void;
	disabled?: boolean;
	"aria-label"?: string;
	"aria-pressed"?: boolean;
}) => (
	<button
		type="button"
		disabled={disabled}
		aria-label={label}
		aria-pressed={pressed}
		onClick={onClick}
	>
		{children}
	</button>
);
const create = mock(async (_data: Parameters<typeof api.createNarrator>[0]) => ({
	id: "new-session",
	title: "New",
	cwd: "/workspace",
	status: "idle",
}));
const navigate = mock(() => {});
const noteSent = mock(() => {});
const clearText = mock(() => {});
let props: ComponentProps<typeof NarratorComposer>;
let documentOverride: ComposerDocument | undefined;
let progressState: { attachmentCount: number; progress: number | null; canCancel: boolean } | null;
function documentFromProps(): ComposerDocument | undefined {
	return documentOverride;
}
replace("@mantine/core", {
	ActionIcon: button,
	Box: shell,
	Button: button,
	Group: shell,
	Popover: Object.assign(shell, { Target: shell, Dropdown: shell }),
	SegmentedControl: ({
		data,
		value,
		onChange,
		disabled,
	}: {
		data: Array<{ value: string; label: string }>;
		value: string;
		onChange: (value: string) => void;
		disabled?: boolean;
	}) => (
		<>
			{data.map((option) => (
				<button
					type="button"
					key={option.value}
					aria-current={value === option.value}
					disabled={disabled}
					onClick={() => onChange(option.value)}
				>
					{option.label}
				</button>
			))}
		</>
	),
	Stack: shell,
	Switch: () => null,
	Text: shell,
	Tooltip: shell,
});
replace("@tanstack/react-query", {
	useQuery: ({ queryKey }: { queryKey: readonly string[] }) => ({
		data:
			queryKey[0] === "user-preferences"
				? { fastModeDefault }
				: { agent: { defaultPermissionMode: "default", defaultStartInPlanMode: false } },
	}),
});
replace("@tanstack/react-router", { useNavigate: () => navigate });
const translate = (key: string) => key;
replace("react-i18next", { useTranslation: () => ({ t: translate }) });
replace("../../hooks/useAuth", { useCurrentUser: () => ({ data: { id: "dashboard-user" } }) });
replace("../../hooks/useNarrator", { useCreateNarrator: () => ({ mutateAsync: create }) });
replace("../../hooks/usePlatform", {
	useNarratorPermissionsCapability: () => ({ supported: false }),
	useNarratorPlanModeCapability: () => ({ supported: false }),
});
replace("../../hooks/useProjectDirStatus", {
	useProjectDirStatus: () => ({ usablePath: "/workspace", needsChoice: false }),
});
replace("../../hooks/useRecentTabs", { addRecentTab: async () => {} });
replace("../../hooks/useUserPreferences", {
	useUserPreferences: () => ({ data: { fastModeDefault } }),
	useUpdateUserPreferences: () => ({ mutate: () => {} }),
});
replace("../../hooks/useWorkspaceContext", {
	useWorkspaceDevices: () => ({ data: { devices: [] }, isSuccess: true }),
});
replace("../common/DeviceDirectoryPicker", { DeviceDirectoryModal: () => null });
replace("../narrator/CreateNarratorLazyModal", { CreateNarratorLazyModal: () => null });
replace("../narrator/interaction/PermissionModePickerMenu", {
	PermissionModePickerMenu: () => null,
});
replace("../narrator/model/ModelPickerMenu", {
	ModelPickerMenu: ({ onChange }: { onChange: (value: string) => void }) => (
		<>
			{[otherCodexModel, unlistedModel, autoModel, aggregationModel, pinnedModel].map((value) => (
				<button type="button" key={value} data-model={value} onClick={() => onChange(value)}>
					{value}
				</button>
			))}
		</>
	),
});
replace("../narrator/model/ReasoningEffortPickerMenu", { ReasoningEffortPickerMenu: () => null });
replace("../../hooks/useModelCatalog", { useActualResolvedModelCard: () => ({}) });
replace("../../hooks/useModels", {
	useAllModels: () => ({
		visibleWithDefault: modelOptions,
		defaultModelValue: codexModel,
		aggregations: [aggregation],
		settingsData: {
			codexAvailable,
			openaiProviders: [{ prefix: "relay", apiMode: "codex" }],
			agent: { codexUltrafastModels },
		},
	}),
});
replace("../narrator/interaction/UploadProgressBar", {
	UploadProgressBar: ({ sendingState }: { sendingState: typeof progressState }) => {
		progressState = sendingState;
		return null;
	},
});
replace("../narrator/composer/NarratorComposer", {
	NarratorComposer: (next: ComponentProps<typeof NarratorComposer>) => {
		props = next;
		useImperativeHandle(
			next.ref,
			() =>
				({
					getText: () => "",
					getDocument: documentFromProps,
					getFileReferences: () => [],
					hasMissingAssets: () => false,
					captureAttachmentInsertion: () => ({ restore: () => true, cancel: () => {} }),
					insertAcceptedFiles: (files: File[]) => {
						const images = [
							...(props.attachedImages ?? []),
							...files.filter((file) => file.type.startsWith("image/")),
						];
						const media = [
							...(props.attachedMedia ?? []),
							...files.filter(
								(file) => file.type.startsWith("audio/") || file.type.startsWith("video/"),
							),
						];
						const texts = [
							...(props.attachedTextFiles ?? []),
							...files.filter((file) => file.type.startsWith("text/")),
						];
						props.onAttachmentsChange?.(images, texts, media);
						return true;
					},
					noteSent,
					clearTextAndDraft: clearText,
				}) as unknown as NarratorComposerHandle,
		);
		return <div>{next.actions}</div>;
	},
});
const { DashboardComposer, DASHBOARD_COMPOSER_KEY } = await import("./DashboardComposer");

const records = new Map<string, Record<string, unknown>>();
const globals = new Map<string, PropertyDescriptor | undefined>();
let closeDatabase: (() => void) | undefined;
let root: Root;
let host: HTMLElement;
let send: ReturnType<typeof mock<typeof api.sendNarratorMessage>>;
let notify: ReturnType<typeof spyOn<typeof notifications, "show">>;
function request<T>(result: T) {
	const pending = { result, onsuccess: undefined as (() => void) | undefined };
	queueMicrotask(() => pending.onsuccess?.());
	return pending;
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { resolve, promise };
}
beforeEach(async () => {
	records.clear();
	const { window } = parseHTML("<!doctype html><html><body></body></html>");
	const database = {
		close() {},
		set onversionchange(handler: () => void) {
			closeDatabase = handler;
		},
		transaction() {
			const transaction = {
				oncomplete: undefined as (() => void) | undefined,
				objectStore: () => ({
					get: (key: string) => request(records.get(key)),
					getAll: () => request([...records.values()]),
					put(value: Record<string, unknown>) {
						records.set(String(value.draftKey), structuredClone(value));
						queueMicrotask(() => transaction.oncomplete?.());
					},
					delete(key: string) {
						records.delete(key);
						queueMicrotask(() => transaction.oncomplete?.());
					},
				}),
			};
			return transaction;
		},
	};
	for (const [key, value] of Object.entries({
		window,
		document: window.document,
		navigator: window.navigator,
		HTMLElement: window.HTMLElement,
		Element: window.Element,
		Node: window.Node,
		IS_REACT_ACT_ENVIRONMENT: true,
		localStorage: { getItem: () => null, setItem() {} },
		indexedDB: { open: () => request(database) },
	})) {
		globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
		Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
	}
	codexAvailable = false;
	codexUltrafastModels = [];
	fastModeDefault = false;
	create.mockClear();
	navigate.mockClear();
	noteSent.mockClear();
	clearText.mockClear();
	documentOverride = undefined;
	progressState = null;
	send = mock(
		async () => ({ id: "message" }) as Awaited<ReturnType<typeof api.sendNarratorMessage>>,
	);
	notify = spyOn(notifications, "show").mockImplementation(() => "notice");
	api.sendNarratorMessage = send;
	host = document.body.appendChild(document.createElement("div"));
	root = createRoot(host);
	await act(async () => {
		root.render(<DashboardComposer />);
	});
});
const originalSend = api.sendNarratorMessage;
afterEach(async () => {
	await act(async () => root.unmount());
	host.remove();
	await new Promise((resolve) => setTimeout(resolve, 0));
	closeDatabase?.();
	closeDatabase = undefined;
	api.sendNarratorMessage = originalSend;
	notify.mockRestore();
	for (const [key, descriptor] of globals) {
		if (descriptor) Object.defineProperty(globalThis, key, descriptor);
		else Reflect.deleteProperty(globalThis, key);
	}
	globals.clear();
});
afterAll(() => {
	for (const [id, original] of originals) mock.module(id, () => original);
});
const audio = (name = "speech.wav") =>
	new File(["original dashboard audio"], name, { type: "audio/wav" });
const video = () => new File(["original dashboard video"], "clip.mp4", { type: "video/mp4" });
async function attach(files: File[]) {
	await act(async () => {
		await props.onPasteImages?.(files);
	});
}
async function submit() {
	await act(async () => {
		await props.onSendWithMode?.("turn");
	});
}

test("Dashboard offers Fast Mode for Codex and can explicitly turn off the enabled default", async () => {
	const fastModeButton = () => host.querySelector<HTMLButtonElement>("button[aria-pressed]");
	expect(fastModeButton()).toBeNull();
	codexAvailable = true;
	fastModeDefault = true;
	await act(async () => root.render(<DashboardComposer />));
	const button = fastModeButton();
	expect(button?.getAttribute("aria-label")).toBe("fast_mode");
	expect(button?.querySelector("svg")).not.toBeNull();
	expect(button?.textContent).toBe("");
	expect(button?.getAttribute("aria-pressed")).toBe("true");
	await act(async () => button?.click());
	expect(fastModeButton()?.getAttribute("aria-pressed")).toBe("false");
	await attach([audio()]);
	await submit();
	expect(create).toHaveBeenCalledWith(expect.objectContaining({ fastModeOverride: "off" }));
	expect(fastModeButton()?.getAttribute("aria-pressed")).toBe("true");
});

test("Dashboard applies Ultrafast from the first send for a named model across Codex providers", async () => {
	const ultrafastOption = () =>
		Array.from(host.querySelectorAll<HTMLButtonElement>("button[aria-current]")).find(
			(item) => item.textContent === "fast_mode_ultrafast",
		);
	const pickModel = async (value: string) => {
		const button = host.querySelector<HTMLButtonElement>(`button[data-model="${value}"]`);
		expect(button).not.toBeNull();
		await act(async () => button?.click());
	};
	codexAvailable = true;
	codexUltrafastModels = ["gpt-test"];
	await act(async () => root.render(<DashboardComposer />));
	expect(ultrafastOption()?.getAttribute("aria-current")).toBe("false");
	await pickModel(autoModel);
	expect(ultrafastOption()).not.toBeUndefined();
	await pickModel(otherCodexModel);
	expect(ultrafastOption()).not.toBeUndefined();
	await pickModel(aggregationModel);
	expect(ultrafastOption()).toBeUndefined();
	await pickModel(pinnedModel);
	await act(async () => ultrafastOption()?.click());
	expect(ultrafastOption()?.getAttribute("aria-current")).toBe("true");
	expect(host.querySelector("button[aria-pressed]")?.getAttribute("aria-label")).toBe(
		"fast_mode_ultrafast",
	);
	await attach([audio()]);
	await submit();
	expect(create).toHaveBeenCalledWith(
		expect.objectContaining({ model: pinnedModel, fastModeOverride: "ultrafast" }),
	);
	expect(send.mock.calls[0]?.[0]).toBe("new-session");

	await act(async () => ultrafastOption()?.click());
	await pickModel(unlistedModel);
	expect(ultrafastOption()).toBeUndefined();
	expect(host.querySelector("button[aria-pressed]")?.getAttribute("aria-label")).toBe("fast_mode");
	await attach([audio()]);
	await submit();
	expect(create).toHaveBeenCalledTimes(2);
	expect(create.mock.calls[1]?.[0]).toMatchObject({ model: unlistedModel });
	expect(create.mock.calls[1]?.[0]).not.toHaveProperty("fastModeOverride");
});

test("Dashboard stages and persists media-only drafts through the real ingestion hook", async () => {
	const speech = audio();
	const clip = video();
	await attach([speech, clip]);
	expect(props.hasAttachments).toBe(true);
	expect(props.attachedMedia).toEqual([speech, clip]);
	const draft = await loadDraftImageAttachments("dashboard-user", DASHBOARD_COMPOSER_KEY);
	expect(draft?.media?.map((file) => file.name)).toEqual([speech.name, clip.name]);
	expect(await draft?.media?.[0].text()).toBe(await speech.text());
});

test("Dashboard media-only send creates a session and sends native originals in API slot 11", async () => {
	const speech = audio();
	await attach([speech]);
	await submit();
	expect(create).toHaveBeenCalledTimes(1);
	expect(send).toHaveBeenCalledTimes(1);
	expect(send.mock.calls[0][0]).toBe("new-session");
	expect(send.mock.calls[0][1]).toBe("");
	expect(send.mock.calls[0][11]).toEqual([speech]);
	expect(props.attachedMedia).toEqual([]);
	expect(clearText).toHaveBeenCalledTimes(1);
	expect(navigate).toHaveBeenCalledTimes(1);
});

test("Dashboard snapshots media in manifest order and includes it in upload progress", async () => {
	const speech = audio();
	const clip = video();
	await attach([speech, clip]);
	const assets = composerAssetsFromFiles([], [], [clip, speech]);
	documentOverride = {
		version: 1,
		assets,
		paragraphs: [
			{
				children: assets.map((asset) => ({
					type: "attachment",
					id: `node-${asset.id}`,
					assetId: asset.id,
				})),
			},
		],
	};
	const pending = deferred<Awaited<ReturnType<typeof api.sendNarratorMessage>>>();
	send.mockImplementation(() => pending.promise);
	let submitted: Promise<void> = Promise.resolve();
	await act(async () => {
		submitted = props.onSendWithMode?.("turn") as Promise<void>;
	});
	expect(send.mock.calls[0][11]).toEqual([clip, speech]);
	expect(send.mock.calls[0][10]).toEqual({ composerDocument: documentOverride });
	expect(progressState).toMatchObject({ attachmentCount: 2, progress: 0, canCancel: true });
	await act(async () => {
		pending.resolve({ id: "message" } as Awaited<ReturnType<typeof api.sendNarratorMessage>>);
		await submitted;
	});
	expect(noteSent).toHaveBeenCalledTimes(1);
});

test("Dashboard failed upload keeps originals and reuses the created session on retry", async () => {
	const speech = audio();
	await attach([speech]);
	send.mockRejectedValueOnce(new Error("upload failed"));
	await submit();
	expect(props.attachedMedia).toEqual([speech]);
	expect(clearText).not.toHaveBeenCalled();
	expect(navigate).not.toHaveBeenCalled();
	expect(
		await (
			await loadDraftImageAttachments("dashboard-user", DASHBOARD_COMPOSER_KEY)
		)?.media?.[0].text(),
	).toBe(await speech.text());
	await submit();
	expect(create).toHaveBeenCalledTimes(1);
	expect(send).toHaveBeenCalledTimes(2);
	expect(send.mock.calls[1][11]).toEqual([speech]);
});
