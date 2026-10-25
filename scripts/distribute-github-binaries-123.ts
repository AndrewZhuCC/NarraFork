import { createHash } from "node:crypto";
import { lstat, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { CI_RELEASE_TARGETS } from "./lib/ci-release-types";

const REPOSITORY = "NarraFork/NarraFork";
const GIB = 1024 ** 3;
const CONFIG = ".ci-release/123-rclone.conf";
function phase(name: string) {
	console.log(`${new Date().toISOString()} phase=${name}`);
}
export interface Asset {
	id: number;
	name: string;
	size: number;
	state: string;
}
export interface Release {
	id: number;
	tag_name: string;
	draft: boolean;
	assets: Asset[];
}
export interface Identity {
	size: number;
	sha256: string;
}
function check(value: unknown, message: string): asserts value {
	if (!value) throw new Error(message);
}
export function versionPath(version: string): string {
	check(
		/^(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})(?![\s\S])/.test(version),
		"Invalid version",
	);
	return version;
}
export function remotePath(version: string, name: string): string {
	versionPath(version);
	check(/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,255}(?![\s\S])/.test(name), "Unsafe filename");
	return `nf_update:${version}/${name}`;
}
export function selectAssets(release: Release, version: string) {
	versionPath(version);
	check(
		Number.isSafeInteger(release.id) &&
			release.id > 0 &&
			release.draft === false &&
			release.tag_name === `v${version}`,
		"Invalid public Release",
	);
	check(Array.isArray(release.assets) && release.assets.length <= 100, "Invalid asset set");
	const names = new Set<string>();
	const ids = new Set<number>();
	for (const asset of release.assets) {
		remotePath(version, asset.name);
		check(
			!names.has(asset.name) &&
				!ids.has(asset.id) &&
				Number.isSafeInteger(asset.id) &&
				asset.id > 0 &&
				Number.isSafeInteger(asset.size) &&
				asset.size > 0 &&
				asset.state === "uploaded",
			"Invalid asset identity",
		);
		names.add(asset.name);
		ids.add(asset.id);
	}
	const expected = CI_RELEASE_TARGETS.map((target) => `narrafork-${version}-${target.suffix}`);
	const binaries = release.assets.filter(
		(asset) =>
			asset.name.startsWith(`narrafork-${version}-`) &&
			!asset.name.endsWith(".metadata.json") &&
			!asset.name.includes(".from-") &&
			!asset.name.endsWith(".meta.json") &&
			![`narrafork-${version}-SHA256SUMS`, `narrafork-${version}-checksums.txt`].includes(
				asset.name,
			),
	);
	check(
		binaries.length === 8 && binaries.every((asset) => expected.includes(asset.name)),
		"Unexpected binary coverage",
	);
	return CI_RELEASE_TARGETS.map((target, index) => {
		const binary = release.assets.find((asset) => asset.name === expected[index]);
		const metadata = release.assets.find(
			(asset) => asset.name === `${expected[index]}.metadata.json`,
		);
		check(
			binary && metadata && binary.size <= GIB && metadata.size <= 65536,
			"Missing or oversized binary/sidecar",
		);
		return { target, binary, metadata };
	});
}
export function validateSidecar(
	value: unknown,
	entry: ReturnType<typeof selectAssets>[number],
	version: string,
	commit: string,
): Identity {
	const meta = value as Record<string, unknown>;
	check(
		meta &&
			meta.repository === REPOSITORY &&
			meta.name === entry.binary.name &&
			meta.version === version &&
			meta.platform === entry.target.platform &&
			meta.target === `bun-${entry.target.target}` &&
			/^[a-f0-9]{40}(?![\s\S])/.test(commit) &&
			meta.commit === commit &&
			meta.size === entry.binary.size &&
			typeof meta.sha256 === "string" &&
			/^[a-f0-9]{64}(?![\s\S])/.test(meta.sha256),
		"Sidecar identity mismatch",
	);
	return { size: entry.binary.size, sha256: meta.sha256 };
}
export function verifyIdentity(actual: Identity, expected: Identity): void {
	check(
		actual.size === expected.size && actual.sha256 === expected.sha256,
		"Size/checksum mismatch or remote conflict",
	);
}
export function validateConfig(text: string): void {
	check(
		Buffer.byteLength(text) > 0 && Buffer.byteLength(text) <= 16384,
		"Invalid relay configuration",
	);
	const fields = new Map<string, string>();
	let section = false;
	for (const raw of text.split(/\r?\n/)) {
		const line = raw.trim();
		if (!line || line.startsWith("#") || line.startsWith(";")) continue;
		if (line.startsWith("[")) {
			check(line === "[nf_update]" && !section, "Unexpected relay remote");
			section = true;
			continue;
		}
		const match = /^([a-z]+)\s*=\s*(.*)$/.exec(line);
		check(
			section &&
				match &&
				["type", "url", "vendor", "user", "pass"].includes(match[1]) &&
				!fields.has(match[1]),
			"Invalid relay field",
		);
		fields.set(match[1], match[2]);
	}
	let url: URL;
	try {
		url = new URL(fields.get("url") ?? "");
	} catch {
		throw new Error("Invalid relay URL");
	}
	check(
		fields.get("type") === "webdav" &&
			url.protocol === "https:" &&
			url.hostname === "webdav.123pan.cn" &&
			!url.username &&
			!url.password &&
			!url.port &&
			!url.search &&
			!url.hash,
		"Relay configuration identity mismatch",
	);
}

const RCLONE_STAT_FIELDS = [
	"bytes",
	"speed",
	"errors",
	"checks",
	"transfers",
	"totalBytes",
	"totalChecks",
	"totalTransfers",
	"elapsedTime",
	"eta",
	"retryAfter",
] as const;

/** Never forward rclone messages, paths, URLs, headers, or arbitrary JSON properties. */
export function rcloneDiagnostics(operation: string, log: (line: string) => void = phase) {
	const op = ["lsjson", "copyto", "cat"].includes(operation) ? operation : "other";
	let pending = Buffer.alloc(0);
	let total = 0;
	let category = "none";
	let status: number | undefined;
	function line(bytes: Buffer) {
		check(bytes.length <= 65536, "Child diagnostic line limit");
		let value: Record<string, unknown>;
		try {
			value = JSON.parse(bytes.toString("utf8"));
		} catch {
			return;
		}
		if (!value || typeof value !== "object" || Array.isArray(value)) return;
		const stats = value.stats;
		if (stats && typeof stats === "object" && !Array.isArray(stats)) {
			const numeric: Record<string, number> = {};
			for (const key of RCLONE_STAT_FIELDS) {
				const number = (stats as Record<string, unknown>)[key];
				if (
					typeof number === "number" &&
					Number.isFinite(number) &&
					number >= 0 &&
					number <= Number.MAX_SAFE_INTEGER
				)
					numeric[key] = number;
			}
			if (Object.keys(numeric).length)
				log(`rclone-stats operation=${op} ${JSON.stringify(numeric)}`);
		}
		// Classify only in memory. Neither this text nor any substring of it is emitted.
		const text = [value.msg, value.error, (stats as Record<string, unknown> | null)?.lastError]
			.filter((item): item is string => typeof item === "string")
			.join(" ");
		const http =
			/(?:HTTP(?:\/\d(?:\.\d)?)?\s*(?:status(?:\s+code)?\s*)?[:=]?\s*|status(?:\s+code)?\s*[:=]?\s*|\b)([45]\d{2})\b/i.exec(
				text,
			);
		if (http) {
			category = "http";
			status = Number(http[1]);
		} else if (/timeout|timed out|deadline|max duration|context canceled/i.test(text)) {
			category = "timeout";
			status = undefined;
		} else if (
			/connection|network|dial tcp|no such host|TLS|certificate|unexpected EOF|broken pipe|connection reset/i.test(
				text,
			)
		) {
			category = "network";
			status = undefined;
		} else if (value.level === "error" || value.level === "fatal") {
			category = "other";
			status = undefined;
		}
	}
	return {
		push(chunk: Uint8Array) {
			total += chunk.length;
			check(total <= 512 * 1024, "Child diagnostic total limit");
			let start = 0;
			for (let index = 0; index < chunk.length; index++) {
				if (chunk[index] !== 10) continue;
				check(pending.length + index - start <= 65536, "Child diagnostic line limit");
				line(Buffer.concat([pending, chunk.subarray(start, index)]));
				pending = Buffer.alloc(0);
				start = index + 1;
			}
			check(pending.length + chunk.length - start <= 65536, "Child diagnostic line limit");
			pending = Buffer.concat([pending, chunk.subarray(start)]);
		},
		finish(code: number, aborted: boolean) {
			if (pending.length) {
				line(pending);
				pending = Buffer.alloc(0);
			}
			log(
				`rclone-result operation=${op} code=${Number.isSafeInteger(code) ? code : -1} category=${aborted ? "timeout" : code === 0 ? "none" : category === "none" ? "other" : category}${!aborted && code !== 0 && status !== undefined ? ` httpStatus=${status}` : ""}`,
			);
		},
	};
}

/** CI-only process adapter: streaming stdout, bounded diagnostics, cancellation and per-child deadline. */
export async function runChild(
	command: string[],
	limit: number,
	signal: AbortSignal,
	consume?: (chunk: Uint8Array) => Promise<void>,
): Promise<{ code: number; stdout: string }> {
	const childSignal = AbortSignal.any([signal, AbortSignal.timeout(15 * 60_000)]);
	childSignal.throwIfAborted();
	const child = Bun.spawn(command, {
		stdout: "pipe",
		stderr: "pipe",
		env: {
			PATH: process.env.PATH,
			HOME: process.env.HOME,
			...(command[0] === "gh" ? { GH_TOKEN: process.env.GH_TOKEN, GH_HOST: "github.com" } : {}),
		},
	});
	const abort = () => child.kill("SIGKILL");
	childSignal.addEventListener("abort", abort, { once: true });
	const chunks: Uint8Array[] = [];
	const diagnostics = command[0] === "rclone" ? rcloneDiagnostics(command[1]) : undefined;
	async function drain(stream: ReadableStream<Uint8Array>, maximum: number, output: boolean) {
		let size = 0;
		let nextProgress = 16 * 1024 ** 2;
		for await (const chunk of stream) {
			size += chunk.length;
			if (output && size >= nextProgress) {
				phase(`stream-progress bytes=${size}`);
				nextProgress = size + 16 * 1024 ** 2;
			}
			check(size <= maximum, "Child output limit");
			if (output) {
				if (consume) await consume(chunk);
				else chunks.push(chunk);
			} else diagnostics?.push(chunk);
		}
	}
	try {
		childSignal.throwIfAborted();
		await Promise.all([
			drain(child.stdout, limit, true),
			drain(child.stderr, diagnostics ? 512 * 1024 : 65536, false),
		]);
		const code = await child.exited;
		childSignal.throwIfAborted();
		return { code, stdout: Buffer.concat(chunks).toString("utf8") };
	} catch {
		child.kill("SIGKILL");
		await child.exited;
		phase(`child-failed aborted=${childSignal.aborted} exit=${child.exitCode}`);
		throw new Error("Bounded child operation failed");
	} finally {
		childSignal.removeEventListener("abort", abort);
		diagnostics?.finish(child.exitCode ?? -1, childSignal.aborted);
	}
}
export type Runner = typeof runChild;
export async function ensureRemote(
	local: string,
	remote: string,
	expected: Identity,
	signal: AbortSignal,
	run: Runner = runChild,
): Promise<"reused" | "uploaded"> {
	const destination = /^nf_update:([^/]+)\/([^/]+)$/.exec(remote);
	check(
		destination && remotePath(destination[1], destination[2]) === remote,
		"Invalid destination",
	);
	check(
		Number.isSafeInteger(expected.size) &&
			expected.size > 0 &&
			expected.size <= GIB &&
			/^[a-f0-9]{64}(?![\s\S])/.test(expected.sha256),
		"Invalid binary identity",
	);
	const base = [
		"--config",
		CONFIG,
		"--retries",
		"1",
		"--low-level-retries",
		"1",
		"--contimeout",
		"30s",
		"--timeout",
		"2m",
		"--max-duration",
		"14m",
		"--stats",
		"30s",
		"--stats-log-level",
		"NOTICE",
		"--use-json-log",
		"--log-level",
		"NOTICE",
		"--cutoff-mode",
		"HARD",
	];
	phase(`remote-stat file=${destination[2]}`);
	const listed = await run(["rclone", "lsjson", remote, "--stat", ...base], 65536, signal);
	phase(`remote-stat-result file=${destination[2]} code=${listed.code}`);
	// rclone uses 3/4 exclusively for missing directory/file. All other failures fail closed.
	check([0, 3, 4].includes(listed.code), "Remote existence check failed");
	const exists = listed.code === 0;
	if (exists) {
		const info = JSON.parse(listed.stdout);
		check(info.IsDir === false && info.Size === expected.size, "Remote conflict");
	} else {
		phase(`upload file=${destination[2]} bytes=${expected.size}`);
		const copied = await run(
			["rclone", "copyto", local, remote, "--immutable", ...base],
			65536,
			signal,
		);
		phase(`upload-result file=${destination[2]} code=${copied.code}`);
		check(copied.code === 0, "Immutable upload failed");
	}
	phase(`readback file=${destination[2]} bytes=${expected.size}`);
	const hash = createHash("sha256");
	let size = 0;
	const result = await run(
		["rclone", "cat", remote, ...base],
		expected.size,
		signal,
		async (chunk) => {
			size += chunk.length;
			hash.update(chunk);
		},
	);
	check(result.code === 0, "Remote readback failed");
	verifyIdentity({ size, sha256: hash.digest("hex") }, expected);
	phase(`verified file=${destination[2]} bytes=${size}`);
	return exists ? "reused" : "uploaded";
}
async function main() {
	const signal = AbortSignal.timeout(60 * 60_000);
	let staging: string | undefined;
	try {
		const version = versionPath(process.env.VERSION ?? "0.9.8");
		check(process.env.GH_TOKEN, "Missing GitHub token");
		const configInfo = await lstat(CONFIG);
		check(
			configInfo.isFile() && (configInfo.mode & 0o777) === 0o600 && configInfo.size <= 16384,
			"Private config permissions mismatch",
		);
		validateConfig(await readFile(CONFIG, "utf8"));
		staging = await mkdtemp(join(tmpdir(), "nf-github-123-"));
		async function api(path: string) {
			const result = await runChild(
				["gh", "api", `repos/${REPOSITORY}/${path}`],
				2 * 1024 ** 2,
				signal,
			);
			check(result.code === 0, "GitHub metadata request failed");
			return JSON.parse(result.stdout);
		}
		phase(`release-metadata version=${version}`);
		const release: Release = await api(`releases/tags/v${version}`);
		const entries = selectAssets(release, version);
		check(
			entries.reduce((size, entry) => size + entry.binary.size, 0) <= 8 * GIB,
			"Total binary limit",
		);
		const commit = (await api(`commits/v${version}`)).sha;
		async function download(asset: Asset, consume: (chunk: Uint8Array) => Promise<void>) {
			phase(`download file=${asset.name} bytes=${asset.size}`);
			const current: Release = await api(`releases/${release.id}`);
			selectAssets(current, version);
			check(
				current.id === release.id &&
					current.assets.some(
						(item) => item.id === asset.id && item.name === asset.name && item.size === asset.size,
					),
				"Release asset changed",
			);
			let size = 0;
			const result = await runChild(
				[
					"gh",
					"api",
					"-H",
					"Accept: application/octet-stream",
					`repos/${REPOSITORY}/releases/assets/${asset.id}`,
				],
				asset.size,
				signal,
				async (chunk) => {
					size += chunk.length;
					await consume(chunk);
				},
			);
			phase(`download-result file=${asset.name} code=${result.code} bytes=${size}`);
			check(result.code === 0 && size === asset.size, "Asset download failed");
		}
		const verified = [];
		// Validate every sidecar before touching the remote.
		for (const entry of entries) {
			const chunks: Uint8Array[] = [];
			await download(entry.metadata, async (chunk) => {
				chunks.push(chunk);
			});
			const identity = validateSidecar(
				JSON.parse(Buffer.concat(chunks).toString("utf8")),
				entry,
				version,
				commit,
			);
			verified.push({ ...entry, identity });
		}
		const files = [];
		for (const entry of verified) {
			const local = join(staging, entry.binary.name);
			const writer = Bun.file(local).writer();
			const hash = createHash("sha256");
			let size = 0;
			try {
				await download(entry.binary, async (chunk) => {
					hash.update(chunk);
					size += chunk.length;
					writer.write(chunk);
					await writer.flush();
				});
			} finally {
				await writer.end();
			}
			verifyIdentity({ size, sha256: hash.digest("hex") }, entry.identity);
			const remote = remotePath(version, entry.binary.name);
			const status = await ensureRemote(local, remote, entry.identity, signal);
			files.push({
				name: entry.binary.name,
				assetId: entry.binary.id,
				...entry.identity,
				remote,
				status,
			});
			await writeFile(
				".ci-release/github-binaries-123-receipt.json",
				`${JSON.stringify({ repository: REPOSITORY, releaseId: release.id, version, commit, complete: false, files }, null, 2)}\n`,
				{ mode: 0o600 },
			);
			await rm(local);
		}
		await writeFile(
			".ci-release/github-binaries-123-receipt.json",
			`${JSON.stringify({ repository: REPOSITORY, releaseId: release.id, version, commit, complete: true, files }, null, 2)}\n`,
			{ mode: 0o600 },
		);
	} finally {
		await rm(CONFIG, { force: true });
		if (staging) await rm(staging, { recursive: true, force: true });
	}
}
if (import.meta.main)
	main().catch(() => {
		console.error("GitHub binary distribution failed (diagnostics withheld)");
		process.exitCode = 1;
	});
