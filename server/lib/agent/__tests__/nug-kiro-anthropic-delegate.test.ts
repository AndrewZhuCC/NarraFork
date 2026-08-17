import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import {
	deleteNugCachedModels,
	setNugCachedCapabilities,
	setNugCachedModels,
} from "../../nug-model-cache";
import type { NUGProviderConfig } from "../../settings";
import { AnthropicProvider } from "../anthropic-provider";
import { NugProvider } from "../nug-provider";
import type { DbMessage } from "../provider";

/**
 * Routing of the NUG Kiro channel onto the Anthropic delegate.
 *
 * The choice is driven by a capability the gateway advertises in `/v1/models`,
 * which is what lets a new client keep working against an old gateway. That
 * fallback is invisible when it misfires — the legacy path still answers — so each
 * direction is pinned here.
 */

const PROVIDER_ID = "nug-kiro-delegate-test";

function config(overrides: Partial<NUGProviderConfig> = {}): NUGProviderConfig {
	return {
		id: PROVIDER_ID,
		name: "Test Gateway",
		prefix: "nugtest",
		baseUrl: "https://gateway.invalid",
		apiKey: "key",
		enabled: true,
		// The operator opt-in defaults ON in these fixtures so each test can focus on
		// the capability side; the switch itself is covered by its own block below.
		kiroAnthropicProtocol: true,
		...overrides,
	} as unknown as NUGProviderConfig;
}

/** Register one kiro-channel model so resolveMeta can classify the channel. */
function cacheKiroModel(): void {
	setNugCachedModels(
		PROVIDER_ID,
		// The cached id is `channel:model`, which is how resolveNugModelMeta
		// classifies the channel.
		[
			{
				id: "kiro:claude-sonnet-4.5",
				channelType: "kiro",
				channel: "kiro",
				model: "claude-sonnet-4.5",
			},
		],
		"hash-1",
	);
}

beforeEach(() => {
	cacheKiroModel();
});

afterEach(() => {
	setNugCachedCapabilities(PROVIDER_ID, undefined);
	deleteNugCachedModels(PROVIDER_ID);
});

/** The delegate is private; prepareForModel is the public way to install it. */
function delegateFor(model: string, overrides: Partial<NUGProviderConfig> = {}): unknown {
	const provider = new NugProvider(config(overrides));
	provider.prepareForModel(model);
	return (provider as unknown as { activeDelegate: unknown }).activeDelegate;
}

describe("nug kiro channel delegate selection", () => {
	test("uses the Anthropic delegate when the gateway advertises the endpoint", () => {
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		expect(delegateFor("nugtest:kiro:claude-sonnet-4.5")).toBeInstanceOf(AnthropicProvider);
	});

	test("falls back to the native Kiro path when the capability is absent", () => {
		// An older gateway omits the field entirely. A null delegate is what selects
		// the in-process CodeWhisperer implementation.
		setNugCachedCapabilities(PROVIDER_ID, undefined);

		expect(delegateFor("nugtest:kiro:claude-sonnet-4.5")).toBeNull();
	});

	test("an unrelated capability does not enable the endpoint", () => {
		// Capabilities are matched by exact name, so a gateway serving some other
		// optional protocol must not be mistaken for one serving this endpoint.
		setNugCachedCapabilities(PROVIDER_ID, ["something.else.v1"]);

		expect(delegateFor("nugtest:kiro:claude-sonnet-4.5")).toBeNull();
	});

	test("the delegate targets the kiro endpoint, not the anthropic channel", () => {
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		const delegate = delegateFor("nugtest:kiro:claude-sonnet-4.5") as unknown as {
			config: { baseUrl: string; officialApi?: boolean };
		};

		expect(delegate.config.baseUrl).toBe("https://gateway.invalid/v1/kiro");
		// officialApi must stay false: it is what makes the history builder lift
		// trailing sys rows into trailingUserText instead of leaving them in history
		// as mid-conversation system messages.
		expect(delegate.config.officialApi).toBe(false);
	});

	test("thinking signatures are tagged with the channel identity", () => {
		// A signature is only valid against the server that minted it. Tagging with
		// the NUG channel keeps it from being replayed to a direct Anthropic upstream
		// or a different channel, where verification would fail.
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:kiro:claude-sonnet-4.5");

		expect(provider.getActiveReasoningSource()).toBe("nugtest:kiro");
	});
});

describe("nug kiro delegate history", () => {
	test("lifts a trailing sys row into trailingUserText", async () => {
		// Position is the whole point: a Dynamic Spec reminder left at the end of
		// history reads as background, while the current turn is what the model was
		// just asked. Both paths deliver the text, so getting this wrong only shows
		// up as the model weighting it lower.
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);
		const provider = new NugProvider(config());

		const messages: DbMessage[] = [
			{
				id: "u1",
				role: "user",
				contentJson: [{ type: "text", text: "first question" }],
				contentText: "first question",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
			{
				id: "a1",
				role: "assistant",
				contentJson: [{ type: "text", text: "answer" }],
				contentText: "answer",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
			{
				id: "s1",
				role: "sys",
				contentJson: [{ type: "text", text: "REMINDER: finish the task" }],
				contentText: "REMINDER: finish the task",
				parentToolUseId: null,
				messageUuid: null,
				toolCalls: [],
			},
		];

		const result = (await provider.buildHistory(
			messages,
			"nugtest:kiro:claude-sonnet-4.5",
			"narr-1",
		)) as {
			history: unknown[];
			trailingUserText?: string;
		};

		expect(result.trailingUserText).toContain("REMINDER: finish the task");
		expect(JSON.stringify(result.history)).not.toContain("REMINDER: finish the task");
	});

	test("history has no trailingUserText when no sys row trails it", async () => {
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);
		const provider = new NugProvider(config());

		const result = (await provider.buildHistory(
			[
				{
					id: "u1",
					role: "user",
					contentJson: [{ type: "text", text: "hello" }],
					contentText: "hello",
					parentToolUseId: null,
					messageUuid: null,
					toolCalls: [],
				},
			],
			"nugtest:kiro:claude-sonnet-4.5",
			"narr-1",
		)) as { trailingUserText?: string };

		expect(result.trailingUserText).toBeUndefined();
	});
});

describe("image dedup shape follows the delegate, not the channel name", () => {
	test("a kiro channel on the Anthropic delegate carries Anthropic image parts", () => {
		// The dedup walker must match the shape the history actually holds. Choosing
		// it by channel name would walk Kiro-shaped nodes over an Anthropic history,
		// find no images, and silently disable dedup — every turn would then resend
		// full payloads, which looks like a gateway problem rather than a client one.
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		const delegate = delegateFor("nugtest:kiro:claude-sonnet-4.5");
		expect(delegate).toBeInstanceOf(AnthropicProvider);

		// formatToolResult is the observable proxy for the history shape: the
		// Anthropic delegate emits `source.data`, the native Kiro path emits
		// `source.bytes` under a `_images` marker.
		const result = (delegate as AnthropicProvider).formatToolResult("t1", "out", false, [
			{ format: "png", base64: "AAAA" },
		]) as { content: Array<{ type: string; source?: { data?: string } }> };

		const image = result.content.find((part) => part.type === "image");
		expect(image?.source?.data).toBe("AAAA");
	});

	test("the native kiro path keeps the Kiro tool-result shape", () => {
		setNugCachedCapabilities(PROVIDER_ID, undefined);
		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:kiro:claude-sonnet-4.5");

		const result = provider.formatToolResult("t1", "out", false, [
			{ format: "png", base64: "AAAA" },
		]) as { toolUseId: string; _images?: Array<{ source: { bytes: string } }> };

		expect(result.toolUseId).toBe("t1");
		expect(result._images?.[0].source.bytes).toBe("AAAA");
	});
});

describe("conversation identity on the kiro delegate", () => {
	/**
	 * Drive one real chat() request through a stubbed fetch and return its headers.
	 *
	 * The header has to be asserted on the actual outgoing request rather than on a
	 * provider field: it is the wire that the gateway reads, and a flag that is set
	 * but never serialized would satisfy any weaker assertion.
	 */
	async function chatHeaders(provider: {
		chat: (params: unknown) => AsyncIterable<unknown>;
	}): Promise<Headers> {
		const original = globalThis.fetch;
		let captured = new Headers();
		globalThis.fetch = (async (_input: unknown, init: { headers?: unknown }) => {
			// Normalized through Headers because the request layer may pass either a
			// plain record or a Headers instance, and header names are
			// case-insensitive on the wire — a record lookup by exact case would fail
			// against a correct request.
			captured = new Headers((init?.headers ?? {}) as HeadersInit);
			return new Response('event: message_stop\ndata: {"type":"message_stop"}\n\n', {
				status: 200,
				headers: { "content-type": "text/event-stream" },
			});
		}) as unknown as typeof fetch;
		try {
			for await (const _event of provider.chat({
				conversationId: "0f9d3a1e-2b4c-4d5e-8f10-112233445566",
				content: "hi",
				model: "nugtest:kiro:claude-sonnet-4.5",
				cwd: "/w",
				history: [],
				tools: [],
				toolResults: [],
				signal: new AbortController().signal,
			})) {
				// drain
			}
		} finally {
			globalThis.fetch = original;
		}
		return captured;
	}

	test("the kiro delegate sends X-Conversation-ID", async () => {
		// The Anthropic Messages protocol carries no conversation identity, so this
		// header is the ONLY thing the gateway can key credential affinity on. Without
		// it the gateway mints a fresh id per request: every turn of one conversation
		// may land on a different upstream credential, and requests still succeed — so
		// nothing surfaces the loss.
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);
		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:kiro:claude-sonnet-4.5");

		const headers = await chatHeaders(provider as unknown as Parameters<typeof chatHeaders>[0]);

		expect(headers.get("x-conversation-id")).toBe("0f9d3a1e-2b4c-4d5e-8f10-112233445566");
	});

	test("a direct Anthropic provider does not send it", async () => {
		// The header is NUG-specific and meaningless to the Anthropic API or any
		// third-party compatible endpoint, so it stays opt-in per delegate rather than
		// becoming a default for every Anthropic-shaped request.
		const direct = new AnthropicProvider({
			id: "direct",
			prefix: "anth",
			baseUrl: "https://api.invalid",
			apiKey: "key",
			officialApi: false,
			models: [{ id: "claude-sonnet-4.5", name: "S" }],
		} as never);

		const headers = await chatHeaders(direct as unknown as Parameters<typeof chatHeaders>[0]);

		expect(headers.get("x-conversation-id")).toBeNull();
	});
});

describe("operator opt-in switch", () => {
	// The switch exists so a gateway that advertises the endpoint cannot change this
	// client's behaviour until someone has verified it. The two paths build different
	// requests and parse different streams, so an automatic switch would make a
	// regression look like a model or upstream problem rather than a protocol change.
	test("stays on the native path when the switch is off, even if advertised", () => {
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		expect(
			delegateFor("nugtest:kiro:claude-sonnet-4.5", { kiroAnthropicProtocol: false }),
		).toBeNull();
	});

	test("an absent switch reads as off", () => {
		// Default-off matters for upgrades: an existing config has no such field and
		// must keep its current behaviour.
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		expect(
			delegateFor("nugtest:kiro:claude-sonnet-4.5", { kiroAnthropicProtocol: undefined }),
		).toBeNull();
	});

	test("the switch alone is not enough without the capability", () => {
		// Turning it on against an older gateway must not target an endpoint that
		// returns 404 on every turn.
		setNugCachedCapabilities(PROVIDER_ID, undefined);

		expect(
			delegateFor("nugtest:kiro:claude-sonnet-4.5", { kiroAnthropicProtocol: true }),
		).toBeNull();
	});

	test("both conditions together select the delegate", () => {
		setNugCachedCapabilities(PROVIDER_ID, ["kiro.messages.v1"]);

		expect(
			delegateFor("nugtest:kiro:claude-sonnet-4.5", { kiroAnthropicProtocol: true }),
		).toBeInstanceOf(AnthropicProvider);
	});
});

describe("nug openai-family channel reasoning source", () => {
	// codex/openai/responses channels mint OpenAI-shaped reasoning credentials
	// (`encrypted_content`), which verify only against the gateway channel that
	// produced them. Each delegate must therefore report the NUG channel identity —
	// not the OpenAIProvider's own prefix — so strict replay never mistakes a
	// gateway credential for a direct-OpenAI one (or another channel's).
	function cacheOpenaiFamilyModel(
		channelType: "codex" | "openai" | "responses",
		channel: string,
	): void {
		setNugCachedModels(
			PROVIDER_ID,
			[
				{
					id: `${channel}:gpt-5`,
					channelType,
					channel,
					model: "gpt-5",
				},
			],
			"hash-openai-family",
		);
	}

	test("codex channel tags encrypted content with the channel identity", () => {
		cacheOpenaiFamilyModel("codex", "codex");
		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:codex:gpt-5");

		expect(provider.getActiveReasoningSource()).toBe("nugtest:codex");
	});

	test("openai channel tags encrypted content with the channel identity", () => {
		cacheOpenaiFamilyModel("openai", "oa");
		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:oa:gpt-5");

		expect(provider.getActiveReasoningSource()).toBe("nugtest:oa");
	});

	test("responses channel tags encrypted content with the channel identity", () => {
		cacheOpenaiFamilyModel("responses", "resp");
		const provider = new NugProvider(config());
		provider.prepareForModel("nugtest:resp:gpt-5");

		expect(provider.getActiveReasoningSource()).toBe("nugtest:resp");
	});
});
