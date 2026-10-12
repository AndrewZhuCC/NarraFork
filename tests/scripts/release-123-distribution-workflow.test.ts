import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const workflow = readFileSync(".github/workflows/release-123-distribution.yml", "utf8");
const restore = readFileSync("scripts/restore-123-originals.ts", "utf8");

describe("protected original-byte 123 distribution", () => {
	test("is explicit default-branch deployment with read-only GitHub permissions", () => {
		expect(workflow).toContain("workflow_dispatch:");
		expect(workflow).not.toMatch(/^\s+(push|pull_request|schedule):/m);
		expect(workflow).toContain("environment: release");
		expect(workflow).toContain("contents: read");
		expect(workflow).toContain("actions: read");
		expect(workflow).not.toContain("contents: write");
		expect(workflow).toContain("refs/heads/$DEFAULT_BRANCH");
		expect(workflow).toContain("cancel-in-progress: false");
		expect(workflow).toContain("timeout-minutes: 60");
	});
	test("restores existing product and NF-specific patch originals without publishing or compiling", () => {
		expect(workflow).toContain("bun scripts/restore-123-originals.ts");
		expect(restore).toContain("05a12a3148b8d985a78e24ffe2d1570ae6bbf970");
		expect(restore).toContain("11742723027");
		expect(restore).toContain("11746942787");
		expect(restore).toContain("verifyReleaseBundle(destination, plan)");
		expect(restore).toContain("downloadArchive(artifact");
		expect(workflow).not.toMatch(
			/build-cross-platform|scripts\/release\.ts|gh release|control\.js (publish|mirror)/,
		);
	});
	test("keeps private config out of retained proof and records only public import metadata", () => {
		expect(workflow).toContain("secrets.NF_RELAY_RCLONE_CONFIG");
		expect(workflow).toContain("c.sections()");
		expect(workflow).toContain("webdav.123pan.cn");
		expect(workflow).toContain("0o600");
		const retained = workflow.slice(
			workflow.indexOf("name: Retain public-only"),
			workflow.indexOf("name: Remove only"),
		);
		expect(retained).toContain("receipt-*.json");
		expect(retained).toContain("patches/import-manifest.json");
		expect(retained).not.toContain("rclone.conf");
		expect(retained).not.toContain("123-originals");
		expect(workflow).toContain("unlink(missing_ok=True)");
	});
});
