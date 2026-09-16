import { describe, expect, test } from "bun:test";
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, mkdtempSync, rmSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { Worker } from "node:worker_threads";

const signingVariables = ["NF_CODESIGN_IDENTITY", "NF_CODESIGN_KEYCHAIN", "NF_CODESIGN_IDENTIFIER"];

describe.skipIf(process.platform !== "darwin")("macOS post-processing signing", () => {
	async function check(envOverrides: Record<string, string>, shouldFail: boolean) {
		const dir = mkdtempSync(join(tmpdir(), "nf-signing-check-"));
		const binary = join(dir, "probe");
		copyFileSync("/usr/bin/true", binary);
		const env = { ...process.env };
		for (const key of signingVariables) delete env[key];
		Object.assign(env, envOverrides);
		const worker = new Worker(join(import.meta.dir, "post-process-worker.ts"), {
			env,
			workerData: {
				platform: { target: "bun-darwin-arm64", platformId: "darwin-arm64", name: "probe" },
				distDir: dir,
				root: dir,
				version: "0.0.0",
				commit: "test",
			},
		});
		try {
			const result = new Promise<void>((resolve, reject) => {
				worker.on("error", reject);
				worker.on("message", (message) => {
					if (message.type === "done") resolve();
				});
				worker.on("exit", (code) => {
					if (code !== 0) reject(new Error(`worker exited ${code}`));
				});
			});
			if (shouldFail) {
				await expect(result).rejects.toThrow();
				expect(existsSync(`${binary}.metadata.json`)).toBe(false);
			} else {
				await result;
				execFileSync("/usr/bin/codesign", ["--verify", "--strict", binary], { timeout: 10_000 });
				expect(existsSync(`${binary}.metadata.json`)).toBe(true);
			}
		} finally {
			await worker.terminate();
			rmSync(dir, { recursive: true, force: true });
		}
	}

	test("unconfigured builds retain ad-hoc signing", () => check({}, false), 20_000);
	test("partial identity configuration fails without metadata", () =>
		check(
			{
				NF_CODESIGN_IDENTIFIER: "app.narrafork.selfuse",
			},
			true,
		));
	test("explicit empty identity cannot silently choose ad-hoc signing", () =>
		check(
			{
				NF_CODESIGN_IDENTITY: "",
			},
			true,
		));
	test("ad-hoc marker cannot be configured as a stable identity", () =>
		check(
			{
				NF_CODESIGN_IDENTITY: "-",
			},
			true,
		));
	test(
		"missing certificate fails rather than falling back",
		() =>
			check(
				{
					NF_CODESIGN_IDENTITY: "F".repeat(40),
					NF_CODESIGN_KEYCHAIN: join(homedir(), "Library/Keychains/login.keychain-db"),
					NF_CODESIGN_IDENTIFIER: "app.narrafork.selfuse",
				},
				true,
			),
		20_000,
	);
});
