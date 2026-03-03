/**
 * Test script: send a massive message to Kiro API to see what happens
 * when the context window is exceeded.
 *
 * Usage: bun scripts/test-context-overflow.ts
 */

import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { buildChatRequest, initKiro, kiroChatRaw } from "../server/lib/kiro/index";

// --- Load settings to find kiro config ---
const settingsPath = resolve(homedir(), ".narrafork", "settings.json");
if (!existsSync(settingsPath)) {
	console.error("❌ No settings.json found at", settingsPath);
	process.exit(1);
}
const settings = JSON.parse(readFileSync(settingsPath, "utf-8"));
const kiroConfig = settings.kiro;
if (!kiroConfig?.credentialsPath) {
	console.error("❌ No kiro credentials configured in settings.json");
	process.exit(1);
}

console.log("📋 Kiro config:", {
	credentialsPath: kiroConfig.credentialsPath,
	configPath: kiroConfig.configPath,
});

// --- Init kiro ---
const ok = initKiro({
	credentialsPath: kiroConfig.credentialsPath,
	configPath: kiroConfig.configPath ?? resolve(homedir(), ".narrafork", "kiro", "config.json"),
});
if (!ok) {
	console.error("❌ initKiro failed — check credentials");
	process.exit(1);
}
console.log("✅ Kiro initialized\n");

// --- Build messages at different sizes to find the boundary ---
// The 1M char message got HTTP 400 "Input is too long" (CONTENT_LENGTH_EXCEEDS_THRESHOLD).
// Now try smaller sizes to see if we can get an invalidStateEvent in the stream instead.
// Kiro's context window might be ~128k-200k tokens. Try ~600k chars (~150k tokens).
const CHUNK = "The quick brown fox jumps over the lazy dog. "; // 46 chars

// Try multiple sizes
const sizes = [600_000, 400_000, 200_000];
const targetSize = Number(process.argv[2]) || sizes[0];

const repeats = Math.ceil(targetSize / CHUNK.length);
const hugeText = CHUNK.repeat(repeats);

console.log(
	`📏 Built message: ${hugeText.length.toLocaleString()} chars (~${Math.round(hugeText.length / 4).toLocaleString()} tokens)`,
);
console.log(
	`   (pass a number as arg to try different sizes, e.g.: bun scripts/test-context-overflow.ts 300000)`,
);
console.log("🚀 Sending to Kiro API...\n");

// Strategy: put the huge text as a single user message in history,
// then send a short current message.
const request = buildChatRequest("Summarize the above text in one sentence.", "claude-haiku-4.5");
request.conversationState.history = [
	{
		userInputMessage: {
			content: hugeText,
			modelId: "claude-haiku-4.5",
			origin: "AI_EDITOR",
		},
	},
];

// --- Stream and log every event ---
try {
	let eventCount = 0;
	for await (const evt of kiroChatRaw(request)) {
		eventCount++;
		// Pretty print each event
		const dataPreview = JSON.stringify(evt.data).slice(0, 500);
		console.log(`[${eventCount}] ${evt.eventType}: ${dataPreview}`);

		// Highlight the interesting ones
		if (evt.eventType === "invalidStateEvent") {
			console.log("\n🔴 === INVALID STATE EVENT ===");
			console.log(JSON.stringify(evt.data, null, 2));
			console.log("==============================\n");
		}
		if (evt.eventType === "contextUsageEvent") {
			console.log(`   📊 Context usage: ${JSON.stringify(evt.data)}`);
		}
	}
	console.log(`\n✅ Stream ended normally. Total events: ${eventCount}`);
} catch (err: unknown) {
	console.log("\n🔴 === ERROR CAUGHT ===");
	const name = err instanceof Error ? err.name : "Error";
	const message = err instanceof Error ? err.message : String(err);
	console.log("Name:", name);
	console.log("Code:", (err as { code?: unknown })?.code ?? "");
	console.log("Message:", message);
	if ((err as { statusCode?: unknown })?.statusCode) {
		console.log("Status:", (err as { statusCode?: unknown }).statusCode);
	}
	console.log("Full error:", err);
	console.log("======================\n");
}
