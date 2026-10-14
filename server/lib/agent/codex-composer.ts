import { createHash, randomUUID } from "node:crypto";
import {
	CODEX_COMPOSER_MAX_PROMPT_CHARS,
	CODEX_COMPOSER_SCHEMA,
	DEFAULT_CODEX_COMPOSER_PROMPT,
} from "@shared/codex-composer";
import { settings } from "../settings";
import type { GenerateMetaResult } from "./provider";
import { type ApiRequestDumpCollector, boundedJsonCopy } from "./request-dump";

export const CODEX_COMPOSER_MAX_SEED_BYTES = 8 * 1024 * 1024;
export const CODEX_COMPOSER_MAX_RESPONSE_BYTES = 1024 * 1024;

export interface CodexComposerRunOptions {
	signal: AbortSignal;
	requestDump?: ApiRequestDumpCollector;
}

export interface CodexComposerSeed {
	providerId: string;
	model: string;
	conversationId: string;
	configSignature: string;
	bytes: number;
	run(options: CodexComposerRunOptions): Promise<GenerateMetaResult>;
}

/** A transient reference to the ordinary request; no copy until a final text answer exists. */
export interface CodexComposerCapture {
	finalize(assistantTurn: unknown[]): CodexComposerSeed | undefined;
}

export function getCodexComposerSettings(
	providerId: string,
): { prompt: string; signature: string } | undefined {
	const provider = settings.openaiProviders?.find((entry) => entry.id === providerId);
	const native = providerId === "codex";
	const source = native ? settings.codex : provider;
	const enabled = native ? settings.codex?.useComposer : provider?.codexComposerEnabled;
	if (!source || !enabled || (!native && (provider?.disabled || provider?.apiMode !== "codex")))
		return undefined;
	const configuredPrompt = native ? settings.codex?.composerPrompt : provider?.codexComposerPrompt;
	if (configuredPrompt && configuredPrompt.length > CODEX_COMPOSER_MAX_PROMPT_CHARS)
		return undefined;
	const prompt = normalizeCodexComposerPrompt(configuredPrompt);
	return {
		prompt,
		signature: createHash("sha256").update(JSON.stringify(source)).digest("hex"),
	};
}
export function normalizeCodexComposerPrompt(prompt: string | undefined): string {
	const value = prompt?.trim() || DEFAULT_CODEX_COMPOSER_PROMPT;
	if (value.length > CODEX_COMPOSER_MAX_PROMPT_CHARS) {
		throw new Error("Composer prompt exceeds size limit");
	}
	return value;
}

export function buildCodexComposerRequest(
	snapshot: Record<string, unknown>,
	prompt: string,
): Record<string, unknown> {
	if (!Array.isArray(snapshot.input))
		throw new Error("Composer requires an ordinary request input");
	const identity = snapshot.client_metadata as Record<string, unknown> | undefined;
	if (typeof identity?.session_id !== "string" || typeof identity.thread_id !== "string") {
		throw new Error("Composer requires the ordinary Codex request identity");
	}
	const turnId = randomUUID();
	const turnMetadata = JSON.stringify({
		session_id: identity.session_id,
		thread_id: identity.thread_id,
		turn_id: turnId,
		request_kind: "turn",
		thread_source: "composer_predictions",
	});
	const { previous_response_id: _previous, type: _type, ...body } = snapshot;
	return {
		...body,
		stream: true,
		store: false,
		// Keep definitions (and the complete ordinary prefix), but execute no tools.
		tool_choice: "none",
		client_metadata: {
			...identity,
			turn_id: turnId,
			"x-codex-turn-metadata": turnMetadata,
		},
		input: [
			...snapshot.input,
			{
				type: "message",
				role: "user",
				content: [
					{
						type: "input_text",
						text: `${normalizeCodexComposerPrompt(prompt)}\n\nReturn only JSON matching this schema: ${JSON.stringify(CODEX_COMPOSER_SCHEMA)}.`,
					},
				],
			},
		],
		// Do not override reasoning or text.format: both changed the rendered prefix
		// in live probes. Output shape is enforced by the prompt and host validation.
	};
}

export function createCodexComposerCapture(options: {
	request: Record<string, unknown>;
	providerId: string;
	model: string;
	conversationId: string;
	configSignature: string;
	prompt: string;
	serializeAssistant(items: unknown[]): unknown[];
	send(
		body: Record<string, unknown>,
		options: CodexComposerRunOptions,
	): Promise<GenerateMetaResult>;
}): CodexComposerCapture {
	return {
		finalize(assistantTurn) {
			if (!assistantTurn.length || !Array.isArray(options.request.input)) return undefined;
			const snapshot = boundedJsonCopy(
				{
					...options.request,
					input: [...options.request.input, ...options.serializeAssistant(assistantTurn)],
				},
				CODEX_COMPOSER_MAX_SEED_BYTES,
			);
			if (snapshot.truncated) return undefined;
			const bytes = Buffer.byteLength(JSON.stringify(snapshot.value), "utf8");
			if (bytes > CODEX_COMPOSER_MAX_SEED_BYTES) return undefined;
			const { providerId, model, conversationId, configSignature, prompt, send } = options;
			return {
				providerId,
				model,
				conversationId,
				configSignature,
				bytes,
				run: (runOptions) => send(buildCodexComposerRequest(snapshot.value, prompt), runOptions),
			};
		},
	};
}
