import { describe, expect, test } from "bun:test";
import { botActivity } from "./bot-activity";

describe("botActivity", () => {
	test("a running turn is thinking", () => {
		expect(botActivity("working", ["reasoning"])).toBe("thinking");
	});

	test("waiting is a decision only when a person can make one", () => {
		expect(botActivity("waiting", [])).toBe("decision");
		expect(botActivity("waiting", ["model_unavailable"])).toBe("model");
		expect(botActivity("waiting", ["quota_exhausted"])).toBe("model");
	});

	test("a failed turn is surfaced instead of reading as silence", () => {
		expect(botActivity("idle", ["error"])).toBe("error");
		expect(botActivity("idle", ["timeout"])).toBe("error");
	});

	test("a finished or interrupted turn says nothing", () => {
		expect(botActivity("idle", ["unread"])).toBeNull();
		expect(botActivity("idle", ["interrupted"])).toBeNull();
		expect(botActivity(undefined, undefined)).toBeNull();
	});
});
