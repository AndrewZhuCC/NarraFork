const root = new URL("../", import.meta.url);

async function source(path: string): Promise<string> {
	return Bun.file(new URL(path, root)).text();
}

function assert(condition: boolean, message: string): void {
	if (!condition) throw new Error(message);
	console.log(`ok: ${message}`);
}

const [loop, reflectionTest, settlementTest, schedulingTest, permission] = await Promise.all([
	source("server/lib/agent/loop.ts"),
	source("server/lib/agent/__tests__/loop-reflection-eager.test.ts"),
	source("server/lib/agent/__tests__/loop-tool-settlement.test.ts"),
	source("server/lib/agent/__tests__/loop-stream-scheduling.test.ts"),
	source("server/services/narrator-permission.ts"),
]);

assert(
	/SHELL_TOOL_NAME,\s*\n\s*"Write",\s*\n\s*"Edit",\s*\n\s*"StructSed",/.test(loop),
	"Bash, Write, Edit, and StructSed are post-stream tools",
);
assert(!loop.includes("tu.name !== SHELL_TOOL_NAME"), "Bash has no eager exception");
assert(
	!loop.includes('tu.input.file_path.startsWith("spec://")'),
	"Write/Edit have no redundant spec-only eager branch",
);
assert(
	permission.includes('toolName === "StructSed" && input.dry_run !== false'),
	"StructSed dry runs remain read-only while dry_run:false is mutating",
);
assert(
	reflectionTest.includes(
		'const PROVIDER_SESSION_TOOLS = ["Bash", "Write", "Edit", "StructSed"] as const;',
	),
	"mock-provider regression covers every provider-session tool",
);
assert(
	reflectionTest.includes("permission and execution start after provider finally"),
	"permission and execution are asserted after provider finally",
);
assert(reflectionTest.includes("ordinary Read remains eager"), "ordinary Read remains eager");
assert(
	settlementTest.includes("waits for stream end before subsequent Read"),
	"Bash settlement assertion uses the post-stream boundary",
);
assert(
	schedulingTest.includes('const EAGER_WRITE_TOOL = "TestEagerWrite";') &&
		schedulingTest.includes('const EAGER_EDIT_TOOL = "TestEagerEdit";'),
	"stream-pump serial and abort tests remain on synthetic eager tools",
);
