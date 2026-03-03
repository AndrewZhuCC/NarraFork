/**
 * Test script: multi-turn conversation to trigger context overflow via invalidStateEvent.
 *
 * The idea: fill context to ~99% in the first turn, then send a second turn
 * with the assistant's response in history, which should push it over the edge.
 *
 * Usage: bun scripts/test-context-overflow-multiturn.ts
 */

import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { initKiro, kiroChatRaw, type StreamEvent } from "../server/lib/kiro/index";
import type { KiroChatRequest, KiroHistoryMessage } from "../server/lib/kiro/types";

// --- Load settings ---
const settingsPath = resolve(homedir(), ".narrafork", "settings.json");
const settings = JSON.parse(readFileSync(settingsPath, "utf-8"));
const kiroConfig = settings.kiro;
if (!kiroConfig?.credentialsPath) {
	console.error("❌ No kiro credentials configured");
	process.exit(1);
}

const ok = initKiro({
	credentialsPath: kiroConfig.credentialsPath,
	configPath: kiroConfig.configPath ?? resolve(homedir(), ".narrafork", "kiro", "config.json"),
});
if (!ok) {
	console.error("❌ initKiro failed");
	process.exit(1);
}
console.log("✅ Kiro initialized\n");

const MODEL = "claude-haiku-4.5";
const CHUNK = "The quick brown fox jumps over the lazy dog. ";
const conversationId = randomUUID();

async function sendTurn(
	content: string,
	history: KiroHistoryMessage[],
	turnLabel: string,
): Promise<{ assistantText: string; contextPercent: number | null; events: StreamEvent[] }> {
	const request: KiroChatRequest = {
		conversationState: {
			conversationId,
			chatTriggerType: "MANUAL",
			currentMessage: {
				userInputMessage: {
					content,
					modelId: MODEL,
					userInputMessageContext: {},
					origin: "AI_EDITOR",
				},
			},
			history,
		},
	};

	console.log(`--- ${turnLabel} ---`);
	console.log(`   History messages: ${history.length}`);
	const historyChars = JSON.stringify(history).length;
	console.log(`   History size: ${historyChars.toLocaleString()} chars`);
	console.log(`   Current message: ${content.length.toLocaleString()} chars`);

	const events: StreamEvent[] = [];
	let assistantText = "";
	let contextPercent: number | null = null;

	try {
		for await (const evt of kiroChatRaw(request)) {
			events.push(evt);

			if (evt.eventType === "assistantResponseEvent" && evt.data.content) {
				assistantText += String(evt.data.content);
			}
			if (evt.eventType === "contextUsageEvent") {
				contextPercent = Number(evt.data.contextUsagePercentage ?? 0);
			}
			if (evt.eventType === "invalidStateEvent") {
				console.log("\n🔴 === INVALID STATE EVENT ===");
				console.log(JSON.stringify(evt.data, null, 2));
				console.log("==============================\n");
			}
			// Log non-content events
			if (evt.eventType !== "assistantResponseEvent" && evt.eventType !== "initial-response") {
				console.log(`   [${evt.eventType}] ${JSON.stringify(evt.data).slice(0, 200)}`);
			}
		}
	} catch (err: unknown) {
		console.log(`\n🔴 ERROR in ${turnLabel}:`);
		const name = err instanceof Error ? err.name : "Error";
		const message = err instanceof Error ? err.message : String(err);
		console.log(`   Name: ${name}`);
		console.log(`   Code: ${String((err as { code?: unknown })?.code ?? "")}`);
		// Try to parse the message as JSON for readability
		try {
			const parsed = JSON.parse(message) as { __type?: string; message?: string; reason?: string };
			console.log(`   Type: ${parsed.__type ?? "unknown"}`);
			console.log(`   Message: ${parsed.message ?? message}`);
			console.log(`   Reason: ${parsed.reason ?? "unknown"}`);
		} catch {
			console.log(`   Message: ${message}`);
		}
		if ((err as { statusCode?: unknown })?.statusCode) {
			console.log(`   Status: ${String((err as { statusCode?: unknown }).statusCode)}`);
		}
	}

	console.log(
		`   Assistant response: "${assistantText.slice(0, 100)}${assistantText.length > 100 ? "..." : ""}"`,
	);
	if (contextPercent !== null) {
		console.log(`   📊 Context usage: ${contextPercent.toFixed(1)}%`);
	}
	console.log(`   Total events: ${events.length}\n`);

	return { assistantText, contextPercent, events };
}

// === Turn 1: Fill context to ~99% ===
const bigText = CHUNK.repeat(Math.ceil(805_000 / CHUNK.length));
const turn1 = await sendTurn(bigText, [], "Turn 1 (fill to ~99%)");

// === Turn 2: Add more content to push over ===
// History now includes turn 1's user message + assistant response
const history2: KiroHistoryMessage[] = [
	{
		userInputMessage: {
			content: bigText,
			modelId: MODEL,
			origin: "AI_EDITOR",
		},
	},
	{
		assistantResponseMessage: {
			content: turn1.assistantText,
		},
	},
];

// Send another big message to push it over
const turn2Extra = CHUNK.repeat(200); // ~9200 chars extra
const turn2 = await sendTurn(
	`Here is more text: ${turn2Extra}\n\nPlease summarize everything.`,
	history2,
	"Turn 2 (push over 100%)",
);

// === Turn 3: Even more if turn 2 didn't overflow ===
if (turn2.assistantText) {
	const history3: KiroHistoryMessage[] = [
		...history2,
		{
			userInputMessage: {
				content: `Here is more text: ${turn2Extra}\n\nPlease summarize everything.`,
				modelId: MODEL,
				origin: "AI_EDITOR",
			},
		},
		{
			assistantResponseMessage: {
				content: turn2.assistantText,
			},
		},
	];

	const turn3Extra = CHUNK.repeat(500); // ~23000 chars more
	await sendTurn(
		`Even more text: ${turn3Extra}\n\nSummarize all of the above.`,
		history3,
		"Turn 3 (definitely overflow)",
	);
}

console.log("🏁 Done.");
