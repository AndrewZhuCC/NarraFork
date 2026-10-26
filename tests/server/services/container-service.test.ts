import { afterEach, describe, expect, it } from "bun:test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	type ContainerConfig,
	resolveComposeFile,
} from "../../../server/services/container-service";

// ─── Temp directory helpers ───
let tmpDirs: string[] = [];

function makeTmpDir(): string {
	const dir = join(tmpdir(), `nf-test-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`);
	mkdirSync(dir, { recursive: true });
	tmpDirs.push(dir);
	return dir;
}

afterEach(() => {
	for (const d of tmpDirs) {
		try {
			rmSync(d, { recursive: true, force: true });
		} catch {}
	}
	tmpDirs = [];
});

// ═══════════════════════════════════════════════════════════════
// resolveComposeFile
// ═══════════════════════════════════════════════════════════════
describe("resolveComposeFile", () => {
	it("returns null when no compose file exists", () => {
		const dir = makeTmpDir();
		expect(resolveComposeFile(dir, null)).toBeNull();
	});

	it("finds compose.yml by default", () => {
		const dir = makeTmpDir();
		const file = join(dir, "compose.yml");
		writeFileSync(file, "version: '3'");
		expect(resolveComposeFile(dir, null)).toBe(file);
	});

	it("finds compose.yaml by default", () => {
		const dir = makeTmpDir();
		writeFileSync(join(dir, "compose.yaml"), "version: '3'");
		expect(resolveComposeFile(dir, null)).toBe(join(dir, "compose.yaml"));
	});

	it("does not match docker-compose.yml", () => {
		const dir = makeTmpDir();
		writeFileSync(join(dir, "docker-compose.yml"), "version: '3'");
		expect(resolveComposeFile(dir, null)).toBeNull();
	});

	it("does not match docker-compose.yaml", () => {
		const dir = makeTmpDir();
		writeFileSync(join(dir, "docker-compose.yaml"), "version: '3'");
		expect(resolveComposeFile(dir, null)).toBeNull();
	});

	it("uses config.composeFile when specified", () => {
		const dir = makeTmpDir();
		const sub = join(dir, "infra");
		mkdirSync(sub, { recursive: true });
		const file = join(sub, "my-compose.yml");
		writeFileSync(file, "version: '3'");

		const config: ContainerConfig = { composeFile: "infra/my-compose.yml" };
		expect(resolveComposeFile(dir, config)).toBe(file);
	});

	it("returns null when config.composeFile does not exist", () => {
		const dir = makeTmpDir();
		const config: ContainerConfig = { composeFile: "nonexistent.yml" };
		expect(resolveComposeFile(dir, config)).toBeNull();
	});

	it("blocks path traversal via config.composeFile", () => {
		const dir = makeTmpDir();
		// Create a file outside the worktree
		const outside = makeTmpDir();
		writeFileSync(join(outside, "evil.yml"), "version: '3'");

		const config: ContainerConfig = { composeFile: `../../${outside}/evil.yml` };
		expect(resolveComposeFile(dir, config)).toBeNull();
	});
});
