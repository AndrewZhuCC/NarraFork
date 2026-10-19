import { describe, expect, test } from "bun:test";
import { parseQueuedGoalCommand, parseQueuedNewCommand } from "../narrator-session";

describe("parseQueuedGoalCommand", () => {
	test("mixed semantic documents never become /new or /goal controls after queuing", () => {
		const document = {
			version: 1 as const,
			paragraphs: [
				{
					children: [
						{ type: "attachment" as const, id: "node", assetId: "asset" },
						{ type: "text" as const, text: "/goal ordinary wording" },
					],
				},
			],
			assets: [
				{ id: "asset", kind: "image" as const, name: "x.png", size: 9, mimeType: "image/png" },
			],
		};
		expect(parseQueuedGoalCommand("/goal ordinary wording", undefined, document)).toBeNull();
		expect(
			parseQueuedNewCommand("/new ordinary wording", "/new ordinary wording", document),
		).toBeNull();
	});
	test("parses a /goal command carried in commandText", () => {
		const parsed = parseQueuedGoalCommand("/goal ship the release", "/goal ship the release");
		expect(parsed).toEqual({ rawCommand: "/goal ship the release", objective: "ship the release" });
	});

	test("parses a /goal command from the message text when commandText is absent", () => {
		const parsed = parseQueuedGoalCommand("/goal write the migration");
		expect(parsed).toEqual({
			rawCommand: "/goal write the migration",
			objective: "write the migration",
		});
	});

	test("trims surrounding whitespace from the objective", () => {
		const parsed = parseQueuedGoalCommand("/goal    fix the flaky test   ");
		expect(parsed?.objective).toBe("fix the flaky test");
	});

	test("returns null for /goal with no objective (falls through to a model turn)", () => {
		expect(parseQueuedGoalCommand("/goal")).toBeNull();
		expect(parseQueuedGoalCommand("/goal    ")).toBeNull();
	});

	test("returns null for a non-goal message", () => {
		expect(parseQueuedGoalCommand("please add a goal for me")).toBeNull();
		expect(parseQueuedGoalCommand("/new explore auth")).toBeNull();
	});

	test("does not match a command that merely starts with the letters 'goal'", () => {
		// `/goalpost` is not the `/goal` command.
		expect(parseQueuedGoalCommand("/goalpost something")).toBeNull();
	});

	test("prefers commandText but falls back to text when commandText is not a /goal", () => {
		// commandText is some other command marker; the message text is the real /goal.
		const parsed = parseQueuedGoalCommand("/goal real objective", "/something else");
		expect(parsed).toEqual({ rawCommand: "/goal real objective", objective: "real objective" });
	});
});
