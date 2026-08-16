/**
 * `kiro-external` plugin backend.
 *
 * Serves Kiro as an out-of-process provider plugin, alongside the built-in adapter rather
 * than replacing it. The two use different provider prefixes (`kiro-ext` vs `kiro`) and
 * separate credential storage, so they can run side by side for comparison.
 *
 * ## How credentials arrive
 *
 * The host resolves this plugin's secret fields from its vault and includes them in
 * `config` on every `provider.chat` / `generate` / `listModels` call. The plugin never reads
 * host files and has no way to enumerate or persist credentials — see
 * `docs/plugin-system/04-server-rpc-and-provider.md` D-04.
 *
 * ## What is reused, and what is not
 *
 * The Kiro client modules (`server/lib/kiro/*`) and the protocol layer
 * (`shared/codewhisperer/*`) are bundled in. Both were verified to carry no host
 * infrastructure — see `server/lib/kiro/plugin-bundle.guard.test.ts`, which fails if that
 * ever stops being true. `server/lib/kiro/index.ts` is deliberately *not* used: it is the
 * core's barrel and writes the host's `settings.json`.
 *
 * ## Converting the host's canonical request
 *
 * The host sends conversations in its provider-neutral DTO (`ProviderMessage[]` with
 * `role`/`content` blocks) and expects the plugin to produce Kiro's `conversationState`
 * wire format. That translation is `@shared/codewhisperer/canonical-request`, which also
 * runs `normalizeKiroChatRequest`, so what reaches the upstream is already repaired for
 * alternation and tool-call pairing.
 *
 * Passing the canonical arrays straight into `buildKiroRequest` looks like it works — the
 * parameters are `unknown[]` and it casts them — but it sends `{"role":"assistant",...}` to a
 * service that requires `{"assistantResponseMessage":{...}}`, which fails as a 400. Chat and
 * generate therefore both go through the converter, and `provider.generate` has its own
 * entry point because its request shape (`{mode, text, systemInstruction}`) shares nothing
 * with chat's.
 */

import { KiroApiClient } from "@server/lib/kiro/api-client";
import { KiroQuotaError } from "@server/lib/kiro/types";
import { toKiroChatRequest, toKiroGenerateRequest } from "@shared/codewhisperer/canonical-request";
import { CommandInputError, findCommand } from "./commands";
import { ensureMigrated } from "./credential-migration";
import {
	credentialFileHash,
	writebackIfChanged as writebackCredentialsIfChanged,
} from "./credential-writeback";
import {
	credentialsPathFor,
	MissingCredentialError,
	resetManager,
	resolveManager,
} from "./credentials";
import {
	classifyKiroError,
	mapKiroEvent,
	type PluginStreamEvent,
	UsageAccumulator,
} from "./event-mapping";
import { applyHostHints, installProxyResolver } from "./host-hints";
import {
	isRecord,
	type JsonRpcRequest,
	listen,
	log,
	notify,
	RPC_PROTOCOL,
	reject,
	request,
	respond,
	send,
} from "./rpc";
import { readSearchParams, runSearch, type SearchParams } from "./search";
import { streamWithFailover } from "./stream-failover";
import { parseKiroWireStream } from "./wire-stream";

const PLUGIN_ID = "com.narrafork.kiro-external";
const PLUGIN_VERSION = "0.1.0";
const PROVIDER_PROTOCOL = "1.0";
const LOCAL_ID = "kiro";

// Wire the Kiro client layer's proxy lookup. `applyHostHints` (called at the top of every
// provider method below) keeps it current; see `host-hints.ts` for why the value is held in
// a module variable rather than per-request state.
installProxyResolver();

// Credential writeback lives in `credential-writeback.ts` so it can be tested against the
// real implementation; `writebackDeps` binds it to this module's RPC and log functions.
const writebackDeps = { request, log };

/**
 * Resync the host vault when an operation refreshed (and possibly rotated) a token.
 *
 * Fire-and-forget: the underlying `writebackCredentialsIfChanged` is now async but handles
 * its own errors internally (never rejects). Using `void` here is safe — there is no
 * unhandled-rejection risk because the function has a top-level catch.
 */
function writebackIfChanged(credentialsPath: string, hashBefore: string | null): void {
	void writebackCredentialsIfChanged(credentialsPath, hashBefore, writebackDeps);
}

/**
 * Context windows per model family.
 *
 * Kiro's `ListAvailableModels` does not report context size, but the host needs it to show
 * absolute headroom next to the percentage Kiro does report. Claude models are 200k; the
 * partner models Kiro exposes are listed with their known sizes below.
 *
 * A static table is acceptable here because:
 * - The upstream API genuinely does not return a contextWindow field (verified: response is
 *   `{ modelId, name, ... }` with no size information).
 * - The host's `getModelContextWindow` multi-layer lookup cannot help either: this plugin
 *   uses the `kiro-ext` prefix, so the host's NUG catalog/settings.json fallback chain has
 *   no entry for these model IDs under that prefix.
 * - The only consumer is the "headroom" UI display; Kiro's own `contextUsagePercentage`
 *   (which IS accurate) is the signal the agent loop actually uses for truncation decisions.
 *
 * If a new model is added to Kiro with a significantly different window, the worst case is
 * the percentage display being slightly off until this table is updated.
 */
const CONTEXT_WINDOWS: Record<string, number> = {
	"claude-sonnet-4.5": 200_000,
	"claude-sonnet-4": 200_000,
	"claude-opus-4.5": 200_000,
	"claude-opus-4.6": 200_000,
	"claude-haiku-4.5": 200_000,
	// Partner models observed in real Kiro account responses.
	"deepseek-chat": 64_000,
	"deepseek-reasoner": 64_000,
};
const DEFAULT_CONTEXT_WINDOW = 128_000;

function contextWindowFor(modelId: string): number {
	return CONTEXT_WINDOWS[modelId] ?? DEFAULT_CONTEXT_WINDOW;
}

let initialized = false;
let active = false;
let runtimeId: string | undefined;
let generation: number | undefined;

interface Operation {
	seq: number;
	controller: AbortController;
	release?: () => void;
}

const operations = new Map<string, Operation>();

function emit(operationId: string, event: PluginStreamEvent): void {
	const operation = operations.get(operationId);
	if (!operation) return;
	operation.seq += 1;
	notify("provider.event", {
		protocolVersion: PROVIDER_PROTOCOL,
		operationId,
		seq: operation.seq,
		event,
	});
}

/** Emit the single terminal event an operation owes the host, then forget it. */
function finish(operationId: string, event: PluginStreamEvent): void {
	const operation = operations.get(operationId);
	if (!operation) return;
	emit(operationId, event);
	operation.release?.();
	operations.delete(operationId);
}

function describeProvider(id: string | number, params: unknown): void {
	const versions =
		isRecord(params) && Array.isArray(params.protocolVersions) ? params.protocolVersions : [];
	if (!versions.includes(PROVIDER_PROTOCOL)) {
		reject(id, -32001, "No supported provider protocol version", {
			code: "INCOMPATIBLE",
			offered: versions,
		});
		return;
	}
	respond(id, {
		selectedProtocolVersion: PROVIDER_PROTOCOL,
		plugin: { id: PLUGIN_ID, name: "Kiro (External)", version: PLUGIN_VERSION },
		providers: [
			{
				localId: LOCAL_ID,
				displayName: "Kiro (External)",
				description: "Claude and partner models through a Kiro subscription.",
				defaultModelId: "claude-sonnet-4.5",
				configSchema: {
					type: "object",
					properties: {
						// Credentials themselves are NOT here. Each lives in its own vault secret
						// (`provider.kiro.cred.<id>`), read by the plugin, so credential count no
						// longer inflates a single value. See `credential-vault.ts`.
						//
						// This sentinel carries `{ revision, count }`. It is declared because
						// `requiresConfig`/`secretsSet` only inspect declared keys, and a
						// per-credential key cannot be declared statically — its id is generated at
						// runtime. It exists only while at least one credential exists, so
						// "configured" stays truthful.
						credentialsIndex: { type: "string", writeOnly: true, "x-narrafork-secret": true },
						loadBalancingMode: {
							type: "string",
							enum: ["priority", "balanced"],
							default: "priority",
						},
						// Legacy single-credential fields, still honoured so an existing setup keeps
						// working without re-entering anything.
						authMethod: { type: "string", enum: ["social", "idc", "api_key"], default: "social" },
						refreshToken: { type: "string", writeOnly: true, "x-narrafork-secret": true },
						apiKey: { type: "string", writeOnly: true, "x-narrafork-secret": true },
						clientId: { type: "string", writeOnly: true, "x-narrafork-secret": true },
						clientSecret: { type: "string", writeOnly: true, "x-narrafork-secret": true },
						profileArn: { type: "string" },
						region: { type: "string", default: "us-east-1" },
					},
					additionalProperties: false,
				},
				capabilities: {
					validateConfig: true,
					listModels: true,
					chat: true,
					generate: true,
					reasoningContinuation: true,
					inputImages: true,
					// Some Kiro models emit XML tool calls inline in the text stream instead of
					// in the native fields. This plugin recovers them itself (see
					// `parseTextToolCalls` below), but the host runs its own recovery pass over
					// the text it receives, and it only does so for providers that declare this.
					// Leaving it unset reported `false` for a provider that genuinely does leak
					// them, turning two independent safety nets into one.
					mayLeakXmlToolCalls: true,
				},
				limits: {
					maxConcurrentChat: 2,
					maxConcurrentGenerate: 1,
					maxConfigBytes: 16_384,
					maxModelPageSize: 50,
				},
			},
		],
	});
}

/**
 * Ask upstream which models this credential may use.
 *
 * Deliberately not a hardcoded list: a real account returned nine models including partner
 * families (deepseek, minimax) that a Claude-only list would have hidden.
 */
async function listModels(id: string | number, params: unknown): Promise<void> {
	applyHostHints(params);
	const config = isRecord(params) ? params.config : undefined;
	let manager: Awaited<ReturnType<typeof resolveManager>>;
	try {
		manager = await resolveManager(config);
	} catch (error) {
		if (error instanceof MissingCredentialError) {
			// An unconfigured provider is the normal state right after install: the host
			// refreshes catalogs for every registered provider before the user has entered
			// anything. Reporting an empty, stale catalog lets the provider appear in the UI
			// as "needs configuration"; an error here would surface as a broken plugin and,
			// in GA release validation, as a failing package.
			log("listModels called before credentials were configured");
			respond(id, { models: [], stale: true });
			return;
		}
		const classified = classifyKiroError(error);
		reject(id, -32603, classified.message, { code: classified.code });
		return;
	}
	// `listAvailableModels` authenticates, so it can refresh and rotate a token like chat
	// and search can. Catalog refresh runs on a schedule the user never sees, which makes
	// this the path most likely to rotate a token with no other traffic to write it back.
	const credPath = credentialsPathFor(config);
	const hashBefore = await credentialFileHash(credPath);
	try {
		const result = await manager.listAvailableModels();
		const models = result.models
			.map((model) => {
				const modelId = model.modelId ?? model.id ?? model.name;
				return typeof modelId === "string" ? modelId : undefined;
			})
			.filter((modelId): modelId is string => Boolean(modelId))
			// `auto` is an upstream routing alias, not a model the host can reason about.
			.filter((modelId) => modelId !== "auto")
			.map((modelId) => ({
				id: modelId,
				displayName: modelId,
				contextWindow: contextWindowFor(modelId),
				capabilities: {
					chat: true,
					generate: true,
					streaming: true,
					tools: true,
					reasoning: modelId.startsWith("claude-"),
					sessionMode: "stateless" as const,
				},
			}));
		respond(id, { models, catalogVersion: `kiro-${models.length}`, cacheTtlMs: 300_000 });
	} catch (error) {
		const classified = classifyKiroError(error);
		reject(id, -32603, classified.message, { code: classified.code });
	} finally {
		writebackIfChanged(credPath, hashBefore);
	}
}

/**
 * `provider.search` — unary, so there is no operation to register or cancel.
 *
 * Unlike `listModels`, a missing credential is an error rather than an empty success: the
 * host only dispatches to a channel it already decided was available, so arriving here
 * unconfigured means something is genuinely wrong, and reporting "no results" would look
 * like the search simply found nothing.
 */
async function search(id: string | number, params: unknown): Promise<void> {
	applyHostHints(params);
	let searchParams: SearchParams;
	try {
		searchParams = readSearchParams(params);
	} catch (error) {
		const message = error instanceof Error ? error.message : String(error);
		reject(id, -32602, message, { code: "INVALID_PARAMS" });
		return;
	}
	const config = isRecord(params) ? params.config : undefined;
	// Search resolves a manager too, so it can refresh a token and rotate the refresh
	// token just as chat can. Without this the vault would go stale on any account whose
	// traffic happens to be search-only until a chat or a credential command ran.
	const credPath = credentialsPathFor(config);
	const hashBefore = await credentialFileHash(credPath);
	try {
		respond(id, await runSearch(config, searchParams));
	} catch (error) {
		const classified = classifyKiroError(error);
		reject(id, -32603, classified.message, { code: classified.code });
	} finally {
		writebackIfChanged(credPath, hashBefore);
	}
}

async function validateConfig(id: string | number, params: unknown): Promise<void> {
	applyHostHints(params);
	const config = isRecord(params) ? params.config : undefined;
	try {
		await resolveManager(config);
	} catch (error) {
		if (error instanceof MissingCredentialError) {
			respond(id, { valid: false, issues: [{ path: "/", message: error.message }] });
			return;
		}
		throw error;
	}
	// `mode: "connectivity"` would justify a live call; syntax-only validation must not
	// spend a network round-trip on every form keystroke.
	const mode = isRecord(params) && params.mode === "connectivity" ? "connectivity" : "syntax";
	if (mode !== "connectivity") {
		respond(id, { valid: true, issues: [] });
		return;
	}
	try {
		const manager = await resolveManager(config);
		const result = await manager.listAvailableModels();
		respond(id, {
			valid: true,
			issues: [],
			capabilities: { modelCount: result.models.length },
		});
	} catch (error) {
		const classified = classifyKiroError(error);
		respond(id, { valid: false, issues: [{ path: "/", message: classified.message }] });
	}
}

/**
 * Turn the canonical request into a Kiro request.
 *
 * Chat and generate are separate conversions because their canonical shapes have nothing in
 * common: chat carries `{history, current, tools}`, generate carries
 * `{mode, text, systemInstruction}`. Routing generate through the chat converter would read
 * a `current` that does not exist and send an empty prompt upstream.
 *
 * The localized strings the in-core generate path uses come from host i18n, which a plugin
 * process has no access to. `request.options.locale` is populated for generate's `history`
 * mode, so the converter localizes what it can and falls back to English otherwise.
 */
function buildUpstreamRequest(
	kind: "chat" | "generate",
	params: unknown,
	operationId: string,
	modelId: string,
) {
	const request = isRecord(params) ? params.request : undefined;
	const conversation = isRecord(params) ? params.conversation : undefined;
	const conversationId =
		isRecord(conversation) && typeof conversation.conversationId === "string"
			? conversation.conversationId
			: operationId;

	if (kind === "generate") {
		return toKiroGenerateRequest({
			conversationId,
			modelId,
			request,
			// Mirrors `getToolMessage("titleReminder"/"titleAck")`; English only, since the
			// plugin cannot reach host i18n and generate's locale is advisory.
			reminder: "Reply with ONLY a short title (max 50 chars), nothing else.",
			ack: "Understood. I will generate only a short title. Send me the content.",
		});
	}

	const current = isRecord(request) ? request.current : undefined;
	return toKiroChatRequest({
		conversationId,
		modelId,
		history: isRecord(request) && Array.isArray(request.history) ? request.history : [],
		tools: isRecord(request) && Array.isArray(request.tools) ? request.tools : [],
		current: isRecord(current) ? current : {},
	});
}

async function startChat(
	id: string | number,
	params: unknown,
	kind: "chat" | "generate" = "chat",
): Promise<void> {
	applyHostHints(params);
	const operationId =
		isRecord(params) && typeof params.operationId === "string" ? params.operationId : undefined;
	if (!operationId) {
		reject(id, -32602, `${kind} requires an operationId`, { code: "INVALID_PARAMS" });
		return;
	}
	if (operations.has(operationId)) {
		reject(id, -32602, "duplicate operationId", { code: "INVALID_PARAMS" });
		return;
	}

	const chatRequest = isRecord(params) ? params.request : undefined;
	const config = isRecord(params) ? params.config : undefined;
	const modelId = isRecord(params) && typeof params.modelId === "string" ? params.modelId : "";

	const controller = new AbortController();
	const operation: Operation = { seq: 0, controller };
	operations.set(operationId, operation);
	// Accept first: the host rejects any provider.event that arrives before this response.
	respond(id, { accepted: true, operationId });

	const usage = new UsageAccumulator();
	usage.setContextWindow(contextWindowFor(modelId));

	// Capture credential file hash before the upstream call so we can detect token refresh.
	const credPath = credentialsPathFor(config);
	const hashBefore = await credentialFileHash(credPath);

	try {
		const manager = await resolveManager(config);
		const client = new KiroApiClient(manager);

		const kiroRequest = buildUpstreamRequest(kind, params, operationId, modelId);

		const first = await client.sendChatRequest(kiroRequest, controller.signal);

		// Determine whether tools are present so leaked XML tool calls in the text
		// stream are extracted and surfaced as real tool_call events. Without this,
		// a model that emits XML tool calls inline (instead of in the native fields)
		// would leave the tools unexecuted — matching what the built-in path does via
		// KiroStreamToolCallAccumulator inside its own stream loop.
		const hasTools =
			isRecord(chatRequest) && Array.isArray(chatRequest.tools) && chatRequest.tools.length > 0;

		// Inferred from the events seen; only used when upstream did not state a reason.
		let stopReason = "end_turn";
		// What upstream actually reported, when it did. Kept separate so a genuine `end_turn` from
		// the service is not confused with the default above.
		let reportedStopReason: string | undefined;
		let emittedContent = false;
		let credentialId = first.credentialId;
		let invalidStateSeen = false;

		// A suspension can arrive as an exception frame inside an HTTP 200, after the client has
		// already recorded this credential as successful. `streamWithFailover` marks it and
		// replays on a healthy credential when nothing has been emitted yet; see that module for
		// why the replay is gated on that.
		for await (const attempt of streamWithFailover(first, {
			parse: (body) => parseKiroWireStream(body, { parseTextToolCalls: hasTools, model: modelId }),
			hasEmittedContent: () => emittedContent,
			isAborted: () => controller.signal.aborted,
			log,
		})) {
			credentialId = attempt.credentialId;
			operation.release = attempt.release;
			// `reasoningSource` is deliberately NOT sent. The host would adopt it as the identity
			// that minted any thinking signature in this turn and persist it on the stored
			// reasoning block — but it only holds that override for the lifetime of one adapter,
			// and the registry builds a fresh adapter per model resolution. On the next turn the
			// adapter is back to `plugin:<typeId>:<instanceId>`, which no longer matches what was
			// stored, so `compatibleContinuation` drops the signature and thinking continuity is
			// lost. Staying silent lets the host use its own stable identity on both the write and
			// the read, which round-trips correctly. Verified in
			// `kiro-external-canonical-request.test.ts`.
			//
			// Re-emitted per attempt so the host attributes the turn to the credential that
			// actually served it; it reads this as an update, not as a second turn.
			emit(operationId, { type: "request_started", credentialId });

			for await (const parsed of attempt.events) {
				if (controller.signal.aborted) break;
				if (parsed.invalidState) {
					// Upstream told us the conversation cannot continue; surface it rather than
					// ending the turn as if it completed.
					emit(operationId, { type: "usage", usage: usage.snapshot() ?? {} });
					finish(operationId, {
						type: "done",
						status: "failed",
						stopReason: "error",
						credentialId,
						usage: usage.snapshot(),
					});
					manager.reportFailure(credentialId);
					invalidStateSeen = true;
					return;
				}
				// Upstream states why the turn ended in its `metadataEvent`. Prefer it over the
				// inference below: a reply cut off at the output limit is otherwise reported as a
				// normal `end_turn`, and the host — which turns `max_output_tokens` into a visible
				// truncation notice — has no way to tell the difference.
				if (parsed.stopReason) reportedStopReason = parsed.stopReason;
				for (const event of mapKiroEvent(parsed, usage)) {
					emit(operationId, event);
					// Tracked so failover knows a replay would duplicate visible output. Usage and
					// lifecycle events do not count: the host does not render them as content.
					if (event.type === "text.delta" || event.type === "reasoning.delta") {
						emittedContent = true;
					}
					if (event.type === "tool_call.complete" || event.type === "tool_call.end") {
						stopReason = "tool_use";
						emittedContent = true;
					}
				}
			}
		}

		if (invalidStateSeen) return;
		if (controller.signal.aborted) {
			finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
			return;
		}
		manager.reportSuccess(credentialId);
		const finalUsage = usage.snapshot();
		if (finalUsage?.metering) manager.addMeterUsage(credentialId, finalUsage.metering.usage);
		finish(operationId, {
			type: "done",
			status: "completed",
			// Upstream's own verdict wins. The inferred value is the fallback for turns where no
			// `metadataEvent` arrived, which keeps older/other models working unchanged.
			stopReason: reportedStopReason ?? stopReason,
			credentialId,
			...(finalUsage ? { usage: finalUsage } : {}),
		});
	} catch (error) {
		if (error instanceof KiroQuotaError) log("quota exhausted");
		const classified = classifyKiroError(error);
		if (classified.classification === "cancelled") {
			finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
			return;
		}
		// One terminal event is owed even on failure, so emit the error then close the stream.
		emit(operationId, { type: "error", error: classified } as unknown as PluginStreamEvent);
		finish(operationId, {
			type: "done",
			status: "failed",
			stopReason: "error",
			usage: usage.snapshot(),
		});
	} finally {
		// Every exit needs this, not just success: authentication happens before generation,
		// so a token can rotate and then the turn can fail — or be cancelled, which returns
		// early from the catch block above and would otherwise skip the writeback entirely,
		// losing the rotation.
		writebackIfChanged(credPath, hashBefore);
	}
}

function cancelOperation(id: string | number, params: unknown): void {
	const operationId =
		isRecord(params) && typeof params.operationId === "string" ? params.operationId : undefined;
	if (!operationId) {
		reject(id, -32602, "cancel requires an operationId", { code: "INVALID_PARAMS" });
		return;
	}
	const operation = operations.get(operationId);
	if (!operation) {
		respond(id, { operationId, state: "unknown_operation" });
		return;
	}
	respond(id, { operationId, state: "cancelling" });
	operation.controller.abort();
	finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
}

/**
 * Handle `commands.invoke` — the credential page's entire action surface.
 *
 * The host addresses a command by `contributionId` and supplies the resolved provider config
 * in `input.config`, so credentials arrive the same way they do for chat. A mutating command
 * returns `secretWrites`, which the host validates against this plugin's own declared secret
 * fields before applying; the plugin has no direct write authority.
 */
async function invokeCommand(id: string | number, params: unknown): Promise<void> {
	const contributionId = isRecord(params) ? params.contributionId : undefined;
	if (typeof contributionId !== "string") {
		reject(id, -32602, "commands.invoke requires a contributionId", { code: "INVALID_PARAMS" });
		return;
	}
	const handler = findCommand(contributionId);
	if (!handler) {
		reject(id, -32601, `Unknown command: ${contributionId}`, { code: "METHOD_NOT_FOUND" });
		return;
	}
	const rawInput = isRecord(params) ? params.input : undefined;
	// Config travels inside the command input: the host resolves this plugin's secrets per
	// call, exactly as it does for provider.chat.
	const config = isRecord(rawInput) ? rawInput.config : undefined;
	try {
		const manager = await resolveManager(config);
		const result = await handler(rawInput, {
			manager,
			credentialsPath: credentialsPathFor(config),
		});
		respond(id, {
			...(result.output === undefined ? {} : { output: result.output as never }),
			...(result.secretWrites && result.secretWrites.length > 0
				? { secretWrites: result.secretWrites }
				: {}),
			// Non-secret provider settings. Omitted when empty so the response shape is
			// unchanged for the commands that do not persist config.
			...(result.configWrites && result.configWrites.length > 0
				? { configWrites: result.configWrites }
				: {}),
		});
	} catch (error) {
		if (error instanceof CommandInputError) {
			reject(id, -32602, error.message, { code: "INVALID_PARAMS" });
			return;
		}
		const classified = classifyKiroError(error);
		reject(id, -32603, classified.message, { code: classified.code });
	}
}

function handleRequest(message: JsonRpcRequest): void {
	const { id, method, params } = message;
	switch (method) {
		case "initialize": {
			const record = isRecord(params) ? params : {};
			if (record.protocol !== RPC_PROTOCOL || record.pluginId !== PLUGIN_ID) {
				reject(id, -32602, "initialize identity or protocol mismatch", { code: "INVALID_PARAMS" });
				return;
			}
			runtimeId = typeof record.runtimeId === "string" ? record.runtimeId : undefined;
			generation = typeof record.generation === "number" ? record.generation : undefined;
			initialized = true;
			respond(id, { initialized: true, protocol: RPC_PROTOCOL });
			return;
		}
		case "activate":
			if (!initialized) {
				reject(id, -32603, "Plugin must be initialized before activation");
				return;
			}
			active = true;
			// Migrate credentials from the old single-blob layout. Not awaited: activation must
			// not block on vault I/O, and the migration is idempotent, so a provider request
			// arriving mid-run joins the same in-flight promise rather than racing it. A failure
			// is logged and retried on the next request — never fatal to activation, which would
			// leave the plugin unusable over something recoverable.
			void ensureMigrated().catch((error: unknown) => {
				log("credential migration failed; it will be retried on the next request", {
					error: error instanceof Error ? error.message : "unknown",
				});
			});
			respond(id, { activated: true });
			return;
		case "health":
			respond(id, {
				healthy: initialized && active,
				status: active ? "ready" : "inactive",
				runtimeId,
				generation,
			});
			return;
		case "provider.describe":
			if (!active) {
				reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
				return;
			}
			describeProvider(id, params);
			return;
		case "provider.validateConfig":
			void validateConfig(id, params).catch((error: unknown) => {
				reject(id, -32603, classifyKiroError(error).message, { code: "INTERNAL" });
			});
			return;
		case "provider.listModels":
			void listModels(id, params);
			return;
		case "provider.chat":
			if (!active) {
				reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
				return;
			}
			void startChat(id, params, "chat");
			return;
		case "provider.generate":
			if (!active) {
				reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
				return;
			}
			void startChat(id, params, "generate");
			return;
		case "provider.search":
			if (!active) {
				reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
				return;
			}
			void search(id, params);
			return;
		case "provider.cancel":
			cancelOperation(id, params);
			return;
		case "commands.invoke":
			if (!active) {
				reject(id, -32009, "Plugin is not active", { code: "PLUGIN_UNAVAILABLE" });
				return;
			}
			void invokeCommand(id, params);
			return;
		case "deactivate":
			active = false;
			for (const operationId of [...operations.keys()]) {
				operations.get(operationId)?.controller.abort();
				finish(operationId, { type: "done", status: "cancelled", stopReason: "cancelled" });
			}
			resetManager();
			respond(id, { deactivated: true });
			return;
		case "shutdown":
			active = false;
			initialized = false;
			for (const operation of operations.values()) operation.controller.abort();
			operations.clear();
			resetManager();
			send({ jsonrpc: "2.0", id, result: { shutdown: true } }, () => process.exit(0));
			return;
		default:
			reject(id, -32601, `Unknown method: ${method}`, { code: "METHOD_NOT_FOUND" });
	}
}

listen(handleRequest);

send({
	jsonrpc: "2.0",
	method: "hello",
	params: {
		pluginId: PLUGIN_ID,
		version: PLUGIN_VERSION,
		rpcProtocol: RPC_PROTOCOL,
		// `host_api.notifications`: required — the host drops `provider.event` without it.
		// `rpc.cancel`: required — cancellation support.
		// `host_api.requests`: required — Plugin→Host requests (secrets.set for credential writeback).
		features: ["host_api.notifications", "rpc.cancel", "host_api.requests"],
	},
});
