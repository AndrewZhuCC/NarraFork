/** @jsxImportSource ./shim */

/**
 * `provider-settings` view: Kiro credential management.
 *
 * ## What this page can and cannot do
 *
 * It runs in a sandboxed iframe with `connect-src 'none'`, so it cannot reach any HTTP
 * endpoint — not even the host's own admin API. Everything it does goes through
 * `commands.execute`, which the host routes to this plugin's backend.
 *
 * Its read surface is deliberately narrow: no credential value is ever readable here. Values
 * typed into the import box travel one way — into a command — and are never echoed back.
 *
 * ## Why React and Mantine
 *
 * The panel does not bundle either. It reads them off `globalThis.__nfPluginRuntime`, a shared
 * runtime the host injects before this file runs (see `host-runtime.ts`). That is what makes
 * this page visually identical to the built-in Kiro settings page — same components, same
 * theme object, same version — rather than an approximation of it. The plugin artifact stays a
 * couple of KB because the framework lives in the host's bundle.
 *
 * ## Interaction constraints worth knowing before editing
 *
 * - No `window.confirm` / `alert`: the sandbox lacks `allow-modals`, so the browser ignores
 *   them and returns `undefined`. Destructive actions use `ConfirmOverlay`.
 * - No Mantine `Modal`, `Select`, `Menu` or other portalled overlay: the panel is a
 *   fixed-height box with `overflow: hidden`, which clips them. Dialogs use `PanelOverlay`,
 *   and choices use `SegmentedControl`.
 */

import { CredentialList } from "./CredentialList";
import { EditForm } from "./EditForm";
import { containsApiKey, getTierMeta, parseApiKeyLines } from "./format";
import {
	createRoot,
	hostTheme,
	MantineCore,
	React,
	useCallback,
	useEffect,
	useMemo,
	useRef,
	useState,
} from "./host-runtime";
import { ConfirmOverlay, PanelOverlay } from "./Overlay";
import {
	type AccountTier,
	type CredentialEntry,
	type CredentialPatch,
	errorMessage,
	isRecord,
	type ModelEntry,
	type PluginUiSdk,
	runCommand,
	type StatusOutput,
	type UsageEntry,
} from "./panel-types";
import { onLocaleChange, t } from "./strings";

const {
	Alert,
	Badge,
	Box,
	Button,
	Divider,
	Group,
	Loader,
	MantineProvider,
	SegmentedControl,
	Stack,
	Text,
	Textarea,
} = MantineCore;

/** Matches the host page, so a pool paginates the same way in both. */
const PAGE_SIZE = 20;

const TIER_ORDER: AccountTier[] = ["paid", "enterprise", "free"];

type StatusTone = "neutral" | "good" | "bad";

interface PendingConfirm {
	message: string;
	label: string;
	run: () => Promise<unknown>;
}

interface ModelsDialog {
	entry: CredentialEntry;
	models?: ModelEntry[];
	error?: string;
}

interface UsageDialog {
	entry: CredentialEntry;
	usage?: UsageEntry;
	error?: string;
}

function CredentialPanel({ sdk }: { sdk: PluginUiSdk }) {
	const [status, setStatus] = useState<StatusOutput | undefined>();
	const [loadError, setLoadError] = useState<string | undefined>();
	const [message, setMessage] = useState<{ text: string; tone: StatusTone } | undefined>();
	const [busy, setBusy] = useState(false);
	const [pages, setPages] = useState<Record<AccountTier, number>>({
		paid: 1,
		enterprise: 1,
		free: 1,
	});
	const [selectedIds, setSelectedIds] = useState<Set<string>>(() => new Set());
	const [importText, setImportText] = useState("");
	const [editing, setEditing] = useState<CredentialEntry | undefined>();
	const [editError, setEditError] = useState<string | undefined>();
	const [confirming, setConfirming] = useState<PendingConfirm | undefined>();
	const [modelsDialog, setModelsDialog] = useState<ModelsDialog | undefined>();
	const [usageDialog, setUsageDialog] = useState<UsageDialog | undefined>();

	/*
	 * Re-render when the host language changes.
	 *
	 * `t()` resolves against the host's current locale on every call, so a re-render is all that is
	 * needed — but something has to cause one. Colours need no equivalent: the host rewrites the
	 * `--nf-*` variables and the browser repaints on its own. Text is the asymmetric case, because
	 * a string already committed to the DOM stays until React replaces it.
	 *
	 * The counter is the render trigger; its value is never read. Deriving it from the locale
	 * instead would be equivalent, but a monotonic bump cannot accidentally coalesce two changes
	 * that happen to resolve to the same tag.
	 */
	const [, bumpLocale] = useState(0);
	useEffect(() => onLocaleChange(() => bumpLocale((value) => value + 1)), []);

	/**
	 * True once the panel has unmounted.
	 *
	 * Every command here is a round trip through the host to the plugin backend, so any of them
	 * can still be in flight when the host closes the view. Most are started from click handlers
	 * rather than effects, so they have no effect scope to be cancelled by — a ref set by a
	 * teardown effect is the one thing all of them can consult before touching state.
	 */
	const unmounted = useRef(false);
	useEffect(() => {
		return () => {
			unmounted.current = true;
		};
	}, []);

	const command = useCallback(
		(commandId: string, input?: Record<string, unknown>) => runCommand(sdk, commandId, input),
		[sdk],
	);

	/** Re-read the snapshot for the pages currently on screen. */
	const refresh = useCallback(
		async (nextPages?: Record<AccountTier, number>) => {
			const active = nextPages ?? pages;
			const output = (await command("status", {
				pageSize: PAGE_SIZE,
				paidPage: active.paid,
				enterprisePage: active.enterprise,
				freePage: active.free,
			})) as StatusOutput | undefined;
			if (!output || !Array.isArray(output.entries)) {
				throw new Error("The status command returned no credential information");
			}
			if (unmounted.current) return output;
			setStatus(output);
			setLoadError(undefined);
			return output;
		},
		[command, pages],
	);

	/**
	 * Run a mutating action, then reload.
	 *
	 * Every failure lands in one place: a command that rejected silently would leave the panel
	 * showing stale state with no indication that anything went wrong.
	 */
	const run = useCallback(
		(label: string, action: () => Promise<unknown>) => {
			if (busy) return;
			setBusy(true);
			setMessage({ text: t("busyStatus", { label }), tone: "neutral" });
			action()
				.then(async () => {
					await refresh();
					if (unmounted.current) return;
					setMessage({ text: t("doneStatus", { label }), tone: "good" });
				})
				.catch((error: unknown) => {
					if (unmounted.current) return;
					setMessage({
						text: t("failedStatus", { label, error: errorMessage(error) }),
						tone: "bad",
					});
				})
				.finally(() => {
					if (unmounted.current) return;
					setBusy(false);
				});
		},
		[busy, refresh],
	);

	// Initial load. A failure here is reported in place of the page rather than as a toast,
	// because there is nothing else to show.
	useEffect(() => {
		// `cancelled` rather than the shared ref: this effect owns the request, so an unmount
		// must also suppress the `ready` notification — emitting it for a panel that no longer
		// exists would tell the host a view is usable after it closed it.
		let cancelled = false;
		refresh()
			.then(() => {
				if (cancelled) return;
				sdk.notify("kiro-external.settings.ready", {});
			})
			.catch((error: unknown) => {
				if (cancelled) return;
				setLoadError(errorMessage(error));
			});
		return () => {
			cancelled = true;
		};
		// Intentionally once on mount: `refresh` closes over `pages`, and page changes call it
		// directly with the new value.
	}, []);

	const changePage = useCallback(
		(tier: AccountTier, page: number) => {
			const next = { ...pages, [tier]: page };
			setPages(next);
			refresh(next).catch((error: unknown) => {
				if (unmounted.current) return;
				setMessage({
					text: t("failedStatus", { label: t("actionRefresh"), error: errorMessage(error) }),
					tone: "bad",
				});
			});
		},
		[pages, refresh],
	);

	const toggleSelect = useCallback((id: string) => {
		setSelectedIds((prev) => {
			const next = new Set(prev);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	}, []);

	const toggleSelectPage = useCallback((ids: string[]) => {
		setSelectedIds((prev) => {
			const next = new Set(prev);
			// All selected → clear the page; otherwise select the whole page. Matches the host's
			// tri-state header checkbox.
			const allSelected = ids.length > 0 && ids.every((id) => next.has(id));
			for (const id of ids) {
				if (allSelected) next.delete(id);
				else next.add(id);
			}
			return next;
		});
	}, []);

	const handleImport = useCallback(() => {
		const raw = importText.trim();
		if (raw.length === 0) {
			setMessage({ text: t("importEmpty"), tone: "bad" });
			return;
		}
		let parsed: unknown;
		try {
			parsed = JSON.parse(raw);
		} catch {
			// Not JSON: fall back to bare `ksk_` lines, which the host page also accepts.
			const keys = parseApiKeyLines(raw);
			if (keys.length > 0) {
				parsed = keys;
			} else if (containsApiKey(raw)) {
				// Comma-separated or otherwise embedded keys: let the backend normalizer handle it.
				parsed = [raw];
			} else {
				setMessage({ text: t("importInvalid"), tone: "bad" });
				return;
			}
		}
		// Cleared immediately: the textarea is the only place a secret exists in this document,
		// and it must not outlive the request.
		setImportText("");
		run(t("actionImport"), async () => {
			const result = await command("credentials.import", { credentials: parsed });
			if (isRecord(result)) {
				setMessage({
					text: t("importSuccess", {
						added: Number(result.added ?? 0),
						duplicates: Number(result.duplicates ?? 0),
					}),
					tone: "good",
				});
			}
		});
	}, [importText, run, command]);

	const handleSubmitEdit = useCallback(
		(patch: CredentialPatch) => {
			const entry = editing;
			if (!entry) return;
			setBusy(true);
			setEditError(undefined);
			command("credentials.update", { credentialId: entry.id, fields: patch })
				.then(async () => {
					if (unmounted.current) return;
					setEditing(undefined);
					await refresh();
					if (unmounted.current) return;
					setMessage({ text: t("doneStatus", { label: t("actionUpdate") }), tone: "good" });
				})
				.catch((error: unknown) => {
					if (unmounted.current) return;
					setEditError(errorMessage(error));
				})
				.finally(() => {
					if (unmounted.current) return;
					setBusy(false);
				});
		},
		[editing, command, refresh],
	);

	const showModels = useCallback(
		(entry: CredentialEntry) => {
			setModelsDialog({ entry });
			command("models.list", { credentialId: entry.id })
				.then((result) => {
					if (unmounted.current) return;
					const models =
						isRecord(result) && Array.isArray(result.models) ? (result.models as ModelEntry[]) : [];
					setModelsDialog({ entry, models });
				})
				.catch((error: unknown) => {
					if (unmounted.current) return;
					setModelsDialog({ entry, error: errorMessage(error) });
				});
		},
		[command],
	);

	const showUsage = useCallback(
		(entry: CredentialEntry) => {
			setUsageDialog({ entry });
			command("credentials.usage", { credentialId: entry.id })
				.then(async (result) => {
					if (unmounted.current) return;
					const usage =
						isRecord(result) && isRecord(result.usage) ? (result.usage as UsageEntry) : undefined;
					setUsageDialog({ entry, usage });
					// The backend caches what it fetched, so a reload makes the inline column agree
					// with the dialog. This is a second round trip, so re-check before it lands.
					await refresh().catch(() => {});
				})
				.catch((error: unknown) => {
					if (unmounted.current) return;
					setUsageDialog({ entry, error: errorMessage(error) });
				});
		},
		[command, refresh],
	);

	const groups = useMemo(() => {
		if (!status) return [];
		const byTier: Record<AccountTier, { entries: CredentialEntry[]; total: number }> = {
			paid: { entries: status.paidEntries ?? [], total: status.paidTotal ?? 0 },
			enterprise: {
				entries: status.enterpriseEntries ?? [],
				total: status.enterpriseTotal ?? 0,
			},
			free: { entries: status.freeEntries ?? [], total: status.freeTotal ?? 0 },
		};
		return TIER_ORDER.filter((tier) => byTier[tier].total > 0).map((tier) => ({
			tier,
			...byTier[tier],
		}));
	}, [status]);

	if (loadError) {
		return (
			<Alert color="red" variant="light" title={t("runtimeMissingTitle")}>
				{loadError}
			</Alert>
		);
	}
	if (!status) {
		return (
			<Group justify="center" p="lg">
				<Loader size="sm" />
			</Group>
		);
	}

	const usageCache = status.usageCache ?? {};
	const unhealthyTotal = status.unhealthyTotal ?? 0;

	return (
		<Stack gap="md">
			<Group justify="space-between">
				<Text fw={500} size="sm">
					{t("credentials")}
				</Text>
				<Badge size="sm" color={status.available > 0 ? "green" : "gray"}>
					{status.available} / {status.total}
				</Badge>
			</Group>
			<Text size="xs" c="dimmed">
				{t("credentialsDesc")}
			</Text>

			<Group gap="xs" align="center">
				<Text size="sm">{t("loadBalancing")}:</Text>
				<SegmentedControl
					size="xs"
					value={status.loadBalancingMode}
					disabled={busy}
					onChange={(value: string) =>
						run(t("actionSwitchMode", { mode: value }), () =>
							command("config.setLoadBalancing", { mode: value }),
						)
					}
					data={[
						{ value: "priority", label: t("modePriority") },
						{ value: "balanced", label: t("modeBalanced") },
					]}
				/>
			</Group>

			<Divider />

			<Text fw={500} size="sm">
				{t("importTitle")}
			</Text>
			<Textarea
				size="xs"
				placeholder={t("importPlaceholder")}
				value={importText}
				onChange={(event: { currentTarget: { value: string } }) =>
					setImportText(event.currentTarget.value)
				}
				minRows={2}
				autosize
				styles={{ input: { fontFamily: "monospace", fontSize: 12 } }}
			/>
			<Text size="xs" c="dimmed">
				{t("importHint")}
			</Text>
			<Button
				size="xs"
				variant="light"
				style={{ alignSelf: "flex-start" }}
				disabled={busy || importText.trim().length === 0}
				onClick={handleImport}
			>
				{t("importBtn")}
			</Button>

			{status.total === 0 ? (
				<Text c="dimmed" size="sm">
					{t("noCredentials")}
				</Text>
			) : null}

			{status.total > 0 ? (
				<Group gap="xs" justify="flex-end" align="center" wrap="wrap">
					<Button
						size="compact-xs"
						variant="light"
						disabled={busy}
						onClick={() =>
							setConfirming({
								message: t("refreshAllModelsConfirm"),
								label: t("actionRefreshAllModels"),
								run: () => command("models.refreshAll"),
							})
						}
					>
						{t("refreshAllModels")}
					</Button>
					{selectedIds.size > 0 ? (
						<Button
							size="compact-xs"
							color="red"
							variant="light"
							disabled={busy}
							onClick={() =>
								setConfirming({
									message: t("batchDeleteConfirm", { count: selectedIds.size }),
									label: t("actionBatchDelete"),
									run: async () => {
										const ids = [...selectedIds];
										const result = await command("credentials.removeMany", {
											credentialIds: ids,
										});
										setSelectedIds(new Set());
										const removed =
											isRecord(result) && Array.isArray(result.removed)
												? result.removed.length
												: ids.length;
										setMessage({ text: t("batchDeleteSuccess", { count: removed }), tone: "good" });
									},
								})
							}
						>
							{t("batchDelete")} ({selectedIds.size})
						</Button>
					) : null}
					<Button
						size="compact-xs"
						color="red"
						variant="outline"
						disabled={busy || unhealthyTotal === 0}
						onClick={() =>
							setConfirming({
								message: t("deleteUnhealthyConfirm", { count: unhealthyTotal }),
								label: t("actionDeleteUnhealthy"),
								run: async () => {
									const result = await command("credentials.removeUnhealthy");
									const removed =
										isRecord(result) && Array.isArray(result.removed) ? result.removed.length : 0;
									setMessage({
										text: t("deleteUnhealthySuccess", { count: removed }),
										tone: "good",
									});
								},
							})
						}
					>
						{t("deleteUnhealthy")} ({unhealthyTotal})
					</Button>
				</Group>
			) : null}

			{groups.map(({ tier, entries, total }) => {
				const meta = getTierMeta(tier);
				return (
					<Stack key={tier} gap="xs">
						<Group gap="xs">
							<Badge color={meta.color} size="sm" variant="filled">
								{meta.label}
							</Badge>
							<Text size="xs" c="dimmed">
								{meta.desc} · {total}
							</Text>
						</Group>
						<CredentialList
							entries={entries}
							total={total}
							page={pages[tier]}
							pageSize={PAGE_SIZE}
							onPageChange={(page: number) => changePage(tier, page)}
							currentId={status.currentId}
							usageCache={usageCache}
							selectedIds={selectedIds}
							onToggleSelect={toggleSelect}
							onToggleSelectPage={toggleSelectPage}
							busy={busy}
							onToggleEnabled={(entry) =>
								run(entry.disabled ? t("actionEnable") : t("actionDisable"), () =>
									command("credentials.setState", {
										credentialId: entry.id,
										state: entry.disabled ? "enabled" : "disabled",
									}),
								)
							}
							onRefreshToken={(entry) =>
								run(t("actionRefresh"), () =>
									command("credentials.refresh", { credentialId: entry.id }),
								)
							}
							onReset={(entry) =>
								run(t("reset"), () =>
									command("credentials.setState", { credentialId: entry.id, state: "reset" }),
								)
							}
							onEdit={(entry) => {
								setEditError(undefined);
								setEditing(entry);
							}}
							onDelete={(entry) =>
								setConfirming({
									message: t("deleteConfirm"),
									label: t("actionRemove"),
									run: () => command("credentials.remove", { credentialId: entry.id }),
								})
							}
							onQueryUsage={showUsage}
							onShowModels={showModels}
						/>
					</Stack>
				);
			})}

			{message ? (
				<Text
					size="xs"
					c={message.tone === "bad" ? "red" : message.tone === "good" ? "green" : "dimmed"}
				>
					{message.text}
				</Text>
			) : null}

			{editing ? (
				<EditForm
					entry={editing}
					loading={busy}
					error={editError}
					onSubmit={handleSubmitEdit}
					onCancel={() => {
						setEditing(undefined);
						setEditError(undefined);
					}}
				/>
			) : null}

			{confirming ? (
				<ConfirmOverlay
					message={confirming.message}
					loading={busy}
					onCancel={() => setConfirming(undefined)}
					onConfirm={() => {
						const pending = confirming;
						setConfirming(undefined);
						run(pending.label, pending.run);
					}}
				/>
			) : null}

			{modelsDialog ? (
				<PanelOverlay
					title={`${t("models")} · ${modelsDialog.entry.displayName || modelsDialog.entry.id}`}
					onClose={() => setModelsDialog(undefined)}
				>
					{modelsDialog.error ? (
						<Alert color="red" variant="light">
							{modelsDialog.error}
						</Alert>
					) : !modelsDialog.models ? (
						<Group justify="center" p="md">
							<Loader size="xs" />
							<Text size="xs" c="dimmed">
								{t("modelsLoading")}
							</Text>
						</Group>
					) : modelsDialog.models.length === 0 ? (
						<Text size="sm" c="dimmed">
							{t("modelsNone")}
						</Text>
					) : (
						<Stack gap={4}>
							<Text size="xs" fw={500}>
								{t("modelsCount", { count: modelsDialog.models.length })}
							</Text>
							{modelsDialog.models.map((model) => (
								<Group key={model.id} justify="space-between" wrap="nowrap">
									<Text size="xs" ff="monospace">
										{model.id}
									</Text>
									{model.contextWindow ? (
										<Text size="xs" c="dimmed">
											{Math.round(model.contextWindow / 1024)}k
										</Text>
									) : null}
								</Group>
							))}
						</Stack>
					)}
				</PanelOverlay>
			) : null}

			{usageDialog ? (
				<PanelOverlay
					title={`${t("colUsage")} · ${usageDialog.entry.displayName || usageDialog.entry.id}`}
					onClose={() => setUsageDialog(undefined)}
				>
					{usageDialog.error ? (
						<Alert color="red" variant="light">
							{usageDialog.error}
						</Alert>
					) : !usageDialog.usage ? (
						<Group justify="center" p="md">
							<Loader size="xs" />
							<Text size="xs" c="dimmed">
								{t("usageLoading")}
							</Text>
						</Group>
					) : (
						<Stack gap={4}>
							{usageDialog.usage.subscriptionTitle ? (
								<Text size="sm">
									{t("usagePlan")}: {usageDialog.usage.subscriptionTitle}
								</Text>
							) : null}
							<Text size="sm">
								{t("colUsage")}: {usageDialog.usage.currentUsage ?? 0} /{" "}
								{usageDialog.usage.usageLimit ?? "-"}
								{usageDialog.usage.usagePercentage != null
									? ` (${usageDialog.usage.usagePercentage}%)`
									: ""}
							</Text>
							<Text size="sm">
								{t("usageRemaining")}: {usageDialog.usage.remaining ?? "-"}
							</Text>
							{usageDialog.usage.overageStatus ? (
								<Text size="sm">
									{t("usageOverage")}: {usageDialog.usage.overageStatus}
								</Text>
							) : null}
						</Stack>
					)}
				</PanelOverlay>
			) : null}
		</Stack>
	);
}

/**
 * Report a failure without using the host runtime.
 *
 * Deliberately plain DOM: the failures this covers are "React or Mantine is unusable", so
 * rendering the message with them would fail the same way and leave the panel blank — which
 * is indistinguishable from the panel never having been asked to load.
 *
 * `strings.ts` is safe here because it reads `globalThis.narrafork.i18n`, which the host installs
 * before this file runs and which does not depend on the runtime that just failed. (It previously
 * read `navigator.language`; the guarantee is the same, the source is not.)
 */
function renderFatal(detail: string): void {
	const box = document.createElement("div");
	box.style.padding = "12px";
	box.style.font = "13px/1.5 var(--nf-font, system-ui, sans-serif)";
	// Host tokens with the previous values as fallbacks: this path runs when the shared runtime is
	// broken, but the token stylesheet is injected by the shell and is unaffected by that.
	box.style.color = "var(--nf-color-error, #ffa8a8)";
	box.style.background = "var(--nf-color-surface, #2e1a1a)";
	box.style.borderRadius = "6px";
	box.style.margin = "12px";
	box.style.whiteSpace = "pre-wrap";
	box.style.wordBreak = "break-word";

	const title = document.createElement("div");
	title.style.fontWeight = "600";
	title.style.marginBottom = "4px";
	// Reuses the key the in-panel load error already shows, so the two paths name the same
	// problem rather than inventing a second wording for it.
	title.textContent = t("runtimeMissingTitle");
	box.appendChild(title);

	const body = document.createElement("div");
	body.textContent = detail;
	box.appendChild(body);

	document.body.style.margin = "0";
	document.body.appendChild(box);
}

interface PanelBoundaryState {
	message?: string;
}

// biome-ignore lint/suspicious/noExplicitAny: the host's React is untyped at this boundary
const HostComponent: any = React.Component;

/**
 * Stop a render-time throw from blanking the panel.
 *
 * React unmounts the whole tree when a render throws and nothing catches it, so without this
 * a single bad response shape or an incompatible Mantine prop turns the panel into an empty
 * box with only a console trace — inside an iframe, where nobody is looking at the console.
 */
class PanelErrorBoundary extends HostComponent {
	state: PanelBoundaryState = {};

	static getDerivedStateFromError(error: unknown): PanelBoundaryState {
		return { message: errorMessage(error) };
	}

	render(): unknown {
		const { message } = this.state as PanelBoundaryState;
		if (message) {
			return (
				<Alert color="red" variant="light" title={t("runtimeMissingTitle")}>
					{message}
				</Alert>
			);
		}
		return (this.props as { children?: unknown }).children;
	}
}

/**
 * Mount the panel.
 *
 * `position: relative` on the root is load-bearing: `PanelOverlay` positions itself against
 * this element, so without it a dialog would escape to the document and be clipped by the
 * frame.
 */
function mount(sdk: PluginUiSdk): void {
	const root = document.createElement("div");
	root.style.position = "relative";
	root.style.minHeight = "100%";
	root.style.padding = "12px";
	root.style.boxSizing = "border-box";
	document.body.style.margin = "0";
	document.body.appendChild(root);

	createRoot(root).render(
		<MantineProvider theme={hostTheme} defaultColorScheme="dark" forceColorScheme="dark">
			<Box>
				<PanelErrorBoundary>
					<CredentialPanel sdk={sdk} />
				</PanelErrorBoundary>
			</Box>
		</MantineProvider>,
	);
}

const sdk = (globalThis as { narrafork?: PluginUiSdk }).narrafork;
if (sdk) {
	// A throw here is not recoverable — `createRoot` failing, or a runtime that satisfied
	// `host-runtime.ts`'s checks but is not actually usable — but it must still say so.
	// Letting it escape leaves an empty iframe with no indication anything was attempted.
	try {
		mount(sdk);
	} catch (error: unknown) {
		renderFatal(errorMessage(error));
	}
} else {
	// The host injects `globalThis.narrafork` before loading this file. Without it the panel
	// cannot reach its backend at all, and previously did nothing at all to say so.
	renderFatal("The host did not provide the plugin UI SDK (globalThis.narrafork).");
}
