import { describe, expect, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import {
	ensureRemote,
	type Release,
	type Runner,
	remotePath,
	runChild,
	selectAssets,
	validateConfig,
	validateSidecar,
	verifyIdentity,
	versionPath,
} from "../../scripts/distribute-github-binaries-123";
import { CI_RELEASE_TARGETS } from "../../scripts/lib/ci-release-types";

const version = "0.9.8";
const commit = "a".repeat(40);
const bytes = Buffer.from("verified binary");
const identity = { size: bytes.length, sha256: createHash("sha256").update(bytes).digest("hex") };
function fixture(): Release {
	let id = 1;
	return {
		id: 42,
		tag_name: `v${version}`,
		draft: false,
		assets: CI_RELEASE_TARGETS.flatMap((target) => {
			const name = `narrafork-${version}-${target.suffix}`;
			return [
				{ id: id++, name, size: identity.size, state: "uploaded" },
				{ id: id++, name: `${name}.metadata.json`, size: 700, state: "uploaded" },
			];
		}),
	};
}
function sidecar(entry: ReturnType<typeof selectAssets>[number]) {
	return {
		repository: "NarraFork/NarraFork",
		name: entry.binary.name,
		version,
		commit,
		platform: entry.target.platform,
		target: `bun-${entry.target.target}`,
		...identity,
	};
}
describe("strict public Release selection", () => {
	test("selects exactly eight full platform binaries, with macos suffix and Windows platform mapping", () => {
		const release = fixture();
		release.assets.push({
			id: 99,
			name: "narrafork-0.9.8-linux-x64.from-0.9.7.zstd-patch",
			size: 2,
			state: "uploaded",
		});
		const entries = selectAssets(release, version);
		expect(entries).toHaveLength(8);
		expect(entries.map((entry) => entry.target.platform)).toContain("win-arm64");
		expect(entries.at(-1)?.binary.name).toBe("narrafork-0.9.8-macos-arm64");
		for (const entry of entries)
			expect(validateSidecar(sidecar(entry), entry, version, commit)).toEqual(identity);
	});
	test("rejects missing, duplicate, extra matching, oversized, draft and wrong tag assets", () => {
		for (const mutate of [
			(release: Release) => {
				release.assets.pop();
			},
			(release: Release) => {
				release.assets.push(release.assets[0]);
			},
			(release: Release) => {
				release.assets.push({
					id: 99,
					name: "narrafork-0.9.8-linux-riscv64",
					size: 1,
					state: "uploaded",
				});
			},
			(release: Release) => {
				release.assets[0].size = 1024 ** 3 + 1;
			},
			(release: Release) => {
				release.draft = true;
			},
			(release: Release) => {
				release.tag_name = "v0.9.7";
			},
		]) {
			const release = fixture();
			mutate(release);
			expect(() => selectAssets(release, version)).toThrow();
		}
	});
	test("rejects sidecar version/platform/target/commit/size/hash mismatch", () => {
		const entry = selectAssets(fixture(), version)[0];
		for (const [key, value] of Object.entries({
			version: "0.9.7",
			platform: "darwin-x64",
			target: "bun-linux-arm64",
			commit: "b".repeat(40),
			name: "other",
			size: 5,
			sha256: "invalid",
			repository: "attacker/fork",
		})) {
			expect(() =>
				validateSidecar({ ...sidecar(entry), [key]: value }, entry, version, commit),
			).toThrow();
		}
		expect(() => verifyIdentity({ ...identity, sha256: "b".repeat(64) }, identity)).toThrow();
	});
});
describe("immutable remote behavior", () => {
	function runner(exists: boolean, remoteBytes = bytes, listCode = exists ? 0 : 4) {
		const commands: string[][] = [];
		const run: Runner = async (command, _limit, _signal, consume) => {
			commands.push(command);
			if (command[1] === "lsjson")
				return {
					code: listCode,
					stdout: JSON.stringify({ IsDir: false, Size: remoteBytes.length }),
				};
			if (command[1] === "cat") {
				await consume?.(remoteBytes.subarray(0, 2));
				await consume?.(remoteBytes.subarray(2));
			}
			return { code: 0, stdout: "" };
		};
		return { run, commands };
	}
	test("existing bytes are streamed and verified before reuse, never copied", async () => {
		const fake = runner(true);
		expect(
			await ensureRemote(
				"local",
				remotePath(version, "binary"),
				identity,
				new AbortController().signal,
				fake.run,
			),
		).toBe("reused");
		expect(fake.commands.map((command) => command[1])).toEqual(["lsjson", "cat"]);
	});
	test("new files use immutable copy and streamed readback", async () => {
		const fake = runner(false);
		expect(
			await ensureRemote(
				"local",
				remotePath(version, "binary"),
				identity,
				new AbortController().signal,
				fake.run,
			),
		).toBe("uploaded");
		expect(fake.commands.map((command) => command[1])).toEqual(["lsjson", "copyto", "cat"]);
		expect(fake.commands[1]).toContain("--immutable");
	});
	test("same-size conflicting bytes, readback corruption, and existence failures fail closed", async () => {
		for (const exists of [true, false]) {
			const fake = runner(exists, Buffer.alloc(bytes.length));
			await expect(
				ensureRemote(
					"local",
					remotePath(version, "binary"),
					identity,
					new AbortController().signal,
					fake.run,
				),
			).rejects.toThrow();
			if (exists) expect(fake.commands.some((command) => command[1] === "copyto")).toBe(false);
		}
		const fake = runner(false, bytes, 1);
		await expect(
			ensureRemote(
				"local",
				remotePath(version, "binary"),
				identity,
				new AbortController().signal,
				fake.run,
			),
		).rejects.toThrow();
		expect(fake.commands).toHaveLength(1);
	});
});
test("strict version and flat paths prevent injection/traversal; future versions work", () => {
	for (const bad of ["v0.9.8", "0.9.8/../", "0.9.8\n", "01.2.3", "0.9.8-beta", "$(id)"])
		expect(() => versionPath(bad)).toThrow();
	for (const bad of ["../secret", "a/b", "a\\b", "a\n"])
		expect(() => remotePath(version, bad)).toThrow();
	expect(remotePath("1.0.0", "narrafork-1.0.0-linux-x64")).toBe(
		"nf_update:1.0.0/narrafork-1.0.0-linux-x64",
	);
});
test("configuration accepts only approved remote with HTTPS host and bounded fields", () => {
	const valid =
		"[nf_update]\ntype = webdav\nurl = https://webdav.123pan.cn/dav/\nuser = private\npass = opaque\n";
	expect(() => validateConfig(valid)).not.toThrow();
	for (const bad of [
		valid.replace("webdav.123pan.cn", "evil.example"),
		valid.replace("https:", "http:"),
		`${valid}[other]\n`,
		`${valid}token = secret\n`,
		`${valid}type = webdav\n`,
		valid.replace("https://", "https://user:pass@"),
		valid.replace("/dav/", "/dav/?token=secret"),
	])
		expect(() => validateConfig(bad)).toThrow();
});
test("bounded child output and cancellation do not expose stderr", async () => {
	await expect(
		runChild(
			[process.execPath, "-e", 'console.log("x".repeat(2000))'],
			20,
			new AbortController().signal,
		),
	).rejects.toThrow("Bounded child operation failed");
	const controller = new AbortController();
	controller.abort();
	await expect(runChild([process.execPath, "-e", "0"], 20, controller.signal)).rejects.toThrow();
});
test("stage diagnostics identify the remote operation without exposing child stderr", async () => {
	const logs: string[] = [];
	const spy = spyOn(console, "log").mockImplementation((line) => logs.push(String(line)));
	try {
		await expect(
			runChild(
				[
					process.execPath,
					"-e",
					'console.error("PRIVATE-CREDENTIAL"); console.log("x".repeat(2000))',
				],
				20,
				new AbortController().signal,
			),
		).rejects.toThrow("Bounded child operation failed");
		expect(logs.some((line) => line.includes("phase=child-failed"))).toBe(true);
		expect(logs.join("\n")).not.toContain("PRIVATE-CREDENTIAL");
		const run: Runner = async (command, _limit, _signal, consume) => {
			if (command[1] === "lsjson")
				return { code: 0, stdout: JSON.stringify({ IsDir: false, Size: bytes.length }) };
			await consume?.(bytes);
			return { code: 0, stdout: "" };
		};
		await ensureRemote(
			"local",
			remotePath(version, "binary"),
			identity,
			new AbortController().signal,
			run,
		);
		for (const stage of ["remote-stat", "readback", "verified"])
			expect(logs.some((line) => line.includes(`phase=${stage} file=binary`))).toBe(true);
	} finally {
		spy.mockRestore();
	}
});
test("workflow is manual/default-branch/official-repository/environment guarded and receipt-only", () => {
	const workflow = readFileSync(".github/workflows/github-binaries-123.yml", "utf8");
	for (const text of [
		"workflow_dispatch:",
		"default: '0.9.8'",
		"github.repository == 'NarraFork/NarraFork'",
		"github.event.repository.default_branch",
		"environment: release",
		"contents: read",
		"timeout-minutes: 60",
		"bun-version: 1.4.3",
		`VERSION: \${{ inputs.version }}`,
		"secrets.NF_RELAY_RCLONE_CONFIG",
		"path: .ci-release/github-binaries-123-receipt.json",
		`if: \${{ always() }}`,
	])
		expect(workflow).toContain(text);
	for (const forbidden of [
		"bun install",
		"bun run build",
		"contents: write",
		"gh release upload",
		"--upload",
		"workflow_call:",
	])
		expect(workflow).not.toContain(forbidden);
	expect(workflow.match(/uses: actions\/upload-artifact/g)).toHaveLength(1);
	expect(workflow).not.toContain("run: ${{");
});
