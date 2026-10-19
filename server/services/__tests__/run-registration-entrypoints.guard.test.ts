import { expect, test } from "bun:test";
import ts from "typescript";

const entryNames = new Set([
	"startNarratorRun",
	"runAgentLoop",
	"continueNarrator",
	"sendMessage",
	"acceptUserMessage",
]);
const recoveryFiles = new Set([
	"server/services/restart-recovery-service.ts",
	"server/services/narrator-subagent-recovery.ts",
	"server/services/update-recovery-service.ts",
]);
function enclosingFunction(node: ts.Node): string {
	for (let parent = node.parent; parent; parent = parent.parent) {
		if (ts.isFunctionDeclaration(parent) && parent.name) return parent.name.text;
		if (ts.isMethodDeclaration(parent) && parent.name) return parent.name.getText();
	}
	return "module";
}

test("every production run entry supplies identity or explicitly relies on its own registration", async () => {
	const unchecked: string[] = [];
	const calls: string[] = [];
	for await (const path of new Bun.Glob("server/**/*.ts").scan(".")) {
		if (path.includes("/__tests__/") || path.endsWith(".test.ts") || path.includes("/generated/"))
			continue;
		const source = await Bun.file(path).text();
		if (
			!/\b(startNarratorRun|runAgentLoop|continueNarrator|sendMessage|acceptUserMessage)\s*\(/.test(
				source,
			)
		)
			continue;
		if (
			path !== "server/services/narrator-session.ts" &&
			!source.includes("narrator-session") &&
			!source.includes("agent-runtime/run")
		)
			continue;
		const file = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true);
		const visit = (node: ts.Node) => {
			if (ts.isCallExpression(node)) {
				const expr = node.expression;
				const name = ts.isIdentifier(expr)
					? expr.text
					: ts.isPropertyAccessExpression(expr) &&
							ts.isIdentifier(expr.expression) &&
							["session", "narratorSession", "sessionService"].includes(expr.expression.text)
						? expr.name.text
						: undefined;
				if (name && entryNames.has(name)) {
					const context = enclosingFunction(node);
					const key = `${path}:${context}:${name}`;
					calls.push(key);
					const args = node.arguments;
					let explicit = false;
					if (name === "startNarratorRun")
						explicit = /\b(principal|binding)\b/.test(args[0]?.getText(file) ?? "");
					if (name === "runAgentLoop")
						explicit = /\bprincipal\b/.test(args[3]?.getText(file) ?? "");
					if (name === "sendMessage")
						explicit = args[6] !== undefined && args[6].getText(file) !== "undefined";
					if (name === "continueNarrator")
						explicit = args[3] !== undefined && args[3].getText(file) !== "undefined";
					if (name === "acceptUserMessage")
						explicit = /\buserId\b/.test(args[2]?.getText(file) ?? "");
					// The prepared-session wrapper passes request options unchanged; the
					// run service also reads the human-prepared active session's identity.
					const preparedWrapper =
						path === "server/services/narrator-session.ts" &&
						context === "runAgentLoop" &&
						name === "startNarratorRun" &&
						args[0]?.getText(file).includes("...options");
					// A background projection registered its principal atomically before
					// this start adopts that exact logical run. Foreground passes binding.
					const adoptedTask =
						path === "server/services/subagent-runner.ts" &&
						context === "runSubagentUnlocked" &&
						name === "startNarratorRun" &&
						args[0]?.getText(file).includes("resumeRunId: task.logicalRunId");
					// Recovery never adopts the recovered tool/notice author's user: it
					// resumes the session's registered identity (or legacy own owner).
					const recovery = name === "continueNarrator" && recoveryFiles.has(path);
					// Restart finishes a committed-but-undelivered checkpoint of the same
					// registered logical run: identity comes from that run's own registration.
					const checkpointResume =
						path === "server/services/background-task-service.ts" &&
						context === "recoverStaleTasksAfterRestart" &&
						name === "startNarratorRun" &&
						/resumeRunId: task\.logicalRunId/.test(args[0]?.getText(file) ?? "") &&
						/recovered: true/.test(args[0]?.getText(file) ?? "");
					if (!explicit && !preparedWrapper && !adoptedTask && !recovery && !checkpointResume)
						unchecked.push(key);
				}
			}
			ts.forEachChild(node, visit);
		};
		visit(file);
	}
	expect(calls.length).toBeGreaterThan(15);
	expect(unchecked).toEqual([]);
});

test("run registration is dispatched through both backend adapters, with no PG SQLite fallback", async () => {
	const source = await Bun.file(new URL("../agent-runtime/publication.ts", import.meta.url)).text();
	const pg = await Bun.file(
		new URL("../agent-runtime/postgres-runtime-publication.ts", import.meta.url),
	).text();
	expect(source).toContain("startRun: async (input) => legacy.startRun(input)");
	expect(source).toContain("startRun: (input) => engine.startRun(input)");
	const method = pg.slice(
		pg.indexOf("async startRun("),
		pg.indexOf("async getAgentRun(", pg.indexOf("async startRun(")),
	);
	expect(method).toContain('runComposite("publication.startRun"');
	expect(method).toContain("readTopRefSeqPg(tx, input.narratorId)");
	expect(method).not.toContain("runAtomicWrite");
});
