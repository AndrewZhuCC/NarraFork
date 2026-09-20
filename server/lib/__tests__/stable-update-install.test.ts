import { afterEach, beforeEach, expect, test } from "bun:test";
import { chmodSync, mkdtempSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type StablePathInstall, verifyStablePathInstall } from "../stable-update-install";

let root: string;
let binary: string;
let calls: number;
let context: NonNullable<Parameters<typeof verifyStablePathInstall>[2]>;
const install: StablePathInstall = {
	launchdLabel: "dev.narrafork.server",
	fromCommit: "aaaaaaaa",
	toCommit: "bbbbbbbb",
	certificateSha1: "d".repeat(40),
};
beforeEach(() => {
	root = realpathSync(mkdtempSync(join(tmpdir(), "nf-stable-install-")));
	binary = join(root, "narrafork-server");
	const identityPath = join(root, "identity.json");
	writeFileSync(binary, "signed test binary", { mode: 0o755 });
	writeFileSync(identityPath, JSON.stringify({ fingerprint: install.certificateSha1 }), {
		mode: 0o600,
	});
	calls = 0;
	context = {
		platform: "darwin",
		parentPid: 1,
		service: install.launchdLabel,
		execPath: binary,
		stablePath: binary,
		identityPath,
		commit: "aaaaaaaa",
		uid: process.getuid?.(),
		verifySignature: async () => {
			calls++;
			return true;
		},
	};
});
afterEach(() => rmSync(root, { recursive: true, force: true }));

test("accepts only a pinned signature at the fixed launchd executable path", async () => {
	expect(await verifyStablePathInstall(binary, install, context)).toBe(true);
	expect(calls).toBe(1);
});
for (const [name, patch] of [
	["other OS", { platform: "linux" }],
	["manual process", { parentPid: 42 }],
	["other launchd job", { service: "other" }],
	["wrong running commit", { commit: "cccccccc" }],
	["already updated", { commit: "bbbbbbbb" }],
] as const) {
	test(`rejects ${name} before signature verification`, async () => {
		expect(await verifyStablePathInstall(binary, install, { ...context, ...patch })).toBe(false);
		expect(calls).toBe(0);
	});
}
test("rejects metadata from another certificate", async () => {
	expect(
		await verifyStablePathInstall(binary, { ...install, certificateSha1: "e".repeat(40) }, context),
	).toBe(false);
	expect(calls).toBe(0);
});
test("rejects an invalid signature", async () => {
	context.verifySignature = async () => false;
	expect(await verifyStablePathInstall(binary, install, context)).toBe(false);
});
test("rejects writable binary and symlink target", async () => {
	chmodSync(binary, 0o777);
	expect(await verifyStablePathInstall(binary, install, context)).toBe(false);
	rmSync(binary);
	const elsewhere = join(root, "elsewhere");
	writeFileSync(elsewhere, "signed", { mode: 0o755 });
	symlinkSync(elsewhere, binary);
	expect(await verifyStablePathInstall(binary, install, context)).toBe(false);
});
test("rejects a candidate outside the stable executable", async () => {
	expect(await verifyStablePathInstall(join(root, "different"), install, context)).toBe(false);
});
test("rejects a writable or oversized signer identity file", async () => {
	chmodSync(context.identityPath, 0o666);
	expect(await verifyStablePathInstall(binary, install, context)).toBe(false);
	chmodSync(context.identityPath, 0o600);
	writeFileSync(context.identityPath, " ".repeat(4097));
	expect(await verifyStablePathInstall(binary, install, context)).toBe(false);
});
