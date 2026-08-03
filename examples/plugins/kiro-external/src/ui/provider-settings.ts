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
	expiresAt?: string;
	authMethod?: string;
	meterUsage?: number;
}

interface StatusOutput {
	credentialCount: number;
	availableCount: number;
	loadBalancingMode: string;
	entries: CredentialEntry[];
}

const COLORS = {
	text: "#e6e6e6",
	muted: "#8b8b8b",
	bad: "#ff8a8a",
	good: "#7ee0a2",
	warn: "#c9a227",
	panel: "#2a2a2e",
	border: "#3a3a40",
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

	function button(label: string, onClick: () => void, tone: "normal" | "bad" = "normal") {
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

	/** Wrap an action so every failure surfaces in one place instead of vanishing. */
	function run(label: string, action: () => Promise<unknown>): void {
		setStatus(`${label}…`);
		action()
			.then(() => {
				setStatus(`${label} done.`, "good");
				return refresh();
			})
			.catch((error: unknown) => {
				setStatus(`${label} failed: ${String(error)}`, "bad");
			});
	}

	function renderEntry(entry: CredentialEntry): HTMLElement {
		const row = element("div", {
			border: `1px solid ${COLORS.border}`,
			borderRadius: "6px",
			padding: "8px 10px",
			marginBottom: "8px",
			background: "#232327",
		});

		const title = element("div", {
			font: "600 13px/1.6 system-ui, sans-serif",
			color: entry.disabled ? COLORS.warn : COLORS.text,
		});
		const name = entry.displayName || entry.email || entry.id;
		title.textContent = `${entry.disabled ? "○" : "●"}  ${name}`;
		row.appendChild(title);

		// Identity and state only — never a token.
		const facts = [
			`id ${entry.id}`,
			`priority ${entry.priority}`,
			entry.authMethod ? `auth ${entry.authMethod}` : undefined,
			entry.disabled
				? `disabled${entry.disabledReason ? ` (${entry.disabledReason})` : ""}`
				: undefined,
			entry.meterUsage !== undefined ? `${entry.meterUsage} credits used` : undefined,
			entry.hasProfileArn ? "profile ARN set" : undefined,
			entry.expiresAt ? `expires ${entry.expiresAt}` : undefined,
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
				setStatus("Fetching usage…");
				command("credentials.usage", { credentialId: entry.id })
					.then((result) => {
						const usage = isRecord(result) ? result.usage : undefined;
						setStatus(`Usage for ${entry.id}: ${JSON.stringify(usage)}`, "good");
					})
					.catch((error: unknown) => setStatus(`Usage failed: ${String(error)}`, "bad"));
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
				refresh()
					.then(() => setStatus("Reloaded.", "good"))
					.catch((error: unknown) => setStatus(`Reload failed: ${String(error)}`, "bad"));
			}),
		);
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
				"Paste a Kiro credentials export. Values are sent to the plugin backend and stored by the host; they are never displayed back here.",
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
		box.placeholder = '[{ "authMethod": "social", "refreshToken": "…" }]';
		importArea.appendChild(box);

		const submit = button("Import", () => {
			const raw = box.value.trim();
			if (raw.length === 0) {
				setStatus("Nothing to import.", "bad");
				return;
			}
			let parsed: unknown;
			try {
				parsed = JSON.parse(raw);
			} catch {
				setStatus("Import payload is not valid JSON.", "bad");
				return;
			}
			// Cleared immediately: the textarea is the only place a secret exists in this
			// document, and it should not outlive the request.
			box.value = "";
			run("Import", () => command("credentials.import", { credentials: parsed }));
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
		for (const entry of output.entries) list.appendChild(renderEntry(entry));
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
