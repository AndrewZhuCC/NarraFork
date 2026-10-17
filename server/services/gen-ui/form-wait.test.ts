import { describe, expect, test } from "bun:test";
import {
	answerGenUiForm,
	describeGenUiFormOutcome,
	isGenUiFormWaiting,
	readGenUiFormFields,
	waitForGenUiForm,
} from "./form-wait";

describe("GenUI blocking forms", () => {
	test("a submit answers the waiting call of the same conversation only", async () => {
		const controller = new AbortController();
		const waiting = waitForGenUiForm({
			narratorId: "n1",
			toolUseId: "use-1",
			signal: controller.signal,
		});
		expect(isGenUiFormWaiting("n1", "use-1")).toBe(true);
		expect(answerGenUiForm("n2", "use-1", { status: "closed" })).toBe(false);
		expect(
			answerGenUiForm("n1", "use-1", {
				status: "submitted",
				message: "  Use these  ",
				fields: { radius: 24 },
			}),
		).toBe(true);
		const outcome = await waiting;
		expect(outcome).toEqual({ status: "submitted", message: "Use these", fields: { radius: 24 } });
		expect(isGenUiFormWaiting("n1", "use-1")).toBe(false);
		// Answered once: a second click finds nothing waiting.
		expect(answerGenUiForm("n1", "use-1", { status: "closed" })).toBe(false);
		const text = describeGenUiFormOutcome(outcome).output;
		expect(text).toContain('The user submitted the form ("Use these").');
		expect(text).toContain('"radius": 24');
	});

	test("closing, timing out and stopping each end the wait with their own result", async () => {
		const closed = waitForGenUiForm({
			narratorId: "n",
			toolUseId: "use-close",
			signal: new AbortController().signal,
		});
		answerGenUiForm("n", "use-close", { status: "closed" });
		expect(await closed).toEqual({ status: "closed" });
		expect(describeGenUiFormOutcome({ status: "closed" }).output).toContain("closed the form");

		expect(
			await waitForGenUiForm({
				narratorId: "n",
				toolUseId: "use-timeout",
				signal: new AbortController().signal,
				timeoutMs: 5,
			}),
		).toEqual({ status: "timeout" });
		expect(isGenUiFormWaiting("n", "use-timeout")).toBe(false);

		const controller = new AbortController();
		const stopped = waitForGenUiForm({
			narratorId: "n",
			toolUseId: "use-stop",
			signal: controller.signal,
		});
		controller.abort();
		expect(await stopped).toEqual({ status: "aborted" });
		expect(describeGenUiFormOutcome({ status: "aborted" }).isError).toBe(true);
	});

	test("only plain, bounded values pass as fields", () => {
		expect(readGenUiFormFields({ a: "x", b: 2, c: false, d: null, e: ["x", 1] })).toEqual({
			a: "x",
			b: 2,
			c: false,
			d: null,
			e: ["x", 1],
		});
		expect(readGenUiFormFields({ long: "x".repeat(3000) })?.long).toHaveLength(2000);
		expect(readGenUiFormFields({ n: Number.NaN })).toEqual({ n: null });
		for (const bad of [
			null,
			[],
			"text",
			{ "bad name": 1 },
			{ __proto__x: 1, ok: { nested: true } },
			{ deep: [[1]] },
			Object.fromEntries(Array.from({ length: 201 }, (_, i) => [`f${i}`, 1])),
			Object.fromEntries(Array.from({ length: 20 }, (_, i) => [`f${i}`, "x".repeat(2000)])),
		])
			expect(readGenUiFormFields(bad)).toBeNull();
	});
});
