/**
 * Test: when context is near 100%, how much can the model still output?
 *
 * Strategy: fill context to various high percentages, then ask the model
 * to generate a long response (e.g. count from 1 to 1000).
 *
 * Usage: bun scripts/test-context-output-limit.ts [chars]
 */

import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { initKiro, kiroChatRaw, type StreamEvent } from "../server/lib/kiro/index";
import type { KiroChatRequest } from "../server/lib/kiro/types";

// --- Init ---
const settingsPath = resolve(homedir(), ".narrafork", "settings.json");
const settings = JSON.parse(readFileSync(settingsPath, "utf-8"));
const kiroConfig = settings.kiro;
const ok = initKiro({
	credentialsPath: kiroConfig.credentialsPath,
	configPath: kiroConfig.configPath ?? resolve(homedir(), ".narrafork", "kiro", "config.json"),
});
if (!ok) {
	console.error("❌ initKiro failed");
	process.exit(1);
}

const MODEL = "claude-haiku-4.5";
const CHUNK = "The quick brown fox jumps over the lazy dog. ";

// Prompt that encourages long output
const LONG_OUTPUT_PROMPT = `Generate a single-file HTML page with embedded CSS and JavaScript that implements a fully functional Kanban board application. Requirements:
- 4 columns: Backlog, In Progress, Review, Done
- Each card has: title, description, priority (high/medium/low with color coding), assignee avatar, due date, tags
- Drag and drop cards between columns using native HTML5 drag API
- Add new card modal with form validation
- Edit card inline on double-click
- Delete card with confirmation dialog
- Filter cards by priority and search by title
- Card count badge on each column header
- Smooth CSS animations for all transitions
- Dark theme with CSS custom properties
- LocalStorage persistence
- Responsive layout that stacks columns on mobile
- Generate at least 12 sample cards with realistic content
Output ONLY the complete HTML file, no explanation.`;

async function testAtSize(chars: number) {
	const filler = CHUNK.repeat(Math.ceil(chars / CHUNK.length));
	const request: KiroChatRequest = {
		conversationState: {
			conversationId: randomUUID(),
			chatTriggerType: "MANUAL",
			currentMessage: {
				userInputMessage: {
					content: LONG_OUTPUT_PROMPT,
					modelId: MODEL,
					userInputMessageContext: {},
					origin: "AI_EDITOR",
				},
			},
			history: [
				{
					userInputMessage: {
						content: filler,
						modelId: MODEL,
						origin: "AI_EDITOR",
					},
				},
			],
		},
	};

	console.log(`\n${"=".repeat(60)}`);
	console.log(`Filler: ${chars.toLocaleString()} chars`);

	let assistantText = "";
	let contextPercent: number | null = null;
	let hasInvalidState = false;
	let invalidStateData: any = null;
	let eventCount = 0;
	let meteringData: any = null;

	try {
		for await (const evt of kiroChatRaw(request)) {
			eventCount++;
			if (evt.eventType === "assistantResponseEvent" && evt.data.content) {
				assistantText += String(evt.data.content);
			}
			if (evt.eventType === "contextUsageEvent") {
				contextPercent = Number(evt.data.contextUsagePercentage ?? 0);
			}
			if (evt.eventType === "meteringEvent") {
				meteringData = evt.data;
			}
			if (evt.eventType === "invalidStateEvent") {
				hasInvalidState = true;
				invalidStateData = evt.data;
			}
		}
	} catch (err: any) {
		try {
			const parsed = JSON.parse(err.message);
			console.log(`❌ HTTP Error: ${parsed.reason} — ${parsed.message}`);
		} catch {
			console.log(`❌ Error: ${err.message}`);
		}
		return;
	}

	// Check if the HTML looks complete
	const hasClosingHtml = assistantText.includes("</html>");
	const hasClosingScript = assistantText.includes("</script>");
	const scriptTags = (assistantText.match(/<script/g) ?? []).length;
	const closingScriptTags = (assistantText.match(/<\/script>/g) ?? []).length;

	console.log(`📊 Context usage: ${contextPercent?.toFixed(2) ?? "?"}%`);
	console.log(`📝 Output length: ${assistantText.length.toLocaleString()} chars`);
	console.log(
		`🏗️  Complete HTML: ${hasClosingHtml ? "✅" : "❌ TRUNCATED"} | Scripts: ${scriptTags} opened, ${closingScriptTags} closed`,
	);
	if (meteringData) {
		console.log(`💰 Metering: ${JSON.stringify(meteringData)}`);
	}
	if (hasInvalidState) {
		console.log(`🔴 INVALID STATE: ${JSON.stringify(invalidStateData)}`);
	}
	console.log(`   Events: ${eventCount}`);

	// Show last 200 chars to see if it was cut off
	const tail = assistantText.slice(-200);
	console.log(`   Tail: ...${JSON.stringify(tail)}`);
}

// Test at various fill levels
const sizes = [
	700_000, // ~87%
	750_000, // ~93%
	780_000, // ~96%
	795_000, // ~98%
	800_000, // ~99%
	805_000, // ~99.5%
];

const singleSize = Number(process.argv[2]);
if (singleSize) {
	await testAtSize(singleSize);
} else {
	for (const size of sizes) {
		await testAtSize(size);
	}
}

console.log("\n🏁 Done.");
