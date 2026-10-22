/**
 * Test the Responses API message structure by simulating the agent loop flow.
 * Usage: bun run scripts/test-responses-history.ts
 *
 * Every request the loop sends is the projection of its rows (`loop-transcript.ts`):
 * the conversation so far plus the turn's input, built by the provider's buildHistory,
 * with the system prompt.
 */

import { LoopTranscript } from "../server/lib/agent/loop-transcript";
import { OpenAIProvider } from "../server/lib/agent/openai-provider";
import type { DbMessage } from "../server/lib/agent/provider";
import { toolRegistry } from "../server/lib/agent/tool-registry";
import { registerCoreTools } from "../server/lib/agent/tools";
import { getOpenaiProviderConfig } from "../server/lib/settings";

registerCoreTools();
const providerConfig = getOpenaiProviderConfig();
if (!providerConfig) {
	console.error("❌ No OpenAI provider configured.");
	process.exit(1);
}
const provider = new OpenAIProvider(providerConfig);

console.log("apiMode:", provider.apiMode);

const _tools = provider.formatTools(
	toolRegistry.all().filter((t) => !t.isAvailable || t.isAvailable()),
);

async function request(rows: DbMessage[]): Promise<unknown[]> {
	const { history } = await provider.buildHistory(rows, "gpt-5");
	provider.injectSystemPrompt(
		history,
		"You are a coding assistant.\n\n## CWD\n`/tmp`",
		"gpt-5",
		"zh-CN",
	);
	return history;
}

function print(label: string, messages: unknown[]): void {
	console.log(`\n=== ${label} ===`);
	for (const [i, m] of messages.entries()) {
		const msg = m as Record<string, unknown>;
		const contentPreview = msg.content != null ? JSON.stringify(msg.content).slice(0, 50) : "";
		const outputPreview = msg.output != null ? String(msg.output).slice(0, 50) : "";
		const argsPreview = msg.arguments != null ? String(msg.arguments).slice(0, 50) : "";
		const preview = contentPreview || outputPreview || argsPreview;
		const roleOrType = String(msg.role ?? msg.type ?? "?");
		const name = typeof msg.name === "string" ? msg.name : undefined;
		const callId = typeof msg.call_id === "string" ? msg.call_id : undefined;
		console.log(
			`  [${i}] ${roleOrType} ${name ? `(${name})` : ""} ${callId ? `call_id=${callId}` : ""} ${preview}`,
		);
	}
}

const transcript = new LoopTranscript();

// Turn 0: the user's input.
print("Turn 0: messages sent to API", await request(transcript.withInput("读取 package.json")));

// The model answered with text and a tool call; the input, the step and its result
// become rows.
transcript.appendInput("读取 package.json");
transcript.appendStep([
	{ type: "text", text: "读取 package.json 文件。" },
	{ type: "tool_use", toolUseId: "call_abc123", name: "Read", input: { file_path: "package.json" } },
]);
transcript.settleTool("call_abc123", { output: "file content here..." });
print("Turn 1: messages sent to API", await request(transcript.rows));

// Turn 1 called another tool.
transcript.appendStep([
	{ type: "text", text: "让我再搜索一下。" },
	{ type: "tool_use", toolUseId: "call_def456", name: "Grep", input: { pattern: "test" } },
]);
transcript.settleTool("call_def456", { output: "grep results..." });
const messages = await request(transcript.rows);
print("Turn 2: messages sent to API", messages);

// Validate structure
console.log("\n=== Validation ===");
let valid = true;
for (const [i, m] of messages.entries()) {
	const msg = m as Record<string, unknown>;
	if (msg.role === "tool") {
		console.log(
			`❌ [${i}] Found role="tool" — should be type="function_call_output" in Responses API`,
		);
		valid = false;
	}
	if (msg.role === "system") {
		console.log(`❌ [${i}] Found role="system" — should be role="developer" in Responses API`);
		valid = false;
	}
	if (msg.role === "assistant" && msg.tool_calls != null) {
		console.log(
			`❌ [${i}] Found assistant with tool_calls — should be separate function_call items`,
		);
		valid = false;
	}
}
if (valid) {
	console.log("✅ All messages in correct Responses API format");
}
