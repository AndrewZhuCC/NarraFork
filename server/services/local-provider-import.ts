/**
 * Find provider settings that other AI tools on this machine already hold, so a
 * first-time admin can import them instead of re-entering keys.
 *
 * Supported tools and the files read (all small, user-owned config files):
 *  - Claude Code:      `$CLAUDE_CONFIG_DIR` or `~/.claude` — `settings.json` /
 *                      `settings.local.json` `env`, plus `primaryApiKey` in `.claude.json`
 *  - Codex:            `$CODEX_HOME` or `~/.codex` — `config.toml` `model_providers`,
 *                      `auth.json` `OPENAI_API_KEY`
 *  - OpenCode:         `$XDG_CONFIG_HOME/opencode` (`~/.config/opencode`) `opencode.json[c]`,
 *                      `$XDG_DATA_HOME/opencode` (`~/.local/share/opencode`) `auth.json`
 *  - DeepSeek Harness: `$DSH_HOME` or `~/.dsh` — `profiles/<profile>/cordis[.patch].yml`,
 *                      legacy `settings.yaml`, keys in `.credentials.yaml`
 *
 * Only API-key style credentials are imported. Subscription logins (Claude
 * `.credentials.json`, ChatGPT tokens in Codex `auth.json`, OAuth entries in
 * OpenCode) are reported as notes and never copied: they belong to that tool's
 * own login flow, and sharing a rotating refresh token would sign the tool out.
 *
 * Nothing here runs unless an admin explicitly asks for a scan. Reads are async,
 * size-capped and bounded in count, so a scan cannot stall the main thread.
 */
import { createHash } from "node:crypto";
import { readdir, readFile, stat } from "node:fs/promises";
import { isAbsolute, join } from "node:path";
import type {
	LocalImportNote,
	LocalImportProtocol,
	LocalImportSource,
	LocalProviderScanResponse,
} from "@shared/local-provider-import";
import { maskSecret } from "../lib/auth-settings";
import { getHome } from "../lib/platform";

/** Config files are tiny; anything larger is not a config file we understand. */
const MAX_CONFIG_BYTES = 1024 * 1024;
const MAX_PROFILES = 32;
const MAX_YAML_SEARCH_DEPTH = 8;

export interface LocalProviderCandidate {
	id: string;
	source: LocalImportSource;
	name: string;
	protocol: LocalImportProtocol;
	baseUrl: string;
	/** Raw key. Never sent to the browser except by an explicit resolve request. */
	apiKey: string;
	configPath: string;
	model?: string;
}

export interface LocalProviderScan {
	candidates: LocalProviderCandidate[];
	notes: LocalImportNote[];
	sources: Array<{ source: LocalImportSource; found: boolean }>;
}

export interface LocalScanContext {
	home: string;
	env: Readonly<Record<string, string | undefined>>;
	/** File contents, or null when missing, unreadable or over the size cap. */
	readText(path: string): Promise<string | null>;
	/** Subdirectory names, or [] when missing. */
	listDirs(path: string): Promise<string[]>;
	exists(path: string): Promise<boolean>;
}

interface SourceScan {
	found: boolean;
	candidates: LocalProviderCandidate[];
	notes: LocalImportNote[];
}

type Json = Record<string, unknown>;

function isRecord(value: unknown): value is Json {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function str(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function candidateId(source: LocalImportSource, key: string, baseUrl: string): string {
	const digest = createHash("sha256").update(`${source}\n${key}\n${baseUrl}`).digest("hex");
	return `${source}:${digest.slice(0, 16)}`;
}

function trimSlashes(url: string): string {
	return url.trim().replace(/\/+$/, "");
}

/** NarraFork's Anthropic transport appends `/messages` to a base that includes `/v1`. */
export function anthropicBaseWithVersion(url: string): string {
	const base = trimSlashes(url);
	return /\/v\d+$/.test(base) ? base : `${base}/v1`;
}

function isHttpUrl(url: string): boolean {
	try {
		const parsed = new URL(url);
		return (parsed.protocol === "http:" || parsed.protocol === "https:") && !parsed.username;
	} catch {
		return false;
	}
}

function hostLabel(url: string): string {
	try {
		return new URL(url).host;
	} catch {
		return url;
	}
}

function makeCandidate(
	source: LocalImportSource,
	key: string,
	fields: Omit<LocalProviderCandidate, "id" | "source">,
): LocalProviderCandidate | null {
	const baseUrl = trimSlashes(fields.baseUrl);
	if (!isHttpUrl(baseUrl)) return null;
	return { ...fields, baseUrl, source, id: candidateId(source, key, baseUrl) };
}

/** Strip `//` and block comments plus trailing commas, leaving string contents intact. */
export function parseJsonc(text: string): unknown {
	let out = "";
	let inString = false;
	for (let i = 0; i < text.length; i++) {
		const ch = text[i];
		if (inString) {
			out += ch;
			if (ch === "\\") {
				out += text[i + 1] ?? "";
				i++;
			} else if (ch === '"') {
				inString = false;
			}
			continue;
		}
		if (ch === '"') {
			inString = true;
			out += ch;
		} else if (ch === "/" && text[i + 1] === "/") {
			while (i < text.length && text[i] !== "\n") i++;
			out += "\n";
		} else if (ch === "/" && text[i + 1] === "*") {
			i += 2;
			while (i < text.length && !(text[i] === "*" && text[i + 1] === "/")) i++;
			i++;
		} else {
			out += ch;
		}
	}
	return JSON.parse(out.replace(/,(\s*[}\]])/g, "$1"));
}

async function readJson(ctx: LocalScanContext, path: string, jsonc = false): Promise<Json | null> {
	const text = await ctx.readText(path);
	if (text === null) return null;
	try {
		const parsed = jsonc ? parseJsonc(text) : JSON.parse(text);
		return isRecord(parsed) ? parsed : null;
	} catch {
		return null;
	}
}

async function readToml(ctx: LocalScanContext, path: string): Promise<Json | null> {
	const text = await ctx.readText(path);
	if (text === null) return null;
	try {
		const parsed = Bun.TOML.parse(text);
		return isRecord(parsed) ? parsed : null;
	} catch {
		return null;
	}
}

async function readYaml(ctx: LocalScanContext, path: string): Promise<unknown> {
	const text = await ctx.readText(path);
	if (text === null) return null;
	try {
		return Bun.YAML.parse(text);
	} catch {
		return null;
	}
}

// ── Claude Code ─────────────────────────────────────────────────────────────

const ANTHROPIC_OFFICIAL_BASE = "https://api.anthropic.com";

export async function scanClaude(ctx: LocalScanContext): Promise<SourceScan> {
	const configDir = str(ctx.env.CLAUDE_CONFIG_DIR) ?? join(ctx.home, ".claude");
	const notes: LocalImportNote[] = [];
	const candidates: LocalProviderCandidate[] = [];
	const found = await ctx.exists(configDir);

	// settings.local.json overrides settings.json, matching Claude Code's precedence.
	const env: Record<string, string> = {};
	let envPath = "";
	for (const file of ["settings.json", "settings.local.json"]) {
		const path = join(configDir, file);
		const settings = await readJson(ctx, path);
		if (!isRecord(settings?.env)) continue;
		for (const [key, value] of Object.entries(settings.env)) {
			const text = str(value);
			if (text && key.startsWith("ANTHROPIC_")) {
				env[key] = text;
				envPath = path;
			}
		}
	}

	const envKey = env.ANTHROPIC_AUTH_TOKEN ?? env.ANTHROPIC_API_KEY;
	if (envKey) {
		const base = env.ANTHROPIC_BASE_URL ?? ANTHROPIC_OFFICIAL_BASE;
		const candidate = makeCandidate("claude", "settings-env", {
			name:
				trimSlashes(base) === ANTHROPIC_OFFICIAL_BASE
					? "Anthropic"
					: `Claude Code (${hostLabel(base)})`,
			protocol: "anthropic-messages",
			baseUrl: anthropicBaseWithVersion(base),
			apiKey: envKey,
			configPath: envPath,
			...(env.ANTHROPIC_MODEL ? { model: env.ANTHROPIC_MODEL } : {}),
		});
		if (candidate) candidates.push(candidate);
	}

	// An API key saved by `claude` itself lives in .claude.json (beside the config
	// dir by default, inside it when CLAUDE_CONFIG_DIR is set).
	const stateFile = str(ctx.env.CLAUDE_CONFIG_DIR)
		? join(configDir, ".claude.json")
		: join(ctx.home, ".claude.json");
	const state = await readJson(ctx, stateFile);
	const primaryApiKey = str(state?.primaryApiKey);
	if (primaryApiKey && primaryApiKey !== envKey) {
		const candidate = makeCandidate("claude", "primary-api-key", {
			name: "Anthropic",
			protocol: "anthropic-messages",
			baseUrl: anthropicBaseWithVersion(ANTHROPIC_OFFICIAL_BASE),
			apiKey: primaryApiKey,
			configPath: stateFile,
		});
		if (candidate) candidates.push(candidate);
	}

	if (await ctx.exists(join(configDir, ".credentials.json"))) {
		notes.push({ source: "claude", code: "subscription-login-skipped" });
	}
	return { found: found || state !== null, candidates, notes };
}

// ── Codex ───────────────────────────────────────────────────────────────────

const OPENAI_OFFICIAL_BASE = "https://api.openai.com/v1";

export async function scanCodex(ctx: LocalScanContext): Promise<SourceScan> {
	const codexHome = str(ctx.env.CODEX_HOME) ?? join(ctx.home, ".codex");
	const configPath = join(codexHome, "config.toml");
	const authPath = join(codexHome, "auth.json");
	const notes: LocalImportNote[] = [];
	const candidates: LocalProviderCandidate[] = [];
	const found = await ctx.exists(codexHome);

	const config = await readToml(ctx, configPath);
	const auth = await readJson(ctx, authPath);
	const openaiKey = str(auth?.OPENAI_API_KEY);
	const activeProvider = str(config?.model_provider);
	const activeModel = str(config?.model);
	let openaiKeyUsedByCustomProvider = false;

	const providers = isRecord(config?.model_providers) ? config.model_providers : {};
	for (const [providerId, raw] of Object.entries(providers)) {
		if (!isRecord(raw)) continue;
		const baseUrl = str(raw.base_url);
		if (!baseUrl) continue;
		const envKeyName = str(raw.env_key);
		const usesOpenaiAuth = raw.requires_openai_auth === true;
		const apiKey =
			str(raw.experimental_bearer_token) ??
			(envKeyName ? str(ctx.env[envKeyName]) : undefined) ??
			(usesOpenaiAuth ? openaiKey : undefined);
		if (!apiKey) continue;
		if (usesOpenaiAuth && apiKey === openaiKey) openaiKeyUsedByCustomProvider = true;
		const candidate = makeCandidate("codex", `provider:${providerId}`, {
			name: str(raw.name) ?? providerId,
			protocol: raw.wire_api === "chat" ? "completions-compatible" : "openai-responses",
			baseUrl,
			apiKey,
			configPath,
			...(activeModel && activeProvider === providerId ? { model: activeModel } : {}),
		});
		if (candidate) candidates.push(candidate);
	}

	// The key in auth.json is an OpenAI key unless a custom provider borrows it
	// (`requires_openai_auth`), which relays commonly do.
	if (openaiKey && !openaiKeyUsedByCustomProvider) {
		const candidate = makeCandidate("codex", "openai", {
			name: "OpenAI",
			protocol: "openai-responses",
			baseUrl: OPENAI_OFFICIAL_BASE,
			apiKey: openaiKey,
			configPath: authPath,
			...(activeModel && (!activeProvider || activeProvider === "openai")
				? { model: activeModel }
				: {}),
		});
		if (candidate) candidates.push(candidate);
	}

	if (isRecord(auth?.tokens)) {
		notes.push({ source: "codex", code: "subscription-login-skipped" });
	}
	return { found, candidates, notes };
}

// ── OpenCode ────────────────────────────────────────────────────────────────

interface KnownProvider {
	name: string;
	protocol: LocalImportProtocol;
	baseUrl: string;
}

/** Built-in OpenCode provider ids that need no `baseURL` in its config. */
const OPENCODE_KNOWN_PROVIDERS: Record<string, KnownProvider> = {
	anthropic: {
		name: "Anthropic",
		protocol: "anthropic-messages",
		baseUrl: "https://api.anthropic.com/v1",
	},
	openai: { name: "OpenAI", protocol: "openai-responses", baseUrl: OPENAI_OFFICIAL_BASE },
	deepseek: {
		name: "DeepSeek",
		protocol: "completions-compatible",
		baseUrl: "https://api.deepseek.com/v1",
	},
	openrouter: {
		name: "OpenRouter",
		protocol: "completions-compatible",
		baseUrl: "https://openrouter.ai/api/v1",
	},
	google: {
		name: "Gemini",
		protocol: "gemini-compatible",
		baseUrl: "https://generativelanguage.googleapis.com/v1beta",
	},
	zhipuai: {
		name: "Zhipu AI",
		protocol: "completions-compatible",
		baseUrl: "https://open.bigmodel.cn/api/paas/v4",
	},
	zai: {
		name: "Z.ai",
		protocol: "completions-compatible",
		baseUrl: "https://api.z.ai/api/paas/v4",
	},
	moonshotai: {
		name: "Moonshot AI",
		protocol: "completions-compatible",
		baseUrl: "https://api.moonshot.ai/v1",
	},
	"moonshotai-cn": {
		name: "Moonshot AI",
		protocol: "completions-compatible",
		baseUrl: "https://api.moonshot.cn/v1",
	},
	xai: { name: "xAI", protocol: "completions-compatible", baseUrl: "https://api.x.ai/v1" },
	groq: {
		name: "Groq",
		protocol: "completions-compatible",
		baseUrl: "https://api.groq.com/openai/v1",
	},
	opencode: {
		name: "OpenCode Zen",
		protocol: "completions-compatible",
		baseUrl: "https://opencode.ai/zen/v1",
	},
};

function protocolFromNpm(npm: string | undefined): LocalImportProtocol | undefined {
	switch (npm) {
		case "@ai-sdk/anthropic":
			return "anthropic-messages";
		case "@ai-sdk/openai":
			return "openai-responses";
		case "@ai-sdk/google":
			return "gemini-compatible";
		case "@ai-sdk/openai-compatible":
			return "completions-compatible";
		default:
			return undefined;
	}
}

function expandHome(path: string, home: string): string {
	return path.startsWith("~/") ? join(home, path.slice(2)) : path;
}

/** Resolve OpenCode's `{env:NAME}` / `{file:path}` substitutions in a key. */
async function resolveOpencodeValue(
	ctx: LocalScanContext,
	value: string,
	configDir: string,
): Promise<string | undefined> {
	const env = /^\{env:([^}]+)\}$/.exec(value);
	if (env?.[1]) return str(ctx.env[env[1].trim()]);
	const file = /^\{file:([^}]+)\}$/.exec(value);
	if (file?.[1]) {
		const raw = expandHome(file[1].trim(), ctx.home);
		const text = await ctx.readText(isAbsolute(raw) ? raw : join(configDir, raw));
		return text === null ? undefined : str(text);
	}
	return value.includes("{") ? undefined : value;
}

export async function scanOpencode(ctx: LocalScanContext): Promise<SourceScan> {
	const configDir = join(str(ctx.env.XDG_CONFIG_HOME) ?? join(ctx.home, ".config"), "opencode");
	const dataDir = join(str(ctx.env.XDG_DATA_HOME) ?? join(ctx.home, ".local", "share"), "opencode");
	const authPath = join(dataDir, "auth.json");
	const notes: LocalImportNote[] = [];
	const candidates: LocalProviderCandidate[] = [];
	const found = (await ctx.exists(configDir)) || (await ctx.exists(dataDir));

	const configFiles = [
		...(str(ctx.env.OPENCODE_CONFIG)
			? [expandHome(str(ctx.env.OPENCODE_CONFIG) ?? "", ctx.home)]
			: []),
		join(configDir, "opencode.json"),
		join(configDir, "opencode.jsonc"),
		join(configDir, "config.json"),
	];
	let config: Json | null = null;
	let configPath = "";
	for (const path of configFiles) {
		config = await readJson(ctx, path, true);
		if (config) {
			configPath = path;
			break;
		}
	}
	const auth = (await readJson(ctx, authPath)) ?? {};
	const configured = isRecord(config?.provider) ? config.provider : {};
	const providerIds = new Set([...Object.keys(configured), ...Object.keys(auth)]);
	let sawOauth = false;

	for (const providerId of providerIds) {
		const entry = isRecord(configured[providerId]) ? configured[providerId] : {};
		const options = isRecord(entry.options) ? entry.options : {};
		const stored = isRecord(auth[providerId]) ? auth[providerId] : undefined;
		if (stored?.type === "oauth") sawOauth = true;

		const optionKey = str(options.apiKey);
		const apiKey =
			(optionKey ? await resolveOpencodeValue(ctx, optionKey, configDir) : undefined) ??
			(stored?.type === "api" ? str(stored.key) : undefined);
		if (!apiKey) continue;

		const known = OPENCODE_KNOWN_PROVIDERS[providerId];
		const protocol = protocolFromNpm(str(entry.npm)) ?? known?.protocol ?? "completions-compatible";
		const rawBase = str(options.baseURL) ?? known?.baseUrl;
		if (!rawBase) continue;
		const candidate = makeCandidate("opencode", `provider:${providerId}`, {
			name: str(entry.name) ?? known?.name ?? providerId,
			protocol,
			baseUrl: rawBase,
			apiKey,
			configPath: optionKey ? configPath : authPath,
		});
		if (candidate) candidates.push(candidate);
	}

	if (sawOauth) notes.push({ source: "opencode", code: "subscription-login-skipped" });
	return { found, candidates, notes };
}

// ── DeepSeek Harness ────────────────────────────────────────────────────────

function protocolFromPiAi(api: string | undefined): LocalImportProtocol | undefined {
	switch (api) {
		case "openai-completions":
			return "completions-compatible";
		case "openai-responses":
			return "openai-responses";
		case "anthropic-messages":
			return "anthropic-messages";
		case "google-generative-ai":
			return "gemini-compatible";
		default:
			return undefined;
	}
}

/**
 * Collect `providers` maps (id → { baseURL, api, apiKeyEnv }) wherever they sit:
 * under the `llm-pi-ai` plugin's `config` in a profile's cordis file, or under a
 * top-level `llm-pi-ai` key in the legacy settings.yaml.
 */
export function collectHarnessProviders(node: unknown, depth = 0): Array<[string, Json]> {
	if (depth > MAX_YAML_SEARCH_DEPTH || !node || typeof node !== "object") return [];
	const out: Array<[string, Json]> = [];
	if (Array.isArray(node)) {
		for (const item of node) out.push(...collectHarnessProviders(item, depth + 1));
		return out;
	}
	for (const [key, value] of Object.entries(node)) {
		if (key === "providers" && isRecord(value)) {
			for (const [id, provider] of Object.entries(value)) {
				if (isRecord(provider) && str(provider.baseURL)) out.push([id, provider]);
			}
		} else {
			out.push(...collectHarnessProviders(value, depth + 1));
		}
	}
	return out;
}

export async function scanDeepseekHarness(ctx: LocalScanContext): Promise<SourceScan> {
	const dshHome = str(ctx.env.DSH_HOME) ?? join(ctx.home, ".dsh");
	const candidates: LocalProviderCandidate[] = [];
	const found = await ctx.exists(dshHome);
	if (!found) return { found, candidates, notes: [] };

	const credentialsDoc = await readYaml(ctx, join(dshHome, ".credentials.yaml"));
	const credentials = isRecord(credentialsDoc) ? credentialsDoc : {};
	// An exported variable wins over the stored credential, as in the harness.
	const credential = (name: string) => str(ctx.env[name]) ?? str(credentials[name]);

	const files = [join(dshHome, "settings.yaml"), join(dshHome, "settings.yaml.imported")];
	const profilesDir = join(dshHome, "profiles");
	for (const profile of (await ctx.listDirs(profilesDir)).slice(0, MAX_PROFILES)) {
		files.push(join(profilesDir, profile, "cordis.patch.yml"));
		files.push(join(profilesDir, profile, "cordis.yml"));
	}

	const usedCredentials = new Set<string>();
	for (const path of files) {
		const doc = await readYaml(ctx, path);
		for (const [providerId, provider] of collectHarnessProviders(doc)) {
			const keyName = str(provider.apiKeyEnv);
			const apiKey = str(provider.apiKey) ?? (keyName ? credential(keyName) : undefined);
			if (!apiKey) continue;
			if (keyName) usedCredentials.add(keyName);
			const protocol = protocolFromPiAi(str(provider.api)) ?? "completions-compatible";
			const rawBase = str(provider.baseURL) ?? "";
			const candidate = makeCandidate("deepseek-harness", `provider:${providerId}`, {
				name: str(provider.name) ?? providerId,
				protocol,
				// The harness documents Anthropic bases without `/v1`.
				baseUrl: protocol === "anthropic-messages" ? anthropicBaseWithVersion(rawBase) : rawBase,
				apiKey,
				configPath: path,
			});
			if (candidate) candidates.push(candidate);
		}
	}

	// The harness's built-in DeepSeek provider needs no providers block, only a key.
	const deepseekKey = credential("DEEPSEEK_API_KEY");
	if (deepseekKey && !usedCredentials.has("DEEPSEEK_API_KEY")) {
		const candidate = makeCandidate("deepseek-harness", "builtin:deepseek", {
			name: "DeepSeek",
			protocol: "completions-compatible",
			baseUrl: "https://api.deepseek.com/v1",
			apiKey: deepseekKey,
			configPath: join(dshHome, ".credentials.yaml"),
		});
		if (candidate) candidates.push(candidate);
	}
	return { found, candidates, notes: [] };
}

// ── Scan ────────────────────────────────────────────────────────────────────

const SCANNERS: Record<LocalImportSource, (ctx: LocalScanContext) => Promise<SourceScan>> = {
	claude: scanClaude,
	codex: scanCodex,
	opencode: scanOpencode,
	"deepseek-harness": scanDeepseekHarness,
};

/** Normalized identity of a credential: same endpoint, protocol and key. */
export function providerIdentity(protocol: string, baseUrl: string, apiKey: string): string {
	return `${protocol}\n${trimSlashes(baseUrl).toLowerCase()}\n${apiKey}`;
}

export async function scanLocalProviderConfigs(
	ctx: LocalScanContext = createDefaultScanContext(),
): Promise<LocalProviderScan> {
	const scan: LocalProviderScan = { candidates: [], notes: [], sources: [] };
	const seen = new Set<string>();
	for (const [source, scanner] of Object.entries(SCANNERS) as Array<
		[LocalImportSource, (ctx: LocalScanContext) => Promise<SourceScan>]
	>) {
		let result: SourceScan;
		try {
			result = await scanner(ctx);
		} catch {
			result = { found: false, candidates: [], notes: [] };
		}
		scan.sources.push({ source, found: result.found });
		scan.notes.push(...result.notes);
		for (const candidate of result.candidates) {
			// The same key is often configured in several tools; offer it once.
			const identity = providerIdentity(candidate.protocol, candidate.baseUrl, candidate.apiKey);
			if (seen.has(identity)) continue;
			seen.add(identity);
			scan.candidates.push(candidate);
		}
	}
	return scan;
}

/**
 * The browser-facing view of a scan: keys reduced to a masked preview, plus
 * whether an identical provider (protocol, base URL, key) is already configured.
 */
export function summarizeLocalScan(
	scan: LocalProviderScan,
	configuredIdentities: ReadonlySet<string>,
): LocalProviderScanResponse {
	return {
		candidates: scan.candidates.map(({ apiKey, ...candidate }) => ({
			...candidate,
			apiKeyPreview: maskSecret(apiKey),
			hasKey: apiKey.length > 0,
			alreadyConfigured: configuredIdentities.has(
				providerIdentity(candidate.protocol, candidate.baseUrl, apiKey),
			),
		})),
		notes: scan.notes,
		sources: scan.sources,
	};
}

export function createDefaultScanContext(): LocalScanContext {
	return {
		home: getHome(),
		env: process.env,
		async readText(path) {
			try {
				const info = await stat(path);
				if (!info.isFile() || info.size > MAX_CONFIG_BYTES) return null;
				return await readFile(path, "utf8");
			} catch {
				return null;
			}
		},
		async listDirs(path) {
			try {
				const entries = await readdir(path, { withFileTypes: true });
				return entries.filter((entry) => entry.isDirectory()).map((entry) => entry.name);
			} catch {
				return [];
			}
		},
		async exists(path) {
			try {
				await stat(path);
				return true;
			} catch {
				return false;
			}
		},
	};
}
