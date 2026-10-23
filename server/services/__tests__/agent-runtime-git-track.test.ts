import { describe, expect, test } from "bun:test";
import { gitTrackPathForSession } from "../agent-runtime/orchestrator";

describe("gitTrackPathForSession", () => {
	test("narrators track their cwd when it is a local Git repo", () => {
		expect(
			gitTrackPathForSession({
				_isInGitRepo: true,
				_defaultDeviceId: null,
				cwd: "/repo",
			}),
		).toBe("/repo");
	});

	test("narrators outside a Git repo are not tracked", () => {
		expect(
			gitTrackPathForSession({
				_isInGitRepo: false,
				_defaultDeviceId: null,
				cwd: "/tmp",
			}),
		).toBe(null);
	});

	test("narrators on a remote device are not tracked with local git", () => {
		expect(
			gitTrackPathForSession({
				_isInGitRepo: true,
				_defaultDeviceId: "device-a",
				cwd: "/repo",
			}),
		).toBe(null);
	});

	test("an explicit local default device id is tracked", () => {
		expect(
			gitTrackPathForSession({
				_isInGitRepo: true,
				_defaultDeviceId: "local",
				cwd: "/repo",
			}),
		).toBe("/repo");
	});
});
