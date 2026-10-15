import { describe, expect, test } from "bun:test";
import { usageSourceFor } from "./custom-api-usage-source";

describe("usageSourceFor", () => {
	test("picks the source the provider is currently configured for", () => {
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: "p1", kimiProviderId: null }),
		).toBe("opencode");
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: null, kimiProviderId: "p1" }),
		).toBe("kimi");
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: null, kimiProviderId: null }),
		).toBeNull();
	});

	test("a provider edited from Kimi to Go gets no Kimi fallback", () => {
		// Regression: React Query keeps a disabled query's last data, so after the id stops
		// being a Kimi provider the Kimi cache still holds its old entry and never refetches.
		// Before the guard, that stale entry was read under the Go configuration and the
		// previous account's allowance was displayed.
		expect(
			usageSourceFor({
				providerId: "same-id",
				opencodeProviderId: "same-id",
				kimiProviderId: null,
			}),
		).toBe("opencode");
	});

	test("the reverse edit does not surface a stale Go allowance either", () => {
		expect(
			usageSourceFor({
				providerId: "same-id",
				opencodeProviderId: null,
				kimiProviderId: "same-id",
			}),
		).toBe("kimi");
	});

	test("opencode wins when both ids somehow match", () => {
		// Not reachable through settings (a provider has one baseUrl), but the precedence is
		// stated so the branch order in the panel is not load-bearing by accident.
		expect(
			usageSourceFor({ providerId: "p1", opencodeProviderId: "p1", kimiProviderId: "p1" }),
		).toBe("opencode");
	});
});
