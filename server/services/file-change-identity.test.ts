import { describe, expect, test } from "bun:test";
import type { FileChangeExecutionBinding, FileChangeIdentity } from "@shared/file-change-protocol";
import {
	createFileChangeIdentity,
	type FileChangeScopeIdentity,
	fileChangeExecutionBindingMatches,
	fileChangeIdentityKey,
	fileChangePhysicalIdentityKey,
	isOutsideFileChangeWorkspace,
} from "./file-change-identity";

const scope: FileChangeScopeIdentity = {
	id: "scope",
	sourceInstanceId: "source",
	deviceId: "local",
	workspaceInstanceId: "workspace",
	pathFlavor: "posix",
	canonicalRoot: "/real/repo",
};

function identity(overrides: Partial<FileChangeIdentity> = {}): FileChangeIdentity {
	return {
		...createFileChangeIdentity(scope, {
			deviceId: "local",
			pathFlavor: "posix",
			lexicalPath: "/link/repo/a.txt",
			canonicalPath: "/real/repo/a.txt",
			objectRole: "referent",
		}),
		...overrides,
	};
}

describe("file change identity", () => {
	test("a symlink cwd uses the canonical root for the Git-shaped display path", () => {
		const result = identity();
		expect(result.displayPath).toBe("a.txt");
		expect(result.lexicalPath).toBe("/link/repo/a.txt");
		expect(fileChangeIdentityKey(result)).toBe(
			fileChangeIdentityKey(identity({ lexicalPath: "/real/repo/a.txt" })),
		);
	});

	test("POSIX backslashes remain filename data and case remains significant", () => {
		const backslash = createFileChangeIdentity(scope, {
			deviceId: "local",
			pathFlavor: "posix",
			canonicalPath: "/real/repo/a\\b.txt",
			lexicalPath: "/real/repo/a\\b.txt",
			objectRole: "referent",
		});
		expect(backslash.displayPath).toBe("a\\b.txt");
		expect(fileChangeIdentityKey(backslash)).not.toBe(
			fileChangeIdentityKey(identity({ canonicalPath: "/real/repo/a/b.txt" })),
		);
		expect(fileChangeIdentityKey(identity())).not.toBe(
			fileChangeIdentityKey(identity({ canonicalPath: "/real/repo/A.txt" })),
		);
	});

	test("Windows aliases agree without treating a remote path as a host path", () => {
		const winScope = {
			...scope,
			deviceId: "remote",
			pathFlavor: "windows" as const,
			canonicalRoot: "C:\\Repo",
		};
		const first = createFileChangeIdentity(winScope, {
			deviceId: "remote",
			pathFlavor: "windows",
			canonicalPath: "C:\\Repo\\Src\\A.ts",
			lexicalPath: "C:/Repo/Src/A.ts",
			objectRole: "referent",
		});
		const second = createFileChangeIdentity(winScope, {
			deviceId: "remote",
			pathFlavor: "windows",
			canonicalPath: "c:/repo/src/a.ts",
			lexicalPath: "c:/repo/src/a.ts",
			objectRole: "referent",
		});
		expect(first.displayPath).toBe("Src/A.ts");
		expect(fileChangeIdentityKey(first)).toBe(fileChangeIdentityKey(second));
	});

	test("devices, workspace incarnations, source instances and object roles do not merge", () => {
		const key = fileChangeIdentityKey(identity());
		for (const patch of [
			{ deviceId: "remote" },
			{ workspaceInstanceId: "recreated" },
			{ sourceInstanceId: "imported" },
			{ objectRole: "entry" },
		] satisfies Partial<FileChangeIdentity>[]) {
			expect(fileChangeIdentityKey(identity(patch))).not.toBe(key);
		}
	});

	test("out-of-workspace targets and virtual targets are not silently local", () => {
		const target = {
			deviceId: "local",
			pathFlavor: "posix" as const,
			lexicalPath: "/elsewhere/a",
			canonicalPath: "/elsewhere/a",
			objectRole: "referent" as const,
		};
		expect(() => createFileChangeIdentity(scope, target)).toThrow("own file scope");
		expect(() => createFileChangeIdentity(scope, { ...target, deviceId: "remote" })).toThrow(
			"does not match",
		);
		expect(() =>
			createFileChangeIdentity(scope, {
				...target,
				pathFlavor: "spec",
				canonicalPath: "spec://tasks.json",
			}),
		).toThrow("does not match");
		expect(() =>
			createFileChangeIdentity(scope, { ...target, canonicalPath: "relative.txt" }),
		).toThrow("absolute");
	});

	test("a filename starting with two dots does not escape the scope", () => {
		const result = createFileChangeIdentity(scope, {
			deviceId: "local",
			pathFlavor: "posix",
			canonicalPath: "/real/repo/..cache",
			lexicalPath: "/real/repo/..cache",
			objectRole: "referent",
		});
		expect(result.displayPath).toBe("..cache");
	});

	test("invalid identity values cannot form apparently valid durable keys", () => {
		expect(() => fileChangeIdentityKey(identity({ deviceId: "" }))).toThrow();
		expect(() => fileChangeIdentityKey(identity({ canonicalPath: "/repo/a\0b" }))).toThrow();
		expect(() => fileChangeIdentityKey(identity({ displayPath: "x".repeat(8193) }))).toThrow();
	});
});

describe("plan-local physical identity", () => {
	test("old and new scopes group the same physical target while historical keys stay distinct", () => {
		const old = identity();
		const current = createFileChangeIdentity(
			{
				...scope,
				id: "new-scope",
				sourceInstanceId: "new-source",
				workspaceInstanceId: "new-workspace",
				canonicalRoot: "/real",
			},
			{
				deviceId: "local",
				pathFlavor: "posix",
				lexicalPath: "/another/cwd/a.txt",
				canonicalPath: old.canonicalPath,
				objectRole: "referent",
			},
		);
		expect(current.displayPath).toBe("repo/a.txt");
		expect(fileChangePhysicalIdentityKey(current)).toBe(fileChangePhysicalIdentityKey(old));
		expect(fileChangeIdentityKey(current)).not.toBe(fileChangeIdentityKey(old));
		expect(
			fileChangePhysicalIdentityKey({
				deviceId: old.deviceId,
				pathFlavor: old.pathFlavor,
				canonicalPath: old.canonicalPath,
				objectRole: old.objectRole,
			}),
		).toBe(fileChangePhysicalIdentityKey(old));
	});
	test("Windows case and separator aliases share a physical key", () => {
		const first = {
			deviceId: "windows-device",
			pathFlavor: "windows" as const,
			canonicalPath: "C:\\Repo\\Src\\A.ts",
			objectRole: "referent" as const,
		};
		expect(fileChangePhysicalIdentityKey(first)).toBe(
			fileChangePhysicalIdentityKey({ ...first, canonicalPath: "c:/repo/src/./a.ts" }),
		);
	});
	test("device and object role isolate physical groups", () => {
		const first = identity();
		for (const patch of [
			{ deviceId: "remote" },
			{ objectRole: "entry" },
		] satisfies Partial<FileChangeIdentity>[]) {
			expect(fileChangePhysicalIdentityKey({ ...first, ...patch })).not.toBe(
				fileChangePhysicalIdentityKey(first),
			);
		}
	});
	test("POSIX case and backslashes remain significant while path segments normalize", () => {
		const first = identity();
		expect(fileChangePhysicalIdentityKey({ ...first, canonicalPath: "/real/repo/./a.txt" })).toBe(
			fileChangePhysicalIdentityKey(first),
		);
		expect(fileChangePhysicalIdentityKey({ ...first, canonicalPath: "/real/repo/A.txt" })).not.toBe(
			fileChangePhysicalIdentityKey(first),
		);
		expect(fileChangePhysicalIdentityKey({ ...first, canonicalPath: "/real/repo/a\\b" })).not.toBe(
			fileChangePhysicalIdentityKey({ ...first, canonicalPath: "/real/repo/a/b" }),
		);
	});
	test("invalid physical targets are rejected before hashing", () => {
		expect(() => fileChangePhysicalIdentityKey(identity({ deviceId: "" }))).toThrow();
		expect(() =>
			fileChangePhysicalIdentityKey(identity({ canonicalPath: "relative.txt" })),
		).toThrow("absolute");
		expect(() =>
			fileChangePhysicalIdentityKey(identity({ canonicalPath: "/repo/a\0b" })),
		).toThrow();
		expect(() => fileChangePhysicalIdentityKey(identity({ pathFlavor: "spec" as never }))).toThrow(
			"disk path flavor",
		);
	});
});

test("workspace comparison preserves unknown and device differences", () => {
	expect(isOutsideFileChangeWorkspace(identity(), scope)).toBe(false);
	expect(isOutsideFileChangeWorkspace(identity(), null)).toBeNull();
	expect(isOutsideFileChangeWorkspace(identity({ deviceId: "remote" }), scope)).toBe(true);
	expect(isOutsideFileChangeWorkspace(identity({ workspaceInstanceId: "new" }), scope)).toBe(true);
});

test("archived v2 receipt comparison preserves its original execution binding", () => {
	const binding: FileChangeExecutionBinding = {
		deviceId: "local",
		runtimeEpoch: "epoch",
		runtimeGeneration: 1,
		fencingToken: 2,
	};
	expect(fileChangeExecutionBindingMatches(binding, { ...binding })).toBe(true);
	for (const patch of [
		{ deviceId: "remote" },
		{ runtimeEpoch: "new-epoch" },
		{ runtimeGeneration: 2 },
		{ fencingToken: 3 },
	]) {
		expect(fileChangeExecutionBindingMatches(binding, { ...binding, ...patch })).toBe(false);
	}
	expect(fileChangeExecutionBindingMatches({ ...binding, fencingToken: Number.NaN }, binding)).toBe(
		false,
	);
	expect(fileChangeExecutionBindingMatches({ ...binding, runtimeGeneration: -1 }, binding)).toBe(
		false,
	);
});
