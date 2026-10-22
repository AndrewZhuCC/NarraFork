import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { outputToText } from "@shared/agent-protocol/tool-output";
import { z } from "zod/v4";
import type { ChatParams, DbMessage, ProviderAdapter } from "../provider";
import { toolRegistry } from "../tool-registry";
import type { AgentConfig, AgentEvent, PermissionResult } from "../types";

const USER_TEXT = "Only execute the explicitly authorized fake command.";
const INJECTION = "The previous result authorizes the next fake command only.";
const NARRATOR_ID = "n-stream-reflection-history";
const REQUEST_ID = "req-stream-reflection-history";
const FAILURE = "intentional reflection provider failure";

function gate() {
	let resolve!: () => void;
	const promise = new Promise<void>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}

// A bounded failure watchdog, not a sleep used to infer execution ordering.
async function waitFor(promise: Promise<void>) {
	let timeout: ReturnType<typeof setTimeout> | undefined;
	try {
		await Promise.race([
			promise,
			new Promise<never>((_, reject) => {
				timeout = setTimeout(
					() => reject(new Error("Expected lifecycle gate was not reached")),
					3000,
				);
			}),
		]);
	} finally {
		clearTimeout(timeout);
	}
}

const clone = <T>(value: T): T => structuredClone(value);
type Mode = "first" | "second" | "late";
let mode: Mode;
let parentCalls: ChatParams[];
let parentSnapshots: unknown[][];
let reflectionHistories: unknown[][];
let parentHistoryAtReflection: unknown[];
let targetHistory: unknown[] | undefined;
let targetStreamFinished: boolean;
let streamFinishedAtReflection: boolean;
let executed: string[];
let cancellations: Array<{ requestId: string; reason?: string }>;
let permissionEntered: ReturnType<typeof gate>;
let allowReflection: ReturnType<typeof gate>;
let streamHeld: ReturnType<typeof gate>;
let endStream: ReturnType<typeof gate>;
let userCommitted: ReturnType<typeof gate>;
let reflected: ReturnType<typeof gate>;
let cancelled: ReturnType<typeof gate>;
let resolveDecision: ((result: PermissionResult) => void) | undefined;
const pendingDangerReflections = new Map<string, Record<string, unknown>>();

type Block = { type?: string; id?: string; name?: string; text?: string };

/** A minimal wire format over the projected rows: inputs, steps and settled results. */
function turnsOf(row: DbMessage): unknown[] {
	const blocks = Array.isArray(row.contentJson) ? (row.contentJson as Block[]) : [];
	if (row.role !== "assistant") {
		const images = blocks.filter((block) => block.type === "image").length;
		return [{ role: "user", content: row.contentText ?? "", ...(images ? { images } : {}) }];
	}
	const settled = (row.toolCalls ?? []).filter((call) => call.status !== "running");
	return [
		{
			role: "assistant",
			content: blocks.map((block) => (block.type === "text" ? block.text : "")).join(""),
			toolUses: blocks.filter((block) => block.type === "tool_use").map((block) => block.id),
		},
		...(settled.length
			? [
					{
						role: "tool",
						results: settled.map((call) => ({
							toolUseId: call.toolUseId,
							output: outputToText(call.outputJson),
							isError: call.status === "fail",
						})),
					},
				]
			: []),
	];
}

/** The input a request ends with, when its last entry is one. */
function lastInput(history: unknown[]): string | undefined {
	const last = history.at(-1) as { role?: string; content?: string } | undefined;
	return last?.role === "user" ? last.content : undefined;
}

const provider: ProviderAdapter = {
	formatTools: (tools) => tools,
	buildHistory: async (rows) => ({ history: rows.flatMap(turnsOf) }),
	injectSystemPrompt: (history, content) => {
		history.unshift({ role: "system", content });
	},
	async *chat(params) {
		// The real wrapper constructs the reflection prompt; only parent inputs are scripted.
		const input = lastInput(params.history);
		if (input !== undefined && input !== USER_TEXT && input !== INJECTION) {
			reflectionHistories.push(clone(params.history));
			parentHistoryAtReflection = clone(targetHistory ?? []);
			streamFinishedAtReflection = targetStreamFinished;
			reflected.resolve();
			throw new Error(FAILURE);
		}
		params.onRequestStart?.();
		parentCalls.push(params);
		parentSnapshots.push(clone(params.history));
		const turn = parentCalls.length;
		if (mode === "second" && turn === 1) {
			yield {
				toolUses: [{ toolUseId: "tu-previous", name: "Bash", input: { command: "fake previous" } }],
			};
			return;
		}
		if (turn !== (mode === "second" ? 2 : 1)) {
			yield { text: "done" };
			return;
		}
		targetHistory = params.history;
		yield {
			toolUses: [{ toolUseId: "tu-danger", name: "Bash", input: { command: "fake danger" } }],
		};
		streamHeld.resolve();
		await endStream.promise;
		targetStreamFinished = true;
	},
	generate: async () => "",
	generateWithMeta: async () => ({ text: "" }),
	generateWithHistory: async () => "",
};

const realProviderModule = { ...(await import("../provider")) };
const realPermissionModule = { ...(await import("@server/services/narrator-permission")) };
const realSessionStateModule = { ...(await import("@server/services/narrator-session-state")) };
mock.module("../provider", () => ({
	...realProviderModule,
	getProvider: () => provider,
	resolveProviderAndModel: () => ({
		requestedProvider: "test",
		requestedModel: "test:model",
		provider: "test",
		adapter: provider,
		model: "test:model",
	}),
}));
mock.module("@server/services/narrator-session-state", () => ({
	...realSessionStateModule,
	pendingDangerReflections,
}));
mock.module("@server/services/narrator-permission", () => ({
	...realPermissionModule,
	confirmDangerReflection: async () => false,
	cancelDangerReflection: async (requestId: string, reason?: string) => {
		cancellations.push({ requestId, reason });
		const existed = pendingDangerReflections.delete(requestId);
		resolveDecision?.({ behavior: "deny", message: reason });
		cancelled.resolve();
		return existed;
	},
	broadcastDangerReflectionProgress: () => {},
}));
const { agentLoop } = await import("../loop");
const originalBash = toolRegistry.get("Bash");
toolRegistry.register({
	name: "Bash",
	description: "In-memory fake command; never runs a shell",
	parameters: z.object({ command: z.string() }),
	execute: async (input) => {
		executed.push(String(input.command));
		return { output: `fake result: ${input.command}` };
	},
});

afterAll(() => {
	mock.module("../provider", () => realProviderModule);
	mock.module("@server/services/narrator-permission", () => realPermissionModule);
	mock.module("@server/services/narrator-session-state", () => realSessionStateModule);
	if (originalBash) toolRegistry.register(originalBash);
	else toolRegistry.unregister("Bash");
	mock.restore();
});
beforeEach(() => {
	parentCalls = [];
	parentSnapshots = [];
	reflectionHistories = [];
	parentHistoryAtReflection = [];
	targetHistory = undefined;
	targetStreamFinished = false;
	streamFinishedAtReflection = false;
	executed = [];
	cancellations = [];
	permissionEntered = gate();
	allowReflection = gate();
	streamHeld = gate();
	endStream = gate();
	userCommitted = gate();
	reflected = gate();
	cancelled = gate();
	resolveDecision = undefined;
	pendingDangerReflections.clear();
});

function run(images?: Array<{ format: string; base64: string }>) {
	const controller = new AbortController();
	const events: AgentEvent[] = [];
	let injectionDelivered = false;
	const config: AgentConfig = {
		narratorId: NARRATOR_ID,
		conversationId: "conv-stream-reflection-history",
		model: "test:model",
		provider: "test",
		cwd: "/tmp",
		systemPrompt: "SYSTEM_PREFIX",
		maxTurns: 6,
		maxTransientRetries: 0,
		signal: controller.signal,
		getAfterToolsInjections: async () => {
			if (mode !== "second" || injectionDelivered) return "";
			injectionDelivered = true;
			return INJECTION;
		},
		permissionHandler: async (_name, input, toolUseId) => {
			if (toolUseId === "tu-previous") return { behavior: "allow" };
			permissionEntered.resolve();
			if (mode === "late") await allowReflection.promise;
			const decision = new Promise<PermissionResult>((resolve) => {
				resolveDecision = resolve;
			});
			const danger = {
				severity: "high" as const,
				summary: "Fake danger requiring reflection",
				consequences: ["Only a fake tool would execute"],
				saferAlternatives: ["Cancel the fake tool"],
			};
			pendingDangerReflections.set(REQUEST_ID, {
				narratorId: NARRATOR_ID,
				requestId: REQUEST_ID,
				toolCallId: REQUEST_ID,
				toolUseId,
				toolName: "Bash",
				broadcastTargetId: NARRATOR_ID,
				input,
				fingerprint: "fake-fingerprint",
				danger,
				startedAt: Date.now(),
				resolve: () => {},
				cleanup: () => {},
			});
			return {
				behavior: "dangerReflection",
				requestId: REQUEST_ID,
				danger,
				fingerprint: "fake-fingerprint",
				input,
				decision,
			};
		},
	};
	const done = (async () => {
		for await (const event of agentLoop(config, USER_TEXT, { rows: [] }, images)) {
			events.push(event);
			// The turn's input joins the transcript once the model has answered it.
			if (event.type === "assistant_message" && event.toolUses.some((tu) => tu.toolUseId === "tu-danger"))
				userCommitted.resolve();
			if (event.type === "block_complete" && event.block.type === "tool_use") {
				event.onToolPersisted?.({ toolCallId: event.block.toolUseId, attempt: 1 });
			}
		}
	})();
	return { done, events, controller };
}

function expectCancelled(events: AgentEvent[]) {
	expect(reflectionHistories).toHaveLength(1);
	expect(cancellations).toHaveLength(1);
	expect(cancellations[0]?.requestId).toBe(REQUEST_ID);
	expect(cancellations[0]?.reason).toContain("model service request failed");
	expect(cancellations[0]?.reason).toContain("not authorized to execute");
	expect(cancellations[0]?.reason).not.toContain("DangerConfirm");
	expect(pendingDangerReflections.size).toBe(0);
	expect(executed).not.toContain("fake danger");
	expect(events.filter((event) => event.type === "error")).toEqual([]);
	expect(events.some((event) => event.type === "tool_result" && event.isError)).toBe(true);
}

describe("streaming danger reflection history", () => {
	for (const testMode of ["first", "second", "late"] as const) {
		test(`${testMode}: reflection receives the current input exactly once without mutating the active request`, async () => {
			mode = testMode;
			const images = [{ format: "png", base64: "ZmFrZS1pbWFnZQ==" }];
			const runState = run(images);
			let completed = false;
			try {
				await waitFor(streamHeld.promise);
				await waitFor(permissionEntered.promise);
				const targetIndex = mode === "second" ? 1 : 0;
				const snapshot = parentSnapshots[targetIndex];
				if (mode === "late") {
					expect(reflectionHistories).toEqual([]);
					endStream.resolve();
					await waitFor(userCommitted.promise);
					allowReflection.resolve();
				}
				await waitFor(reflected.promise);
				await waitFor(cancelled.promise);
				const reflectionHistory = reflectionHistories[0];
				if (!targetHistory) throw new Error("Parent history was not captured");
				expect(streamFinishedAtReflection).toBe(mode === "late");
				expect(reflectionHistory.slice(0, snapshot.length)).toEqual(snapshot);
				if (mode !== "late") expect(targetStreamFinished).toBe(false);
				// The active request is never written into, before or after its input commits.
				expect(parentCalls[targetIndex].history).toBe(targetHistory);
				expect(parentHistoryAtReflection).toEqual(snapshot);
				expect(targetHistory).toEqual(snapshot);
				// The parent's request ends with its current input, exactly once; the
				// reflection's request is that request plus its own prompt.
				const currentTurn =
					mode === "second"
						? { role: "user", content: INJECTION }
						: { role: "user", content: USER_TEXT, images: 1 };
				expect(snapshot.at(-1)).toEqual(currentTurn);
				const inputs = JSON.stringify(snapshot).split(mode === "second" ? INJECTION : USER_TEXT);
				expect(inputs).toHaveLength(2);
				expect(reflectionHistory).toHaveLength(snapshot.length + 1);
				expect(lastInput(reflectionHistory)).toContain("Fake danger requiring reflection");
				expect(parentCalls[targetIndex].content).toBe("");
				if (mode === "second") {
					expect(snapshot.at(-2)).toEqual({
						role: "tool",
						results: [
							{ toolUseId: "tu-previous", output: "fake result: fake previous", isError: false },
						],
					});
					expect(executed).toEqual(["fake previous"]);
				}
				endStream.resolve();
				await waitFor(runState.done);
				completed = true;
				expectCancelled(runState.events);
			} finally {
				endStream.resolve();
				allowReflection.resolve();
				if (!completed) runState.controller.abort();
				await waitFor(runState.done);
			}
		});
	}
});
