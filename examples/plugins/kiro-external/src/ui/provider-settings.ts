/**
 * `provider-settings` view: full Kiro credential management.
 *
 * ## What this page can and cannot do
 *
 * It runs in a sandboxed iframe with `connect-src 'none'`, so it cannot reach any HTTP
 * endpoint — not even the host's own admin API. Everything it does goes through
 * `commands.execute`, which the host routes to this plugin's backend.
 *
 * Its read surface is deliberately narrow: `config.get` returns non-secret config only, and
 * `secrets.list` reports key names with a configured/not-configured flag. **No credential
 * value is ever readable here.** Values entered into the import box travel one way — into a
 * command — and are never echoed back.
 *
 * ## Why plain DOM
 *
 * The iframe is a separate document, so the host's React and Mantine are unavailable. The
 * built-in Kiro panel is ~1500 lines of React; this is longer per feature but has no
 * framework, which keeps the plugin bundle small and the contract obvious.
 */

interface PluginUiSdk {
	request(method: string, params?: unknown): Promise<unknown>;
	notify(event: string, payload?: unknown): void;
}

interface CredentialEntry {
	id: string;
	priority: number;
	disabled: boolean;
	disabledReason?: string;
	failureCount?: number;
	successCount?: number;
	lastUsedAt?: string;
	email?: string;
	displayName?: string;
	hasProfileArn?: boolean;
	hasStartUrl?: boolean;
	expiresAt?: string;
	authMethod?: string;
	isApiKey?: boolean;
	subscriptionTitle?: string;
	region?: string;
	authRegion?: string;
	apiRegion?: string;
	profileArn?: string;
	startUrl?: string;
	machineId?: string;
	hasClientId?: boolean;
	hasClientSecret?: boolean;
	proxyUrl?: string;
	proxyUsername?: string;
	hasProxyPassword?: boolean;
	meterUsage?: number;
	availableModels?: string[];
	tier?: string;
}

interface StatusOutput {
	credentialCount: number;
	availableCount: number;
	loadBalancingMode: string;
	currentId?: string;
	entries: CredentialEntry[];
}

interface UsageData {
	id: string;
	subscriptionTitle?: string;
	currentUsage: number;
	usageLimit: number;
	remaining: number;
	usagePercentage: number;
	nextResetAt?: number;
	overageStatus?: string;
	overageLimit?: number;
	overageCap?: number;
	overageRate?: number;
	overageCharges?: number;
	currentOverages?: number;
}

interface ModelEntry {
	id: string;
	displayName: string;
	contextWindow?: number;
}

const COLORS = {
	text: "#e6e6e6",
	muted: "#8b8b8b",
	bad: "#ff8a8a",
	good: "#7ee0a2",
	warn: "#c9a227",
	panel: "#2a2a2e",
	border: "#3a3a40",
	highlight: "#3a3a5e",
} as const;

(() => {
	const sdk = globalThis.narrafork as PluginUiSdk | undefined;
	if (!sdk) return;
	const root = document.body;

	const style = (element: HTMLElement, css: Partial<CSSStyleDeclaration>): void => {
		Object.assign(element.style, css);
	};

	function element<K extends keyof HTMLElementTagNameMap>(
		tag: K,
		css: Partial<CSSStyleDeclaration> = {},
		text?: string,
	): HTMLElementTagNameMap[K] {
		const node = document.createElement(tag);
		if (text !== undefined) node.textContent = text;
		style(node, css);
		return node;
	}

	function button(
		label: string,
		onClick: () => void,
		tone: "normal" | "bad" = "normal",
	): HTMLButtonElement {
		const node = element("button", {
			padding: "4px 10px",
			font: "12px system-ui, sans-serif",
			color: tone === "bad" ? COLORS.bad : COLORS.text,
			background: COLORS.panel,
			border: `1px solid ${COLORS.border}`,
			borderRadius: "5px",
			cursor: "pointer",
			marginRight: "6px",
		});
		node.textContent = label;
		node.addEventListener("click", onClick);
		return node;
	}

	function isRecord(value: unknown): value is Record<string, unknown> {
		return typeof value === "object" && value !== null && !Array.isArray(value);
	}

	// Tracks in-flight operations to disable buttons and prevent double-submits.
	let busy = false;

	/**
	 * Run a backend command.
	 *
	 * The host wraps a successful result as `{ status, output }`; unwrap it so callers see the
	 * command's own payload.
	 */
	async function command(commandId: string, input?: Record<string, unknown>): Promise<unknown> {
		const raw = await sdk.request("commands.execute", {
			commandId,
			...(input === undefined ? {} : { input }),
		});
		if (isRecord(raw) && "output" in raw) return raw.output;
		return raw;
	}

	const header = element("div", {
		font: "600 14px/1.9 system-ui, sans-serif",
		color: COLORS.text,
	});
	header.textContent = "Kiro credentials";
	root.appendChild(header);

	const summary = element("div", { font: "13px/1.7 system-ui, sans-serif", color: COLORS.muted });
	root.appendChild(summary);

	const toolbar = element("div", { margin: "10px 0" });
	root.appendChild(toolbar);

	const list = element("div", {});
	root.appendChild(list);

	const modelsSection = element("div", { marginTop: "14px" });
	root.appendChild(modelsSection);

	const importArea = element("div", { marginTop: "14px" });
	root.appendChild(importArea);

	const status = element("div", {
		font: "12px/1.7 system-ui, sans-serif",
		color: COLORS.muted,
		marginTop: "10px",
		minHeight: "18px",
	});
	root.appendChild(status);

	function setStatus(message: string, tone: "normal" | "bad" | "good" = "normal"): void {
		status.textContent = message;
		status.style.color = tone === "bad" ? COLORS.bad : tone === "good" ? COLORS.good : COLORS.muted;
	}

	/** Disable all buttons during an operation, re-enable when done. */
	function setBusy(isBusy: boolean): void {
		busy = isBusy;
		for (const btn of root.querySelectorAll("button")) {
			(btn as HTMLButtonElement).disabled = isBusy;
			(btn as HTMLButtonElement).style.opacity = isBusy ? "0.5" : "1";
		}
	}

	/** Wrap an action so every failure surfaces in one place instead of vanishing. */
	function run(label: string, action: () => Promise<unknown>): void {
		if (busy) return;
		setBusy(true);
		setStatus(`${label}…`);
		action()
			.then(() => {
				setStatus(`${label} done.`, "good");
				return refresh();
			})
			.catch((error: unknown) => {
				setStatus(`${label} failed: ${String(error)}`, "bad");
			})
			.finally(() => setBusy(false));
	}

	function tierLabel(tier?: string): string {
		if (!tier) return "";
		if (tier === "enterprise") return "Enterprise";
		if (tier === "paid") return "Paid";
		return "Free";
	}

	function formatTimestamp(iso?: string | number): string {
		if (!iso) return "";
		const date = new Date(
			typeof iso === "number" ? (iso < 10_000_000_000 ? iso * 1000 : iso) : iso,
		);
		if (Number.isNaN(date.getTime())) return String(iso);
		return date.toLocaleString();
	}

	function renderUsageInline(container: HTMLElement, credId: string): void {
		const usageBox = element("div", {
			marginTop: "6px",
			padding: "6px 10px",
			background: COLORS.panel,
			borderRadius: "5px",
			font: "12px/1.6 system-ui, sans-serif",
			color: COLORS.muted,
		});
		usageBox.textContent = "Loading usage…";
		container.appendChild(usageBox);

		command("credentials.usage", { credentialId: credId })
			.then((result) => {
				const usage = isRecord(result) ? (result.usage as UsageData | undefined) : undefined;
				if (!usage || !isRecord(usage)) {
					usageBox.textContent = "No usage data available.";
					return;
				}
				usageBox.replaceChildren();
				const lines: string[] = [];
				if (usage.subscriptionTitle) lines.push(`Plan: ${usage.subscriptionTitle}`);
				lines.push(
					`Usage: ${usage.currentUsage} / ${usage.usageLimit} (${usage.usagePercentage}%)`,
				);
				lines.push(`Remaining: ${usage.remaining}`);
				if (usage.nextResetAt) lines.push(`Resets: ${formatTimestamp(usage.nextResetAt)}`);
				if (usage.overageStatus) {
					lines.push(
						`Overage: ${usage.overageStatus}${usage.currentOverages ? ` ($${(usage.currentOverages / 100).toFixed(2)})` : ""}`,
					);
				}
				for (const line of lines) {
					const p = element("div", {});
					p.textContent = line;
					usageBox.appendChild(p);
				}
			})
			.catch((error: unknown) => {
				usageBox.textContent = `Usage failed: ${String(error)}`;
				usageBox.style.color = COLORS.bad;
			});
	}

	function renderEditForm(container: HTMLElement, entry: CredentialEntry): void {
		const form = element("div", {
			marginTop: "8px",
			padding: "8px 10px",
			background: COLORS.panel,
			borderRadius: "5px",
			border: `1px solid ${COLORS.border}`,
		});

		const formTitle = element("div", {
			font: "600 12px/1.6 system-ui, sans-serif",
			color: COLORS.text,
			marginBottom: "6px",
		});
		formTitle.textContent = "Edit credential";
		form.appendChild(formTitle);

		const hint = element("div", {
			font: "11px/1.5 system-ui, sans-serif",
			color: COLORS.muted,
			marginBottom: "8px",
		});
		hint.textContent =
			"Secret fields: leave blank to keep existing value. Non-secret fields: blank clears.";
		form.appendChild(hint);

		// Text fields
		const textFields: Array<{ key: string; label: string; current?: string }> = [
			{ key: "displayName", label: "Display name", current: entry.displayName },
			{ key: "email", label: "Email", current: entry.email },
			{ key: "region", label: "Region", current: entry.region },
			{ key: "authRegion", label: "Auth region", current: entry.authRegion },
			{ key: "apiRegion", label: "API region", current: entry.apiRegion },
			{ key: "profileArn", label: "Profile ARN", current: entry.profileArn },
			{ key: "subscriptionTitle", label: "Subscription title", current: entry.subscriptionTitle },
			{ key: "startUrl", label: "Start URL", current: entry.startUrl },
			{ key: "machineId", label: "Machine ID", current: entry.machineId },
			{ key: "proxyUrl", label: "Proxy URL", current: entry.proxyUrl },
			{ key: "proxyUsername", label: "Proxy username", current: entry.proxyUsername },
		];

		// Secret fields — never show current value
		const secretFields: Array<{ key: string; label: string; hasValue?: boolean }> = [
			{ key: "refreshToken", label: "Refresh token", hasValue: !entry.isApiKey },
			{ key: "apiKey", label: "API key (ksk_…)", hasValue: entry.isApiKey },
			{ key: "clientId", label: "Client ID", hasValue: entry.hasClientId },
			{ key: "clientSecret", label: "Client secret", hasValue: entry.hasClientSecret },
			{ key: "proxyPassword", label: "Proxy password", hasValue: entry.hasProxyPassword },
		];

		const inputs: Record<string, HTMLInputElement> = {};

		for (const { key, label, current } of textFields) {
			const row = element("div", { marginBottom: "4px" });
			const lbl = element("label", {
				font: "11px system-ui, sans-serif",
				color: COLORS.muted,
				display: "inline-block",
				width: "120px",
			});
			lbl.textContent = label;
			row.appendChild(lbl);
			const inp = element("input", {
				padding: "2px 6px",
				font: "12px ui-monospace, monospace",
				color: COLORS.text,
				background: "#1a1a1e",
				border: `1px solid ${COLORS.border}`,
				borderRadius: "3px",
				width: "220px",
			}) as HTMLInputElement;
			inp.value = current ?? "";
			inputs[key] = inp;
			row.appendChild(inp);
			form.appendChild(row);
		}

		// Auth method select
		const authRow = element("div", { marginBottom: "4px" });
		const authLbl = element("label", {
			font: "11px system-ui, sans-serif",
			color: COLORS.muted,
			display: "inline-block",
			width: "120px",
		});
		authLbl.textContent = "Auth method";
		authRow.appendChild(authLbl);
		const authSelect = element("select", {
			padding: "2px 6px",
			font: "12px system-ui, sans-serif",
			color: COLORS.text,
			background: "#1a1a1e",
			border: `1px solid ${COLORS.border}`,
			borderRadius: "3px",
		}) as HTMLSelectElement;
		for (const opt of ["", "social", "idc", "api_key"]) {
			const o = document.createElement("option");
			o.value = opt;
			o.textContent = opt || "(keep current)";
			if (opt === entry.authMethod) o.selected = true;
			authSelect.appendChild(o);
		}
		authRow.appendChild(authSelect);
		form.appendChild(authRow);

		// Secret fields
		const secretHeader = element("div", {
			font: "11px system-ui, sans-serif",
			color: COLORS.warn,
			marginTop: "8px",
			marginBottom: "4px",
		});
		secretHeader.textContent = "Secrets (leave blank to keep existing)";
		form.appendChild(secretHeader);

		for (const { key, label, hasValue } of secretFields) {
			const row = element("div", { marginBottom: "4px" });
			const lbl = element("label", {
				font: "11px system-ui, sans-serif",
				color: COLORS.muted,
				display: "inline-block",
				width: "120px",
			});
			lbl.textContent = `${label}${hasValue ? " ●" : ""}`;
			row.appendChild(lbl);
			const inp = element("input", {
				padding: "2px 6px",
				font: "12px ui-monospace, monospace",
				color: COLORS.text,
				background: "#1a1a1e",
				border: `1px solid ${COLORS.border}`,
				borderRadius: "3px",
				width: "220px",
			}) as HTMLInputElement;
			inp.type = "password";
			inp.placeholder = hasValue ? "(stored)" : "(none)";
			inputs[key] = inp;
			row.appendChild(inp);
			form.appendChild(row);
		}

		const btnRow = element("div", { marginTop: "8px" });
		btnRow.appendChild(
			button("Save", () => {
				const fields: Record<string, string> = {};
				for (const { key } of textFields) {
					// Send all text field values — empty means clear
					const val = inputs[key].value;
					if (val !== (entry[key as keyof CredentialEntry] ?? "")) {
						fields[key] = val;
					}
				}
				// Secret fields: only send non-empty (non-empty means replace)
				for (const { key } of secretFields) {
					const val = inputs[key].value;
					if (val.trim()) fields[key] = val;
				}
				// Auth method
				if (authSelect.value && authSelect.value !== entry.authMethod) {
					fields.authMethod = authSelect.value;
				}
				if (Object.keys(fields).length === 0) {
					setStatus("No changes to save.", "normal");
					return;
				}
				run("Update credential", () =>
					command("credentials.update", { credentialId: entry.id, fields }),
				);
			}),
		);
		btnRow.appendChild(
			button("Cancel", () => {
				form.remove();
			}),
		);
		form.appendChild(btnRow);
		container.appendChild(form);
	}

	function renderEntry(entry: CredentialEntry, currentId?: string): HTMLElement {
		const isCurrent = entry.id === currentId;
		const row = element("div", {
			border: `1px solid ${isCurrent ? COLORS.highlight : COLORS.border}`,
			borderRadius: "6px",
			padding: "8px 10px",
			marginBottom: "8px",
			background: isCurrent ? "#282838" : "#232327",
		});

		const title = element("div", {
			font: "600 13px/1.6 system-ui, sans-serif",
			color: entry.disabled ? COLORS.warn : COLORS.text,
		});
		const name = entry.displayName || entry.email || entry.id;
		const currentMark = isCurrent ? " ◆" : "";
		const tierStr = entry.tier ? ` [${tierLabel(entry.tier)}]` : "";
		title.textContent = `${entry.disabled ? "○" : "●"}  ${name}${tierStr}${currentMark}`;
		row.appendChild(title);

		// Identity and state only — never a token.
		const facts = [
			`id ${entry.id}`,
			`priority ${entry.priority}`,
			entry.authMethod ? `auth ${entry.authMethod}` : undefined,
			entry.isApiKey ? "API key (never expires)" : undefined,
			entry.disabled
				? `disabled${entry.disabledReason ? ` (${entry.disabledReason})` : ""}`
				: undefined,
			entry.failureCount ? `failures: ${entry.failureCount}` : undefined,
			entry.successCount ? `successes: ${entry.successCount}` : undefined,
			entry.lastUsedAt ? `last used ${formatTimestamp(entry.lastUsedAt)}` : undefined,
			entry.meterUsage !== undefined ? `${entry.meterUsage} credits used` : undefined,
			entry.hasProfileArn ? "profile ARN set" : undefined,
			entry.region ? `region ${entry.region}` : undefined,
			entry.expiresAt ? `expires ${formatTimestamp(entry.expiresAt)}` : undefined,
			entry.availableModels?.length ? `${entry.availableModels.length} models` : undefined,
		].filter((value): value is string => Boolean(value));
		row.appendChild(
			element(
				"div",
				{ font: "12px/1.7 ui-monospace, monospace", color: COLORS.muted },
				facts.join("  ·  "),
			),
		);

		const actions = element("div", { marginTop: "6px" });
		actions.appendChild(
			button(entry.disabled ? "Enable" : "Disable", () =>
				run(entry.disabled ? "Enable" : "Disable", () =>
					command("credentials.setState", {
						credentialId: entry.id,
						state: entry.disabled ? "enabled" : "disabled",
					}),
				),
			),
		);
		actions.appendChild(
			button("Reset", () =>
				run("Reset", () =>
					command("credentials.setState", { credentialId: entry.id, state: "reset" }),
				),
			),
		);
		actions.appendChild(
			button("Refresh token", () =>
				run("Refresh", () => command("credentials.refresh", { credentialId: entry.id })),
			),
		);
		actions.appendChild(
			button("Usage", () => {
				// Toggle: remove existing usage box or show one
				const existing = row.querySelector("[data-usage]");
				if (existing) {
					existing.remove();
					return;
				}
				const box = element("div", {});
				box.setAttribute("data-usage", "1");
				renderUsageInline(box, entry.id);
				row.appendChild(box);
			}),
		);
		actions.appendChild(
			button("Edit", () => {
				const existing = row.querySelector("[data-edit-form]");
				if (existing) {
					existing.remove();
					return;
				}
				const wrap = element("div", {});
				wrap.setAttribute("data-edit-form", "1");
				renderEditForm(wrap, entry);
				row.appendChild(wrap);
			}),
		);
		actions.appendChild(
			button("Models", () => {
				const existing = row.querySelector("[data-models]");
				if (existing) {
					existing.remove();
					return;
				}
				const box = element("div", {
					marginTop: "6px",
					padding: "6px 10px",
					background: COLORS.panel,
					borderRadius: "5px",
					font: "12px/1.6 ui-monospace, monospace",
					color: COLORS.muted,
				});
				box.setAttribute("data-models", "1");
				box.textContent = "Fetching models…";
				row.appendChild(box);

				command("models.list", { credentialId: entry.id })
					.then((result) => {
						if (!isRecord(result) || !Array.isArray(result.models)) {
							box.textContent = "No model data.";
							return;
						}
						box.replaceChildren();
						const models = result.models as ModelEntry[];
						if (models.length === 0) {
							box.textContent = "No models available.";
							return;
						}
						const heading = element("div", {
							font: "600 12px/1.6 system-ui, sans-serif",
							color: COLORS.text,
							marginBottom: "4px",
						});
						heading.textContent = `${models.length} models`;
						box.appendChild(heading);
						for (const m of models) {
							const mRow = element("div", { paddingLeft: "8px" });
							const ctx = m.contextWindow ? ` (${Math.round(m.contextWindow / 1024)}k ctx)` : "";
							mRow.textContent = `${m.id}${ctx}`;
							box.appendChild(mRow);
						}
						// Refresh button inside model list
						const refreshBtn = button("Refresh models", () =>
							run("Refresh models", () => command("models.refresh", { credentialId: entry.id })),
						);
						style(refreshBtn, { marginTop: "6px" });
						box.appendChild(refreshBtn);
					})
					.catch((error: unknown) => {
						box.textContent = `Models failed: ${String(error)}`;
						box.style.color = COLORS.bad;
					});
			}),
		);

		const priority = element("input", {
			width: "56px",
			marginRight: "6px",
			padding: "3px 6px",
			font: "12px system-ui, sans-serif",
			color: COLORS.text,
			background: COLORS.panel,
			border: `1px solid ${COLORS.border}`,
			borderRadius: "5px",
		}) as HTMLInputElement;
		priority.type = "number";
		priority.value = String(entry.priority);
		priority.title = "Lower runs first";
		actions.appendChild(priority);
		actions.appendChild(
			button("Set priority", () =>
				run("Set priority", () =>
					command("credentials.setPriority", {
						credentialId: entry.id,
						priority: Number(priority.value),
					}),
				),
			),
		);
		actions.appendChild(
			button(
				"Remove",
				() => {
					// Irreversible for this credential, so require a deliberate second click.
					if (!window.confirm(`Remove credential ${name}?`)) return;
					run("Remove", () => command("credentials.remove", { credentialId: entry.id }));
				},
				"bad",
			),
		);
		row.appendChild(actions);
		return row;
	}

	function renderToolbar(current: string): void {
		toolbar.replaceChildren();
		for (const mode of ["priority", "balanced"]) {
			toolbar.appendChild(
				button(`${mode === current ? "✓ " : ""}${mode}`, () =>
					run(`Switch to ${mode}`, () => command("config.setLoadBalancing", { mode })),
				),
			);
		}
		toolbar.appendChild(
			button("Reload", () => {
				setStatus("Reloading…");
				setBusy(true);
				refresh()
					.then(() => setStatus("Reloaded.", "good"))
					.catch((error: unknown) => setStatus(`Reload failed: ${String(error)}`, "bad"))
					.finally(() => setBusy(false));
			}),
		);
		toolbar.appendChild(
			button("Refresh all models", () =>
				run("Refresh all models", async () => {
					// Refresh each credential's model cache sequentially
					const output = (await command("status")) as StatusOutput | undefined;
					if (!output?.entries?.length) return;
					for (const entry of output.entries) {
						if (!entry.disabled) {
							await command("models.refresh", { credentialId: entry.id });
						}
					}
				}),
			),
		);
	}

	/**
	 * Parse plain-text `ksk_` API key input into an importable credential object.
	 * Supports one key per line.
	 */
	function parseKskPlaintext(text: string): Array<Record<string, string>> {
		const results: Array<Record<string, string>> = [];
		for (const line of text.split("\n")) {
			const trimmed = line.trim();
			if (trimmed.startsWith("ksk_")) {
				results.push({ authMethod: "api_key", apiKey: trimmed });
			}
		}
		return results;
	}

	function renderImport(): void {
		importArea.replaceChildren();
		importArea.appendChild(
			element(
				"div",
				{ font: "600 13px/1.8 system-ui, sans-serif", color: COLORS.text },
				"Import credentials",
			),
		);
		importArea.appendChild(
			element(
				"div",
				{ font: "12px/1.6 system-ui, sans-serif", color: COLORS.muted },
				"Paste a Kiro credentials export (JSON) or ksk_ API keys (one per line). Values are sent to the plugin backend and stored by the host; they are never displayed back here.",
			),
		);
		const box = element("textarea", {
			width: "100%",
			minHeight: "84px",
			marginTop: "6px",
			padding: "6px",
			font: "12px ui-monospace, monospace",
			color: COLORS.text,
			background: COLORS.panel,
			border: `1px solid ${COLORS.border}`,
			borderRadius: "5px",
			boxSizing: "border-box",
		}) as HTMLTextAreaElement;
		box.placeholder = "JSON array, object, or ksk_ keys (one per line)";
		importArea.appendChild(box);

		const submit = button("Import", () => {
			const raw = box.value.trim();
			if (raw.length === 0) {
				setStatus("Nothing to import.", "bad");
				return;
			}
			let parsed: unknown;
			// Try JSON first; fall back to plain-text ksk_ lines
			try {
				parsed = JSON.parse(raw);
			} catch {
				const kskEntries = parseKskPlaintext(raw);
				if (kskEntries.length > 0) {
					parsed = kskEntries;
				} else {
					setStatus("Import payload is not valid JSON and contains no ksk_ keys.", "bad");
					return;
				}
			}
			// Cleared immediately: the textarea is the only place a secret exists in this
			// document, and it should not outlive the request.
			box.value = "";
			run("Import", () =>
				command("credentials.import", { credentials: parsed }).then((result) => {
					if (isRecord(result)) {
						const added = result.added ?? 0;
						const dupes = result.duplicates ?? 0;
						setStatus(`Import done: ${added} added, ${dupes} duplicates.`, "good");
					}
				}),
			);
		});
		style(submit, { marginTop: "6px" });
		importArea.appendChild(submit);
	}

	async function refresh(): Promise<void> {
		const output = (await command("status")) as StatusOutput | undefined;
		if (!output || !Array.isArray(output.entries)) {
			summary.textContent = "No credential information available.";
			return;
		}
		summary.textContent = `${output.availableCount} of ${output.credentialCount} credentials available  ·  load balancing: ${output.loadBalancingMode}`;
		summary.style.color = output.availableCount > 0 ? COLORS.text : COLORS.warn;

		list.replaceChildren();
		if (output.entries.length === 0) {
			list.appendChild(
				element(
					"div",
					{ font: "13px/1.7 system-ui, sans-serif", color: COLORS.warn },
					"No credentials configured yet. Import one below.",
				),
			);
		}

		// Group by tier for visual separation
		const tiers = ["enterprise", "paid", "free", ""] as const;
		const grouped = new Map<string, CredentialEntry[]>();
		for (const entry of output.entries) {
			const tier = entry.tier || "";
			if (!grouped.has(tier)) grouped.set(tier, []);
			grouped.get(tier)?.push(entry);
		}

		for (const tier of tiers) {
			const entries = grouped.get(tier);
			if (!entries?.length) continue;
			if (output.entries.length > 1 && tier) {
				const tierHeader = element("div", {
					font: "600 12px/2 system-ui, sans-serif",
					color: COLORS.muted,
					textTransform: "uppercase",
					letterSpacing: "0.5px",
				});
				tierHeader.textContent = tierLabel(tier);
				list.appendChild(tierHeader);
			}
			for (const entry of entries) {
				list.appendChild(renderEntry(entry, output.currentId));
			}
		}
		renderToolbar(output.loadBalancingMode);

		// Lets the host and the e2e test observe that the view reached a usable state.
		sdk.notify("kiro-external.settings.ready", {
			credentialCount: output.credentialCount,
			availableCount: output.availableCount,
		});
	}

	renderImport();
	refresh().catch((error: unknown) => {
		summary.textContent = `Failed to load credentials: ${String(error)}`;
		summary.style.color = COLORS.bad;
	});
})();
