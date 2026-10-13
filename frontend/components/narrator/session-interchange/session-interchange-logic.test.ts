/**
 * What the import modal and export fields offer, and how a write result is shown.
 *
 * Tested on the pure decisions rather than through Mantine's Select: its combobox
 * needs `ShadowRoot`, which linkedom does not provide. The components only map
 * these results to labels.
 */
import { describe, expect, test } from "bun:test";
import type { RemoteDevice } from "../../../lib/api/devices";
import {
	availableExportDestinations,
	availableImportSources,
	downloadOutcome,
	effectiveImportSource,
	formatBytes,
	mergeUploadFiles,
	selectedSessionDevice,
	usableSessionDevices,
	writeOutcome,
} from "./session-interchange-logic";

function device(overrides: Partial<RemoteDevice>): RemoteDevice {
	return {
		id: "d",
		name: "d",
		status: "online",
		revokedAt: null,
		scope: "global",
		projectId: null,
		ownerScope: "shared",
		createdBy: "someone",
		...overrides,
	} as RemoteDevice;
}

const ME = { userId: "me" };

describe("session interchange targets", () => {
	test("the server is offered only to admins, a device only when one is usable", () => {
		expect(availableImportSources(false, 0)).toEqual(["upload"]);
		expect(availableImportSources(true, 0)).toEqual(["upload", "local"]);
		expect(availableImportSources(false, 2)).toEqual(["upload", "device"]);
		expect(availableExportDestinations(true, 1)).toEqual(["download", "local", "device"]);
		expect(availableExportDestinations(false, 0)).toEqual(["download"]);
	});

	test("offline and revoked devices are not usable", () => {
		const usable = usableSessionDevices(
			[
				device({ id: "on" }),
				device({ id: "off", status: "offline" }),
				device({ id: "revoked", revokedAt: "2026-01-01T00:00:00.000Z" }),
			],
			ME,
		);
		expect(usable.map((d) => d.id)).toEqual(["on"]);
		expect(usableSessionDevices(undefined, ME)).toEqual([]);
	});

	test("someone else's private device is not offered, even to an admin who manages it", () => {
		const devices = [
			device({ id: "shared" }),
			device({ id: "mine", ownerScope: "private", createdBy: "me" }),
			device({ id: "theirs", ownerScope: "private", createdBy: "other" }),
		];
		expect(usableSessionDevices(devices, ME).map((d) => d.id)).toEqual(["shared", "mine"]);
		// Before the user is known, no private device qualifies.
		expect(usableSessionDevices(devices, { userId: null }).map((d) => d.id)).toEqual(["shared"]);
	});

	test("a project device is offered only for a narrator in that project", () => {
		const devices = [
			device({ id: "global" }),
			device({ id: "p1", scope: "project", projectId: "p1" }),
			device({ id: "p2", scope: "project", projectId: "p2" }),
		];
		// Import creates standalone narrators: global devices only.
		expect(usableSessionDevices(devices, ME).map((d) => d.id)).toEqual(["global"]);
		expect(usableSessionDevices(devices, { ...ME, projectId: "p1" }).map((d) => d.id)).toEqual([
			"global",
			"p1",
		]);
	});

	test("a device that dropped out of the list is not swapped for another one", () => {
		const list = [device({ id: "a" }), device({ id: "b" })];
		expect(selectedSessionDevice(list, "b")?.id).toBe("b");
		expect(selectedSessionDevice(list, "gone")).toBeNull();
		// Nothing picked: a single device is unambiguous, several are not.
		expect(selectedSessionDevice([device({ id: "only" })], null)?.id).toBe("only");
		expect(selectedSessionDevice(list, null)).toBeNull();
	});

	test("a source that disappeared falls back to upload", () => {
		expect(effectiveImportSource("device", ["upload", "local"])).toBe("upload");
		expect(effectiveImportSource("local", ["upload", "local"])).toBe("local");
	});
});

describe("mergeUploadFiles", () => {
	test("later picks append, a same-named file replaces the earlier one", () => {
		const first = new File(["a"], "session.jsonl");
		const agent = new File(["b"], "agent-1.jsonl");
		const replaced = new File(["cc"], "session.jsonl");
		const merged = mergeUploadFiles(mergeUploadFiles([], [first]), [agent, replaced]);
		expect(merged.map((f) => f.name)).toEqual(["session.jsonl", "agent-1.jsonl"]);
		expect(merged[0]?.size).toBe(2);
	});
});

describe("writeOutcome", () => {
	test("a missing cwd moves out of the warning list into its own flag", () => {
		const outcome = writeOutcome({
			format: "claude-code",
			sessionId: "s1",
			cwd: "/work/app",
			root: "/home/dev/.claude",
			paths: ["/home/dev/.claude/projects/-work-app/s1.jsonl"],
			cwdExists: false,
			resumeCommand: "cd /work/app && claude --resume s1",
			warnings: [
				{ kind: "target_cwd_missing", count: 1, sample: "/work/app" },
				{ kind: "dropped_reasoning", count: 3 },
			],
		});
		expect(outcome.cwdExists).toBe(false);
		// The alert names the cwd the server wrote, not whatever the input holds now.
		expect(outcome.cwd).toBe("/work/app");
		expect(outcome.warnings.map((w) => w.kind)).toEqual(["dropped_reasoning"]);
		expect(outcome.resumeCommand).toBe("cd /work/app && claude --resume s1");
		expect(outcome.placement).toBeNull();
	});
});

describe("downloadOutcome", () => {
	test("a single file says exactly where it goes", () => {
		const outcome = downloadOutcome("claude-code", {
			resumeCommand: "cd /w && claude --resume s1",
			warnings: [],
			placement: "projects/-w/s1.jsonl",
		});
		expect(outcome.placement).toEqual({
			configDir: "~/.claude",
			relativePath: "projects/-w/s1.jsonl",
			isArchive: false,
		});
	});

	test("a zip is extracted into the config dir", () => {
		const outcome = downloadOutcome("codex", {
			resumeCommand: null,
			warnings: [],
			placement: null,
		});
		expect(outcome.placement).toEqual({
			configDir: "~/.codex",
			relativePath: null,
			isArchive: true,
		});
	});
});

test("formatBytes", () => {
	expect(formatBytes(512)).toBe("512 B");
	expect(formatBytes(2048)).toBe("2.0 KB");
	expect(formatBytes(5 * 1024 * 1024)).toBe("5.0 MB");
});
