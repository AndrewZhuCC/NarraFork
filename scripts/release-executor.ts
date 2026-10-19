/**
 * Default server target mirrors the public GitHub executor manifest/license
 * metadata only. Component builds and immutable bundle publication stay on the
 * protected GitHub path. --dry-run is offline; --preview verifies GitHub online.
 */
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { EXECUTOR_RELEASE_VERSION } from "../shared/helper-distribution";
import {
	EXECUTOR_PLATFORMS,
	type ExecutorPlatform,
	isExecutorPlatform,
} from "../shared/remote-executor";
import { resolveBuildGitHubRepository } from "./lib/build-repository";
import { validateGitHubRepository } from "./lib/github-release";

const ROOT = join(import.meta.dir, "..");
const VERSION_RE = /^\d+\.\d+\.\d+(?:-[A-Za-z0-9._-]+)?$/;
const args = process.argv.slice(2);
if (args.filter((arg) => arg.startsWith("--target=")).length > 1)
	throw new Error("Duplicate --target option");
const target =
	args.find((arg) => arg.startsWith("--target="))?.slice("--target=".length) ?? "server";
if (target !== "server" && target !== "github") throw new Error("Expected --target=server|github");

if (target === "github") {
	// Restored GitHub bundles retain their own repository, version and component
	// evidence; today's component pin must never rewrite their immutable plan.
	const { parseHelperReleaseArguments, runHelperReleaseController } = await import(
		"./release-helpers"
	);
	const githubArgs = args.filter((arg) => arg.startsWith("--") && !arg.startsWith("--target="));
	const optionNames = githubArgs.map((arg) => arg.split("=")[0]);
	if (new Set(optionNames).size !== optionNames.length)
		throw new Error("Duplicate GitHub executor option");
	const positional = args.filter((arg) => !arg.startsWith("--"));
	if (positional.length > 1 || (positional[0] !== undefined && !VERSION_RE.test(positional[0])))
		throw new Error("Expected at most one valid executor version");
	if (
		githubArgs.some(
			(arg) =>
				!["--dry-run", "--upload-only"].includes(arg) &&
				![
					"--plan=",
					"--output=",
					"--root=",
					"--github-repository=",
					"--source-run-attempt=",
					"--bundle-artifact-id=",
				].some((prefix) => arg.startsWith(prefix)),
		)
	)
		throw new Error(
			"GitHub executor publication accepts only a complete native-smoked bundle; no --platform or legacy upload options",
		);
	if (
		!githubArgs.some((arg) => arg.startsWith("--plan=")) ||
		!githubArgs.some((arg) => arg.startsWith("--output="))
	)
		throw new Error(
			"GitHub executor release requires --plan=<immutable plan.json> and --output=<bundle-dir>",
		);
	// Keep legacy preview without provenance; publish --dry-run accepts the optional fields offline.
	const preview =
		args.includes("--dry-run") &&
		!optionNames.some((name) => name === "--source-run-attempt" || name === "--bundle-artifact-id");
	const forwarded = [
		preview ? "preview" : "publish",
		...githubArgs.filter(
			(arg) =>
				!(preview && arg === "--dry-run") &&
				arg !== "--upload-only" &&
				!arg.startsWith("--github-repository="),
		),
	];
	const parsed = parseHelperReleaseArguments(forwarded);
	const { readHelperReleasePlan } = await import("./lib/helper-release-control");
	const githubRoot = resolve(parsed.options.get("root") ?? ".");
	const planPath = resolve(githubRoot, parsed.options.get("plan") as string);
	const plan = await readHelperReleasePlan(planPath);
	if (plan.kind !== "executor") throw new Error("Expected executor release plan");
	// A historical sealed bundle is the repository authority in this path.
	// Real publication still validates trusted dispatch/repository authorization
	// in the protected helper controller; an offline preview is not publication.
	const repository =
		args
			.find((arg) => arg.startsWith("--github-repository="))
			?.slice("--github-repository=".length) ?? plan.repository;
	validateGitHubRepository(repository);
	if (repository.toLowerCase() !== plan.repository.toLowerCase())
		throw new Error("Executor repository does not match the immutable bundle plan");
	const requestedVersion = args.find((arg) => !arg.startsWith("--"));
	if (requestedVersion && requestedVersion !== plan.version)
		throw new Error("Executor version does not match the immutable bundle plan");
	await runHelperReleaseController(forwarded);
	process.exit(0);
}

const positional = args.filter((arg) => !arg.startsWith("--"));
if (positional.length > 1) throw new Error("Expected at most one valid executor version");
const version = positional[0] ?? EXECUTOR_RELEASE_VERSION;
if (!VERSION_RE.test(version)) throw new Error("Invalid executor version format");
const dryRun = args.includes("--dry-run");
const preview = args.includes("--preview");
if (dryRun && preview) throw new Error("--preview is online read-only; --dry-run is offline");
const optionNames = args.filter((arg) => arg.startsWith("--")).map((arg) => arg.split("=")[0]);
if (new Set(optionNames).size !== optionNames.length)
	throw new Error("Duplicate executor mirror option");
for (const arg of args.filter((arg) => arg.startsWith("--"))) {
	if (
		!["--dry-run", "--preview", "--upload-only"].includes(arg) &&
		!["--target=", "--platform=", "--github-repository="].some((prefix) => arg.startsWith(prefix))
	)
		throw new Error(
			`${arg} is obsolete/unsupported for mirrors: no build, dist upload or patch generation; publish via the primary GitHub entry first`,
		);
}
const platformArg = args.find((arg) => arg.startsWith("--platform="))?.slice("--platform=".length);
const targetPlatforms: ExecutorPlatform[] = platformArg
	? platformArg.split(",").map((value) => {
			const platform = value.trim();
			if (!isExecutorPlatform(platform))
				throw new Error(
					`Unknown platform: ${platform}; available: ${EXECUTOR_PLATFORMS.join(", ")}`,
				);
			return platform;
		})
	: [...EXECUTOR_PLATFORMS];
const repository =
	args
		.find((arg) => arg.startsWith("--github-repository="))
		?.slice("--github-repository=".length) ?? resolveBuildGitHubRepository({ root: ROOT });
validateGitHubRepository(repository);

function loadUpdateServerConfig(): { serverUrl: string; token: string } {
	const configPath = join(homedir(), ".narrafork", "update-server.json");
	let fileConfig: { serverUrl?: string; token?: string } = {};
	if (existsSync(configPath)) {
		try {
			fileConfig = JSON.parse(readFileSync(configPath, "utf-8"));
		} catch {
			/* Environment overrides retain the original config precedence. */
		}
	}
	return {
		serverUrl:
			process.env.NF_UPDATE_SERVER ??
			fileConfig.serverUrl ??
			"https://narrafork-update.b.domexie.cn",
		token: process.env.NF_UPDATE_TOKEN ?? fileConfig.token ?? "",
	};
}

if (dryRun) {
	console.log(
		`Offline mirror intent: executor ${repository} executor-v${version}; metadata only; no network, build, mutation, dist or token access. Use --preview for online verification.`,
	);
	process.exit(0);
}
const controller = new AbortController();
const cancel = () => controller.abort();
process.once("SIGINT", cancel);
process.once("SIGTERM", cancel);
const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(25 * 60_000)]);
try {
	const { mirrorPublishedGitHubRelease } = await import("./mirror-update-server");
	const receipt = await mirrorPublishedGitHubRelease({
		kind: "executor",
		repository,
		tag: `executor-v${version}`,
		platforms: targetPlatforms,
		dryRun: preview,
		signal,
		...(preview ? {} : { config: loadUpdateServerConfig() }),
	});
	console.log(JSON.stringify(receipt, null, 2));
} catch (error) {
	console.error(
		`Executor mirror failed; an already-public GitHub component is required. Publish via the primary GitHub entry first; no automatic build/publication is attempted. ${error instanceof Error ? error.message : String(error)}`,
	);
	process.exitCode = 1;
} finally {
	process.removeListener("SIGINT", cancel);
	process.removeListener("SIGTERM", cancel);
}
process.exit();
