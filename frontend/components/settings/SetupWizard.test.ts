import { describe, expect, test } from "bun:test";
import { AUTO_LAN_HOST } from "../../../shared/server-host";
import {
	countConfiguredProviders,
	persistSetupWizardBeforeNetworkChange,
	resolveWizardNetworkMode,
	shouldOpenAddProviderPage,
	WIZARD_STEPS,
	wizardNetworkModeToHost,
	wizardNextBlockedReasonKey,
	wizardStepIndex,
} from "./SetupWizard";

describe("setup wizard completion", () => {
	test("persists completion before applying a network change", async () => {
		const calls: string[] = [];
		const response = await persistSetupWizardBeforeNetworkChange(
			"0.0.0.0",
			async () => {
				calls.push("completion");
			},
			async (host) => {
				calls.push(`network:${host}`);
				return { serverRestarting: true };
			},
		);

		expect(calls).toEqual(["completion", "network:0.0.0.0"]);
		expect(response).toEqual({ serverRestarting: true });
	});
});

describe("setup wizard network modes", () => {
	test("recognizes automatic LAN even without a detected address", () => {
		expect(resolveWizardNetworkMode(AUTO_LAN_HOST, [])).toBe("lan");
		expect(resolveWizardNetworkMode(AUTO_LAN_HOST, ["192.168.1.10"])).toBe("lan");
	});

	test("recognizes existing manual addresses on any current LAN interface", () => {
		const addresses = ["192.168.1.10", "10.0.0.5"];
		for (const host of addresses) {
			expect(resolveWizardNetworkMode(host, addresses)).toBe("lan");
		}
		expect(resolveWizardNetworkMode("192.168.1.20", addresses)).toBe("local");
	});

	test("preserves local and open modes", () => {
		for (const host of ["localhost", "127.0.0.1", "::1"]) {
			expect(resolveWizardNetworkMode(host, [])).toBe("local");
		}
		expect(resolveWizardNetworkMode("0.0.0.0", [])).toBe("open");
		expect(wizardNetworkModeToHost("local")).toBe("localhost");
		expect(wizardNetworkModeToHost("open")).toBe("0.0.0.0");
	});

	test("LAN selection saves the automatic intent, not a detected IP", async () => {
		const host = wizardNetworkModeToHost("lan");
		expect(host).toBe(AUTO_LAN_HOST);
		const calls: string[] = [];
		await persistSetupWizardBeforeNetworkChange(
			host,
			async () => {
				calls.push("completion");
			},
			async (savedHost) => {
				calls.push(`network:${savedHost}`);
			},
		);
		expect(calls).toEqual(["completion", "network:auto-lan"]);
		// The staged selection survives a remount or a change of network interfaces.
		expect(resolveWizardNetworkMode(host, [])).toBe("lan");
		expect(resolveWizardNetworkMode(host, ["10.0.0.5"])).toBe("lan");
	});
});

describe("setup wizard step order and gating", () => {
	test("providers and models both come before dependencies", () => {
		// Order matters: dependency installation can be delegated to a Setup
		// Assistant narrator, which is spawned on `settings.agent.defaultModel`.
		// Both the provider credential AND the model selection must therefore be
		// settled before the user can reach the step that offers delegation,
		// otherwise the narrator runs on an unconfigured fallback model.
		expect(wizardStepIndex("provider")).toBeLessThan(wizardStepIndex("deps"));
		expect(wizardStepIndex("basic")).toBeLessThan(wizardStepIndex("deps"));
		// The wizard opens straight onto the provider step; there is no separate welcome page.
		expect(WIZARD_STEPS[0]).toBe("provider");
		expect(WIZARD_STEPS.at(-1)).toBe("complete");
	});

	test("the provider step is a hard gate", () => {
		expect(
			wizardNextBlockedReasonKey({
				step: wizardStepIndex("provider"),
				providerCount: 0,
				basicStepValid: true,
			}),
		).toBe("wizardProviderRequired");
		expect(
			wizardNextBlockedReasonKey({
				step: wizardStepIndex("provider"),
				providerCount: 1,
				basicStepValid: true,
			}),
		).toBeNull();
	});

	test("the dependency step never blocks Next", () => {
		// Missing system dependencies must not trap a first-time user: git only
		// gates project features, and a narrator can install it later.
		expect(
			wizardNextBlockedReasonKey({
				step: wizardStepIndex("deps"),
				providerCount: 0,
				basicStepValid: false,
			}),
		).toBeNull();
	});

	test("the basic step requires the project directory and both models", () => {
		expect(
			wizardNextBlockedReasonKey({
				step: wizardStepIndex("basic"),
				providerCount: 1,
				basicStepValid: false,
			}),
		).toBe("wizardBasicRequired");
		expect(
			wizardNextBlockedReasonKey({
				step: wizardStepIndex("basic"),
				providerCount: 1,
				basicStepValid: true,
			}),
		).toBeNull();
	});
});

describe("setup wizard provider readiness", () => {
	test("allows any registered provider to advance without a frontend allowlist or catalog", () => {
		for (const prefix of ["tokendance", "codex", "future-provider", "plugin-provider"]) {
			const providerCount = countConfiguredProviders({
				providerReadiness: [{ id: `instance:${prefix}`, prefix, name: prefix, ready: true }],
			});
			expect(providerCount).toBe(1);
			expect(
				wizardNextBlockedReasonKey({
					step: wizardStepIndex("provider"),
					providerCount,
					basicStepValid: false,
				}),
			).toBeNull();
		}
	});

	test("counts unique ready instances and ignores unready providers", () => {
		const ready = { id: "api:first", prefix: "custom", name: "Custom", ready: true };
		expect(
			countConfiguredProviders({
				providerReadiness: [
					ready,
					ready,
					{ ...ready, id: "api:second", prefix: "custom-two" },
					{ ...ready, id: "api:disabled", ready: false },
				],
			}),
		).toBe(2);
	});

	test("does not advance before readiness is loaded", () => {
		expect(countConfiguredProviders(undefined)).toBe(0);
		expect(countConfiguredProviders({})).toBe(0);
		expect(countConfiguredProviders({ providerReadiness: [] })).toBe(0);
	});
});

describe("setup wizard add-provider page", () => {
	test("opens the add-provider page only once readiness says nothing is configured", () => {
		expect(
			shouldOpenAddProviderPage({ settingsLoaded: false, providerCount: 0, pathname: "/" }),
		).toBe(false);
		expect(
			shouldOpenAddProviderPage({ settingsLoaded: true, providerCount: 1, pathname: "/" }),
		).toBe(false);
		expect(
			shouldOpenAddProviderPage({ settingsLoaded: true, providerCount: 0, pathname: "/" }),
		).toBe(true);
	});

	test("leaves the user on the providers pages they are already using", () => {
		for (const pathname of [
			"/settings/providers",
			"/settings/providers/",
			"/settings/providers/add",
			"/settings/providers/tokendance/callback",
		]) {
			expect(shouldOpenAddProviderPage({ settingsLoaded: true, providerCount: 0, pathname })).toBe(
				false,
			);
		}
		expect(
			shouldOpenAddProviderPage({
				settingsLoaded: true,
				providerCount: 0,
				pathname: "/settings/providers-other",
			}),
		).toBe(true);
	});
});
