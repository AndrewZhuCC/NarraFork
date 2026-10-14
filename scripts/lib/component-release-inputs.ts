import { createHash } from "node:crypto";
import { posix } from "node:path";
import { parse } from "@babel/parser";
import {
	createHelperDistributionDependencies,
	type HelperDistributionDependencies,
	parseComponentVersions,
	parseHelperDistributionDependencies,
} from "../../shared/helper-distribution";
import { updateServerChildEnvironment } from "../../shared/update-server-child-env";
import { type GhRunner, validateGitHubRepository } from "./github-release";
import { resolveHelperTagCommit, verifyPublishedHelperDependencies } from "./helper-release";

export interface ComponentReleaseEvidence {
	executor: {
		version: string;
		protocolVersion: number;
		tag: string;
		commit: string;
		manifestSha256: string;
		buildInputsSha256: string;
	};
}
export interface ComponentReleaseInputOptions {
	repository: string;
	targetCommit: string;
	dependencies?: HelperDistributionDependencies;
	run?: GhRunner;
	signal?: AbortSignal;
	/** Absolute parent deadline, in milliseconds since epoch. */
	deadline?: number;
}
const MAX_ENTRIES = 4096;
const MAX_TEXT = 2 * 1024 * 1024;
const MAX_TOTAL = 16 * 1024 * 1024;
const MAX_OUTPUT = 1024 * 1024;
const REQUEST_MS = 30_000;
const sha256 = (text: string | Uint8Array) => createHash("sha256").update(text).digest("hex");
function record(value: unknown): Record<string, unknown> {
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error("Invalid component source metadata");
	return value as Record<string, unknown>;
}
function exactKeys(value: Record<string, unknown>, allowed: string[]): void {
	if (Object.keys(value).some((key) => !allowed.includes(key)))
		throw new Error("Unknown executor build-affecting field");
}

/** Own async gh transport: never execute repository scripts; bounded streams and 30s requests. */
async function nativeGh(args: string[], signal: AbortSignal): Promise<string> {
	const proc = Bun.spawn(["gh", ...args], {
		stdout: "pipe",
		stderr: "pipe",
		stdin: "ignore",
		env: { ...updateServerChildEnvironment(), GH_HOST: "github.com", GH_PROMPT_DISABLED: "1" },
	});
	const cancel = () => proc.kill();
	signal.addEventListener("abort", cancel, { once: true });
	if (signal.aborted) cancel();
	const consume = async (stream: ReadableStream<Uint8Array>, maximum: number) => {
		const chunks: Uint8Array[] = [];
		let size = 0;
		for await (const chunk of stream) {
			size += chunk.length;
			if (size > maximum) {
				proc.kill();
				throw new Error("Component GitHub output exceeds limit");
			}
			chunks.push(chunk);
		}
		return Buffer.concat(chunks, size).toString("utf8");
	};
	try {
		const [stdout, stderr, exit] = await Promise.all([
			consume(proc.stdout, MAX_OUTPUT),
			consume(proc.stderr, 8192),
			proc.exited,
		]);
		signal.throwIfAborted();
		if (exit !== 0) throw new Error(`gh failed: ${stderr}`);
		return stdout;
	} finally {
		signal.removeEventListener("abort", cancel);
		if (proc.exitCode === null) proc.kill();
	}
}
class SourceReader {
	entries = 0;
	bytes = 0;
	readonly signal: AbortSignal;
	readonly trees = new Map<string, Promise<TreeEntry[]>>();
	readonly blobs = new Map<string, Promise<Buffer>>();
	constructor(readonly options: ComponentReleaseInputOptions) {
		validateGitHubRepository(options.repository);
		if (!/^[a-f0-9]{40}$/.test(options.targetCommit))
			throw new Error("Expected exact component target commit");
		const deadline = options.deadline ?? Date.now() + 5 * 60_000;
		if (!Number.isFinite(deadline) || deadline <= Date.now())
			throw new Error("Component source deadline exceeded");
		this.signal = AbortSignal.any([
			...(options.signal ? [options.signal] : []),
			AbortSignal.timeout(Math.max(0, deadline - Date.now())),
		]);
	}
	async call(args: string[]): Promise<string> {
		this.signal.throwIfAborted();
		const signal = AbortSignal.any([this.signal, AbortSignal.timeout(REQUEST_MS)]);
		let cancel: (() => void) | undefined;
		try {
			const pending = this.options.run
				? Promise.resolve(
						this.options.run(args, { signal, timeoutMs: REQUEST_MS, maxOutputBytes: MAX_OUTPUT }),
					)
				: nativeGh(args, signal);
			const text = await Promise.race([
				pending,
				new Promise<never>((_, reject) => {
					cancel = () => reject(signal.reason);
					signal.addEventListener("abort", cancel, { once: true });
					if (signal.aborted) cancel();
				}),
			]);
			signal.throwIfAborted();
			if (Buffer.byteLength(text) > MAX_OUTPUT)
				throw new Error("Component GitHub output exceeds limit");
			return text;
		} finally {
			if (cancel) signal.removeEventListener("abort", cancel);
		}
	}
	run: GhRunner = (args) => this.call(args);
	async tree(sha: string): Promise<TreeEntry[]> {
		let pending = this.trees.get(sha);
		if (!pending) {
			pending = this.readTree(sha);
			this.trees.set(sha, pending);
		}
		return pending;
	}
	private async readTree(sha: string): Promise<TreeEntry[]> {
		const data = record(
			JSON.parse(await this.call(["api", `repos/${this.options.repository}/git/trees/${sha}`])),
		);
		if (data.truncated !== false || !Array.isArray(data.tree))
			throw new Error("Truncated/invalid component source tree");
		this.entries += data.tree.length;
		if (this.entries > MAX_ENTRIES) throw new Error("Component source exceeds 4096 entries");
		const names = new Set<string>();
		return data.tree.map((value) => {
			const entry = record(value);
			if (
				typeof entry.path !== "string" ||
				!/^[^/\\\0]+$/.test(entry.path) ||
				entry.path === "." ||
				entry.path === ".." ||
				typeof entry.sha !== "string" ||
				!/^[a-f0-9]{40}$/.test(entry.sha) ||
				(entry.type !== "tree" && entry.type !== "blob" && entry.type !== "commit") ||
				(entry.type === "tree" && entry.mode !== "040000") ||
				(entry.type === "commit" && entry.mode !== "160000") ||
				(entry.type === "blob" && !["100644", "100755", "120000"].includes(String(entry.mode)))
			)
				throw new Error("Unsupported component tree entry");
			if (names.has(entry.path as string)) throw new Error("Duplicate component tree entry");
			names.add(entry.path as string);
			return entry as unknown as TreeEntry;
		});
	}
	async find(commit: string, path: string): Promise<TreeEntry | undefined> {
		let sha = commit;
		const parts = path.split("/");
		for (const [index, part] of parts.entries()) {
			const entry = (await this.tree(sha)).find((value) => value.path === part);
			if (!entry) return undefined;
			if (index === parts.length - 1) return entry;
			if (entry.type !== "tree") throw new Error("Invalid component source directory");
			sha = entry.sha;
		}
		return undefined;
	}
	async text(commit: string, path: string, optional = false): Promise<string | undefined> {
		const entry = await this.find(commit, path);
		if (!entry) {
			if (optional) return undefined;
			throw new Error(`Missing component build input: ${path}`);
		}
		return this.blob(entry);
	}
	async blob(entry: TreeEntry): Promise<string> {
		const text = new TextDecoder("utf-8", { fatal: true }).decode(await this.rawBlob(entry));
		if (text.includes("\0")) throw new Error("Non-text component build input");
		return text;
	}
	async rawBlob(entry: TreeEntry): Promise<Buffer> {
		if (
			entry.type !== "blob" ||
			!["100644", "100755"].includes(entry.mode) ||
			!Number.isSafeInteger(entry.size) ||
			entry.size < 0 ||
			entry.size > MAX_TEXT
		)
			throw new Error("Component input exceeds 2MiB / invalid blob");
		let pending = this.blobs.get(entry.sha);
		if (!pending) {
			this.bytes += entry.size;
			if (this.bytes > MAX_TOTAL) throw new Error("Component inputs exceed 16MiB");
			pending = this.readBlob(entry);
			this.blobs.set(entry.sha, pending);
		}
		return pending;
	}
	private async readBlob(entry: TreeEntry): Promise<Buffer> {
		// Slice gh's jq output: a 2MiB input must not overflow the shared 1MiB runner.
		let base64 = "";
		const length = Math.ceil(entry.size / 3) * 4;
		for (let start = 0; start < Math.max(1, length); start += 512 * 1024) {
			const chunk = (
				await this.call([
					"api",
					`repos/${this.options.repository}/git/blobs/${entry.sha}`,
					"--jq",
					`.content | gsub("\\n"; "") | .[${start}:${Math.min(length, start + 512 * 1024)}]`,
				])
			).trim();
			if (!/^[A-Za-z0-9+/]*={0,2}$/.test(chunk)) throw new Error("Invalid component blob encoding");
			base64 += chunk;
		}
		const bytes = Buffer.from(base64, "base64");
		const gitSha = createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
		if (bytes.length !== entry.size || gitSha !== entry.sha)
			throw new Error("Component source blob identity mismatch");
		return bytes;
	}
	async files(commit: string, directory: string): Promise<Map<string, TreeEntry>> {
		const root = await this.find(commit, directory);
		if (!root || root.type !== "tree") throw new Error("Missing executor source directory");
		const files = new Map<string, TreeEntry>();
		const visit = async (sha: string, prefix: string, depth: number) => {
			if (depth > 32) throw new Error("Component directory depth exceeds limit");
			for (const entry of await this.tree(sha)) {
				const path = `${prefix}/${entry.path}`;
				if (entry.type === "tree") await visit(entry.sha, path, depth + 1);
				else files.set(path, entry);
			}
		};
		await visit(root.sha, directory, 0);
		return files;
	}
}
interface TreeEntry {
	path: string;
	sha: string;
	type: "tree" | "blob" | "commit";
	mode: string;
	size: number;
}

async function targetDependencies(
	reader: SourceReader,
): Promise<HelperDistributionDependencies | undefined> {
	const raw = await reader.text(
		reader.options.targetCommit,
		"shared/component-versions.json",
		true,
	);
	if (raw === undefined) return undefined;
	const pin = parseComponentVersions(JSON.parse(raw));
	return createHelperDistributionDependencies(pin.executor.version, pin.executor.protocolVersion);
}
export async function readTargetComponentDependencies(
	options: ComponentReleaseInputOptions,
): Promise<HelperDistributionDependencies | undefined> {
	return targetDependencies(new SourceReader(options));
}

// Babel AST data only. Ignore positions/comments; follow the executor-specific declaration closure.
function canonical(value: unknown): unknown {
	if (Array.isArray(value)) return value.map(canonical);
	if (!value || typeof value !== "object") return value;
	const node = value as Record<string, unknown>;
	if (node.type === "TSAsExpression" || node.type === "TSSatisfiesExpression")
		return canonical(node.expression);
	return Object.fromEntries(
		Object.entries(value)
			.filter(
				([key]) =>
					![
						"start",
						"end",
						"loc",
						"extra",
						"leadingComments",
						"trailingComments",
						"innerComments",
						"typeAnnotation",
						"returnType",
						"typeParameters",
					].includes(key),
			)
			.map(([key, item]) => [key, canonical(item)]),
	);
}
function staticInitializer(value: unknown): boolean {
	if (value === null) return true;
	if (!value || typeof value !== "object") return false;
	const node = value as Record<string, unknown>;
	if (
		["StringLiteral", "NumericLiteral", "BooleanLiteral", "NullLiteral"].includes(String(node.type))
	)
		return true;
	if (node.type === "TSAsExpression" || node.type === "TSSatisfiesExpression")
		return staticInitializer(node.expression);
	if (node.type === "UnaryExpression")
		return (
			["+", "-", "!", "~", "void"].includes(String(node.operator)) &&
			staticInitializer(node.argument)
		);
	if (node.type === "ArrayExpression")
		return Array.isArray(node.elements) && node.elements.every(staticInitializer);
	if (node.type === "ObjectExpression")
		return (
			Array.isArray(node.properties) &&
			node.properties.every((property) => {
				const entry = record(property);
				return (
					entry.type === "ObjectProperty" &&
					entry.computed === false &&
					staticInitializer(entry.value)
				);
			})
		);
	return false;
}
function recipeSlice(source: string, roots: string[]): unknown {
	const ast = parse(source, { sourceType: "module", plugins: ["typescript"] });
	const declarations = new Map<string, unknown>();
	for (const statement of ast.program.body) {
		const node = statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
		if (!node) throw new Error("Unsupported executor recipe export");
		if (node.type === "ImportDeclaration") {
			if (!node.specifiers.length) throw new Error("Executor recipe side-effect import");
			for (const spec of node.specifiers)
				declarations.set(spec.local.name, {
					source: node.source.value,
					specifier: canonical(spec),
				});
		} else if (
			node.type === "FunctionDeclaration" ||
			node.type === "TSTypeAliasDeclaration" ||
			node.type === "TSInterfaceDeclaration"
		) {
			if (node.id) declarations.set(node.id.name, node);
		} else if (node.type === "VariableDeclaration") {
			for (const item of node.declarations) {
				if (item.id.type !== "Identifier") throw new Error("Unsupported executor recipe binding");
				// Module-level execution could alter process.env or the build function itself.
				if (item.init && !staticInitializer(item.init))
					throw new Error("Unknown executor recipe module initialization");
				declarations.set(item.id.name, item);
			}
		} else throw new Error("Unknown executor recipe module side effect");
	}
	const selected = new Map<string, unknown>();
	const visit = (name: string) => {
		if (selected.has(name)) return;
		const node = declarations.get(name);
		if (!node) throw new Error(`Missing executor recipe declaration: ${name}`);
		const imported = record(node);
		if (
			typeof imported.source === "string" &&
			!["node:fs/promises", "node:path", "../../shared/remote-executor"].includes(imported.source)
		)
			throw new Error("Unknown executor recipe dependency");
		selected.set(name, canonical(node));
		const walk = (item: unknown) => {
			if (!item || typeof item !== "object") return;
			if (Array.isArray(item)) {
				for (const child of item) walk(child);
				return;
			}
			const object = item as Record<string, unknown>;
			if (
				object.type === "Identifier" &&
				typeof object.name === "string" &&
				declarations.has(object.name)
			)
				visit(object.name);
			for (const [key, child] of Object.entries(object))
				if (
					![
						"loc",
						"leadingComments",
						"trailingComments",
						"innerComments",
						"typeAnnotation",
						"returnType",
						"typeParameters",
					].includes(key)
				)
					walk(child);
		};
		walk(node);
	};
	for (const root of roots) visit(root);
	return [...selected].sort(([a], [b]) => a.localeCompare(b));
}
function workflowRecipe(source: string): unknown {
	const workflow = record(Bun.YAML.parse(source));
	if (workflow.env !== undefined) throw new Error("Unknown executor workflow environment");
	const build = record(record(workflow.jobs).build);
	exactKeys(build, [
		"name",
		"needs",
		"if",
		"runs-on",
		"timeout-minutes",
		"strategy",
		"steps",
		"permissions",
	]);
	const strategy = record(build.strategy);
	exactKeys(strategy, ["fail-fast", "matrix", "max-parallel"]);
	const matrix = record(strategy.matrix);
	exactKeys(matrix, ["include"]);
	if (!Array.isArray(matrix.include) || matrix.include.length !== 6)
		throw new Error("Invalid executor build platform matrix");
	for (const item of matrix.include) exactKeys(record(item), ["platform", "buildRunner"]);
	if (!Array.isArray(build.steps)) throw new Error("Missing executor build steps");
	const steps: unknown[] = [];
	let go = 0;
	let recipe = 0;
	for (const value of build.steps) {
		const step = record(value);
		if (
			typeof step.if === "string" &&
			/^\$\{\{\s*needs\.preflight\.outputs\.kind\s*==\s*'helpers'\s*\}\}$/.test(step.if)
		)
			continue;
		exactKeys(step, ["name", "if", "uses", "with", "run", "env"]);
		if (typeof step.uses === "string") {
			if (step.env !== undefined || step.run !== undefined)
				throw new Error("Unknown executor action environment/command");
			if (
				!/^(actions\/(checkout|download-artifact|upload-artifact)|oven-sh\/setup-bun|actions\/setup-go)@[a-f0-9]{40}$/.test(
					step.uses,
				)
			)
				throw new Error("Unknown executor build action");
			if (step.uses.startsWith("actions/setup-go@")) {
				go++;
				const settings = record(step.with);
				exactKeys(settings, ["go-version", "check-latest", "cache"]);
				if (
					!/^\d+\.\d+\.\d+$/.test(String(settings["go-version"])) ||
					settings["check-latest"] !== false ||
					settings.cache !== false
				)
					throw new Error("Executor Go toolchain must be fixed");
				steps.push({ uses: step.uses, with: settings, if: step.if });
			} else if (step.uses.startsWith("oven-sh/setup-bun@")) {
				const settings = record(step.with);
				exactKeys(settings, ["bun-version"]);
				if (!/^\d+\.\d+\.\d+$/.test(String(settings["bun-version"])))
					throw new Error("Executor control Bun version must be fixed");
				// Bun executes the separately verified control recipe; Go compiles the executor.
				// Updating that control runtime alone does not change the pinned Go artifact.
			} else if (!step.uses.startsWith("actions/upload-artifact@")) {
				steps.push({ uses: step.uses, with: step.with, if: step.if });
			}
		} else if (
			typeof step.run === "string" &&
			/^bun \.helper-release\/control\/control\.js build\s/.test(step.run)
		) {
			recipe++;
			const env = record(step.env);
			exactKeys(env, ["PLATFORM", "CONTAINER_RUNTIME", "DEVELOPER_DIR"]);
			steps.push({ run: step.run, env: { PLATFORM: env.PLATFORM }, if: step.if });
		} else throw new Error("Unknown executor build step");
	}
	if (go !== 1 || recipe !== 1) throw new Error("Incomplete executor workflow recipe");
	return { runner: build["runs-on"], matrix, steps, defaults: workflow.defaults };
}

/** Small lexical Go scan: import statements only, not strings/comments in function bodies. */
function goTokens(source: string): string[] {
	return (
		source.match(/\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|`[^`]*`|[A-Za-z_]\w*|\d+|[^\s]/g) ??
		[]
	).filter((token) => !token.startsWith("//") && !token.startsWith("/*"));
}
function goImports(source: string): string[] {
	const clean = goTokens(source);
	const imports: string[] = [];
	for (let i = 0; i < clean.length; i++) {
		if (clean[i] !== "import") continue;
		const group = clean[++i] === "(";
		for (; i < clean.length; i++) {
			const token = clean[i] as string;
			if (token.startsWith('"') || token.startsWith("`")) {
				imports.push(token.startsWith('"') ? JSON.parse(token) : token.slice(1, -1));
				if (!group) break;
			}
			if (group && token === ")") break;
		}
	}
	return imports;
}
async function buildInputs(
	reader: SourceReader,
	commit: string,
	protocolVersion: number,
): Promise<string> {
	const files = await reader.files(commit, "remote-executor");
	for (const path of [
		"go.work",
		"go.work.sum",
		"remote-executor/go.work",
		"remote-executor/go.work.sum",
	]) {
		if (await reader.find(commit, path)) throw new Error("Unsupported executor workspace input");
	}
	if (
		[...files.keys()].some(
			(path) => path === "remote-executor/vendor" || path.startsWith("remote-executor/vendor/"),
		)
	)
		throw new Error("Unsupported executor vendored inputs");
	const inputs = new Map<string, string>();
	for (const name of ["go.mod", "go.sum"])
		inputs.set(
			`remote-executor/${name}`,
			(await reader.text(commit, `remote-executor/${name}`)) as string,
		);
	const module = inputs.get("remote-executor/go.mod")?.match(/^module\s+(\S+)/m)?.[1];
	if (!module || /(?:=>\s*[./]|^replace\s*\()/m.test(inputs.get("remote-executor/go.mod") ?? ""))
		throw new Error("Unsupported executor local module replacement");
	const pending = ["remote-executor/cmd/narrafork-executor"];
	const visited = new Set<string>();
	while (pending.length) {
		const directory = pending.pop() as string;
		if (visited.has(directory)) continue;
		visited.add(directory);
		const sources = [...files].filter(
			([path]) =>
				posix.dirname(path) === directory && path.endsWith(".go") && !path.endsWith("_test.go"),
		);
		if (!sources.length) throw new Error(`Missing executor production package: ${directory}`);
		for (const [path, entry] of sources) {
			const text = await reader.blob(entry);
			inputs.set(path, text);
			for (const dependency of goImports(text)) {
				if (dependency === module) pending.push("remote-executor");
				else if (dependency.startsWith(`${module}/`))
					pending.push(`remote-executor/${dependency.slice(module.length + 1)}`);
			}
			for (const directive of text.matchAll(/^\s*\/\/go:embed\s+([^\n]+)/gm)) {
				const patterns = directive[1]?.match(/"(?:\\.|[^"\\])*"|`[^`]*`|[^\s]+/g) ?? [];
				for (let pattern of patterns) {
					if (pattern.startsWith('"')) pattern = JSON.parse(pattern);
					else if (pattern.startsWith("`")) pattern = pattern.slice(1, -1);
					const all = pattern.startsWith("all:");
					if (all) pattern = pattern.slice(4);
					if (
						!pattern ||
						pattern.startsWith("/") ||
						pattern.split("/").some((part) => part === ".." || part === ".") ||
						pattern.includes("\\")
					)
						throw new Error("Unsupported executor embed pattern");
					const glob = new Bun.Glob(pattern);
					let matches = 0;
					for (const [resource, metadata] of files) {
						if (!resource.startsWith(`${directory}/`)) continue;
						const relative = resource.slice(directory.length + 1);
						if (!all && relative.split("/").some((part) => /^[._]/.test(part))) continue;
						const parts = relative.split("/");
						if (!parts.some((_, index) => glob.match(parts.slice(0, index + 1).join("/"))))
							continue;
						matches++;
						inputs.set(resource, `sha256:${sha256(await reader.rawBlob(metadata))}`);
					}
					if (!matches) throw new Error("Executor embed pattern has no resources");
				}
			}
		}
		// Native/assembler input, if present in a reachable production package.
		for (const [path, entry] of files)
			if (posix.dirname(path) === directory && /\.(s|S|c|h|cc|cpp|m|syso)$/.test(path))
				inputs.set(path, `sha256:${sha256(await reader.rawBlob(entry))}`);
	}
	const protocolTokens = goTokens(inputs.get("remote-executor/internal/rpc/protocol.go") ?? "");
	const protocolAt = protocolTokens.findIndex(
		(token, index) => token === "const" && protocolTokens[index + 1] === "ProtocolVersion",
	);
	const literal = protocolTokens[protocolAt + 3];
	const next = protocolTokens[protocolAt + 4];
	if (
		protocolAt < 0 ||
		protocolTokens[protocolAt + 2] !== "=" ||
		!/^\d+$/.test(literal ?? "") ||
		(next !== undefined && !["const", "var", "type", "func", ";"].includes(next)) ||
		Number(literal) !== protocolVersion
	)
		throw new Error("Executor source protocol does not match component pin");
	const helper = (await reader.text(commit, "scripts/lib/helper-build.ts")) as string;
	const contract = (await reader.text(commit, "shared/remote-executor.ts")) as string;
	inputs.set(
		"executor-recipe",
		JSON.stringify(recipeSlice(helper, ["buildExecutorPlatform", "executorPlatform"])),
	);
	inputs.set(
		"executor-platforms",
		JSON.stringify(recipeSlice(contract, ["EXECUTOR_PLATFORMS", "executorPublishedFilename"])),
	);
	inputs.set(
		"executor-toolchain",
		JSON.stringify(
			workflowRecipe(
				(await reader.text(commit, ".github/workflows/helpers-release.yml")) as string,
			),
		),
	);
	return sha256(JSON.stringify([...inputs].sort(([a], [b]) => a.localeCompare(b))));
}

export async function collectComponentReleaseEvidence(
	options: ComponentReleaseInputOptions,
): Promise<ComponentReleaseEvidence | undefined> {
	const reader = new SourceReader(options);
	const dependencies = await targetDependencies(reader);
	if (!dependencies) return undefined; // historical target: do not invent today's control pin
	if (
		!options.dependencies ||
		JSON.stringify(parseHelperDistributionDependencies(options.dependencies)) !==
			JSON.stringify(dependencies)
	)
		throw new Error("Release dependencies do not match target component pin");
	const serverProtocol = parse(
		(await reader.text(options.targetCommit, "server/lib/agent/execution/rpc-types.ts")) as string,
		{ sourceType: "module", plugins: ["typescript"] },
	)
		.program.body.flatMap((statement) => {
			const declaration =
				statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
			return declaration?.type === "VariableDeclaration" ? declaration.declarations : [];
		})
		.find(
			(declaration) =>
				declaration.id.type === "Identifier" && declaration.id.name === "DEVICE_PROTOCOL_VERSION",
		)?.init;
	if (
		serverProtocol?.type !== "NumericLiteral" ||
		serverProtocol.value !== dependencies.executor.protocolVersion
	)
		throw new Error("Application RPC protocol does not match target component pin");
	const verified = await verifyPublishedHelperDependencies({
		repository: options.repository,
		helpersTag: dependencies.helpers.tag,
		executorVersion: dependencies.executor.version,
		protocolVersion: dependencies.executor.protocolVersion,
		run: reader.run,
		signal: reader.signal,
	});
	const target = await buildInputs(
		reader,
		options.targetCommit,
		dependencies.executor.protocolVersion,
	);
	const component = await buildInputs(
		reader,
		verified.executor.commit,
		dependencies.executor.protocolVersion,
	);
	if (target !== component)
		throw new Error(
			"Executor build inputs changed: bump the component pin and publish a separate executor release",
		);
	if (
		(await resolveHelperTagCommit(
			{ repository: options.repository, run: reader.run, signal: reader.signal },
			dependencies.executor.tag,
		)) !== verified.executor.commit
	)
		throw new Error("Executor tag moved during component verification");
	return {
		executor: {
			...dependencies.executor,
			commit: verified.executor.commit,
			manifestSha256: verified.executor.manifestSha256,
			buildInputsSha256: target,
		},
	};
}
/** New auxiliary builds execute trusted control code against tag Go sources.
 * Freeze its executor-specific recipe before building; historical sealed restores do not call this.
 */
export async function compareExecutorRecipeIdentity(options: {
	commit: string;
	controlCommit: string;
	readSource: (commit: string, path: string) => string | Promise<string>;
	signal?: AbortSignal;
	deadline?: number;
}): Promise<void> {
	if (![options.commit, options.controlCommit].every((commit) => /^[a-f0-9]{40}$/.test(commit)))
		throw new Error("Expected exact executor recipe source commits");
	options.signal?.throwIfAborted();
	if (
		options.deadline !== undefined &&
		(!Number.isFinite(options.deadline) || options.deadline <= Date.now())
	)
		throw new Error("Executor recipe deadline exceeded");
	if (options.commit === options.controlCommit) return;
	const parent = AbortSignal.any([
		...(options.signal ? [options.signal] : []),
		AbortSignal.timeout(Math.max(0, (options.deadline ?? Date.now() + 180_000) - Date.now())),
	]);
	let total = 0;
	const read = async (commit: string, path: string) => {
		parent.throwIfAborted();
		const signal = AbortSignal.any([parent, AbortSignal.timeout(REQUEST_MS)]);
		let cancel: (() => void) | undefined;
		try {
			const raw = await Promise.race([
				Promise.resolve(options.readSource(commit, path)),
				new Promise<never>((_, reject) => {
					cancel = () => reject(signal.reason);
					signal.addEventListener("abort", cancel, { once: true });
					if (signal.aborted) cancel();
				}),
			]);
			signal.throwIfAborted();
			const size = Buffer.byteLength(raw);
			total += size;
			if (size > MAX_TEXT || total > MAX_TOTAL || raw.includes("\0"))
				throw new Error("Executor recipe input exceeds bounded text budget");
			return raw;
		} finally {
			if (cancel) signal.removeEventListener("abort", cancel);
		}
	};
	const recipe = async (commit: string) =>
		JSON.stringify([
			recipeSlice(await read(commit, "scripts/lib/helper-build.ts"), [
				"buildExecutorPlatform",
				"executorPlatform",
			]),
			recipeSlice(await read(commit, "shared/remote-executor.ts"), [
				"EXECUTOR_PLATFORMS",
				"executorPublishedFilename",
			]),
			workflowRecipe(await read(commit, ".github/workflows/helpers-release.yml")),
		]);
	if ((await recipe(options.commit)) !== (await recipe(options.controlCommit)))
		throw new Error("Trusted control executor recipe/toolchain differs from component tag source");
}

export async function verifyComponentReleaseEvidence(
	options: ComponentReleaseInputOptions & { evidence?: ComponentReleaseEvidence },
): Promise<ComponentReleaseEvidence | undefined> {
	const current = await collectComponentReleaseEvidence(options);
	if (options.evidence) {
		exactKeys(record(options.evidence), ["executor"]);
		const executor = record(options.evidence.executor);
		exactKeys(executor, [
			"version",
			"protocolVersion",
			"tag",
			"commit",
			"manifestSha256",
			"buildInputsSha256",
		]);
		if (
			!current ||
			Object.entries(current.executor).some(([key, value]) => executor[key] !== value)
		)
			throw new Error("Component release evidence mismatch (tag/manifest/build inputs)");
	}
	return current;
}
