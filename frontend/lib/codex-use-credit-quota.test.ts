import { afterEach, describe, expect, mock, test } from "bun:test";
import en from "../locales/en/settings.json";
import zh from "../locales/zh-CN/settings.json";
import { miscApi } from "./api/misc";
import { codexCredentialEditForm, saveCodexCredentialEdit } from "./codex-use-credit-quota";

const originalFetch = globalThis.fetch;
const originalStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");
afterEach(() => {
	globalThis.fetch = originalFetch;
	if (originalStorage) Object.defineProperty(globalThis, "localStorage", originalStorage);
	else Reflect.deleteProperty(globalThis, "localStorage");
});

describe("Codex paid credit quota editing", () => {
	test("missing values default to false and existing enabled values are retained", () => {
		expect(codexCredentialEditForm({ priority: 0 })).toEqual({
			displayName: "",
			priority: 0,
			useCreditQuota: false,
		});
		expect(
			codexCredentialEditForm({ displayName: "account", priority: 5, useCreditQuota: true }),
		).toEqual({ displayName: "account", priority: 5, useCreditQuota: true });
	});

	for (const current of [false, undefined]) {
		test(`enabling from ${current} waits for confirmation and cancellation never saves`, async () => {
			const form = codexCredentialEditForm({ priority: 0, useCreditQuota: true });
			const save = mock(() => {});
			let resolve!: (confirmed: boolean) => void;
			const confirm = mock(
				() =>
					new Promise<boolean>((done) => {
						resolve = done;
					}),
			);
			const pending = saveCodexCredentialEdit(current, form, confirm, save);
			expect(confirm).toHaveBeenCalledTimes(1);
			expect(save).not.toHaveBeenCalled();
			resolve(false);
			expect(await pending).toBe(false);
			expect(save).not.toHaveBeenCalled();
		});
	}

	test("confirmed enabling saves the full form, including an intentionally cleared name", async () => {
		const form = codexCredentialEditForm({ priority: 7, useCreditQuota: true });
		const confirm = mock(async () => true);
		const save = mock(() => {});
		expect(await saveCodexCredentialEdit(false, form, confirm, save)).toBe(true);
		expect(confirm).toHaveBeenCalledTimes(1);
		expect(save).toHaveBeenCalledWith({ displayName: "", priority: 7, useCreditQuota: true });
	});

	for (const [current, target] of [
		[true, false],
		[true, true],
		[false, false],
	]) {
		test(`${current} to ${target} saves without confirmation`, async () => {
			const form = codexCredentialEditForm({ priority: 2, useCreditQuota: target });
			const confirm = mock(async () => false);
			const save = mock(() => {});
			expect(await saveCodexCredentialEdit(current, form, confirm, save)).toBe(true);
			expect(confirm).not.toHaveBeenCalled();
			expect(save).toHaveBeenCalledWith(form);
		});
	}

	for (const useCreditQuota of [true, false]) {
		test(`PATCH serializes useCreditQuota=${useCreditQuota} for only the selected credential`, async () => {
			Object.defineProperty(globalThis, "localStorage", {
				configurable: true,
				value: { getItem: () => null },
			});
			const calls: Array<{ url: string; init?: RequestInit }> = [];
			globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
				calls.push({ url: String(input), init });
				return Response.json({ ok: true });
			}) as typeof fetch;
			await miscApi.codexCredentialUpdate("selected-account", { useCreditQuota });
			expect(calls).toHaveLength(1);
			expect(calls[0].url).toEndWith("/codex/credentials/selected-account");
			expect(calls[0].init?.method).toBe("PATCH");
			expect(JSON.parse(String(calls[0].init?.body))).toEqual({ useCreditQuota });
		});
	}

	test("both locales explicitly warn about consuming paid credits", () => {
		expect(en.codexUseCreditQuotaConfirm).toContain("consuming paid credits");
		expect(zh.codexUseCreditQuotaConfirm).toContain("消耗付费积分");
		for (const locale of [en, zh]) {
			expect(locale.codexUseCreditQuota).toBeTruthy();
			expect(locale.codexUseCreditQuotaDescription).toBeTruthy();
			expect(locale.codexUseCreditQuotaBadge).toBeTruthy();
			expect(locale.codexUseCreditQuotaConfirmAction).toBeTruthy();
		}
	});
});
