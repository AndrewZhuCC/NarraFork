import { describe, expect, it } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * Bundle-purity boundary for the Kiro client layer.
 *
 * This directory has two consumers that must not share a module graph:
 *
 * - the NarraFork core, which loads it through `kiro-adapter.ts`;
 * - the `kiro-external` plugin, which bundles it and runs it in its own process.
 *
 * The plugin case is why this file exists. `kiro-adapter.ts` already imports this
 * directory through a runtime-constructed path (`./kiro/${"index"}`) so the core
 * compiles when it is absent — the directory is a detachable module by design, and the
 * plugin depends on that staying true.
 *
 * The failure being guarded is silent: a single `../settings` import here would drag the
 * settings graph, Drizzle and `bun:sqlite` into the plugin bundle. Type checking and
 * unit tests would both still pass, and the breakage would only appear as a bloated or
 * non-functional plugin package. `shared/codewhisperer/shared-core.guard.test.ts` guards
 * the protocol layer the same way for the same reason, but it only covers `shared/`.
 *
 * Note this directory legitimately uses a handful of *relative* imports into
 * `server/lib/` (logger, id, narrafork-home, stream-timeout, agent/diagnostic-fetch).
 * Those are allowed: each was verified to pull in nothing beyond node builtins, `undici`
 * and `nanoid`. So this guard forbids the specific heavyweight couplings rather than all
 * relative imports, which would fail on day one and get deleted.
 *
 * ## `index.ts` is host-only, on purpose
 *
 * `index.ts` is the barrel the *core* consumes, and it reads and writes the host's
 * `~/.narrafork/settings.json` to persist the credentials path across restarts. That is
 * correct behaviour for the built-in adapter and unacceptable inside a plugin, which must
 * never touch host settings and gets its config injected over RPC instead.
 *
 * So the plugin bundles the client modules directly rather than the barrel, and the
 * bundle assertion below is scoped to that subset (verified: 116 modules, no settings,
 * database or ORM code). `index.ts` is still covered by the static checks — it is only
 * excluded from the *bundle* check, and `PLUGIN_BUNDLE_ENTRYPOINTS` records exactly what
 * the plugin is allowed to pull in.
 */

const KIRO_DIR = import.meta.dir;

/**
 * The modules `kiro-external` is allowed to bundle.
 *
 * Deliberately excludes `index.ts` (see the header): it is the core's barrel and writes
 * host settings. Adding a module here is a decision to make it plugin-safe, and the
 * bundle assertion below will reject it if it is not.
 */
const PLUGIN_BUNDLE_ENTRYPOINTS = [
	"api-client.ts",
	"auth-method.ts",
	"config.ts",
	"multi-token-manager.ts",
	"quota-check.ts",
	"token-manager.ts",
] as const;

/** Host-only modules, exempt from the plugin bundle check but not the static checks. */
const HOST_ONLY_MODULES = new Set(["index.ts"]);

/** Host modules this layer must never reach, directly or relatively. */
const FORBIDDEN_HOST_MODULES = [
	// The settings barrel is the single worst offender: it transitively reaches the
	// database and the whole provider-config graph.
	"settings",
	"db/schema",
	"db/index",
	"db/connection",
	"event-bus",
	"services/",
	"routes/",
	"websocket/",
] as const;

function kiroSourceFiles(): string[] {
	return readdirSync(KIRO_DIR)
		.filter((name) => name.endsWith(".ts") && !name.endsWith(".test.ts"))
		.sort();
}

describe("kiro plugin bundle boundary", () => {
	it("covers every non-test module in the directory", () => {
		// Guards against a new module being added and silently escaping the checks below,
		// which are otherwise driven by the same listing.
		const files = kiroSourceFiles();
		expect(files.length).toBeGreaterThan(0);
		expect(files).toContain("index.ts");
		expect(files).toContain("api-client.ts");
		expect(files).toContain("token-manager.ts");
	});

	it("does not import host modules through path aliases", () => {
		for (const file of kiroSourceFiles()) {
			const source = readFileSync(join(KIRO_DIR, file), "utf8");
			// `@server/*` is the alias form; using it here would also make the directory
			// unusable outside the host's tsconfig paths.
			expect(source, `${file} must not import @server/*`).not.toMatch(/from\s+["']@server\//);
			expect(source, `${file} must not import @frontend/*`).not.toMatch(/from\s+["']@frontend\//);
			expect(source, `${file} must not dynamically import host modules`).not.toMatch(
				/import\(\s*["'](?:@server\/|@frontend\/)/,
			);
		}
	});

	it("does not reach the database, HTTP server or host settings", () => {
		for (const file of kiroSourceFiles()) {
			const source = readFileSync(join(KIRO_DIR, file), "utf8");
			expect(source, `${file} must not use bun:sqlite`).not.toMatch(/["']bun:sqlite["']/);
			expect(source, `${file} must not use drizzle`).not.toMatch(/from\s+["']drizzle/);
			expect(source, `${file} must not use hono`).not.toMatch(/from\s+["']hono/);
			for (const target of FORBIDDEN_HOST_MODULES) {
				// Matches both `../settings` and `../../server/lib/settings` spellings.
				expect(source, `${file} must not import ${target}`).not.toMatch(
					new RegExp(`from\\s+["'][^"']*\\.\\.?/[^"']*${target.replace("/", "\\/")}["']`),
				);
			}
		}
	});

	it("declares plugin entrypoints that all exist and exclude host-only modules", () => {
		const files = new Set(kiroSourceFiles());
		for (const entry of PLUGIN_BUNDLE_ENTRYPOINTS) {
			expect(files.has(entry), `${entry} is listed but missing`).toBe(true);
			expect(HOST_ONLY_MODULES.has(entry), `${entry} is host-only`).toBe(false);
		}
	});

	it("bundles the plugin subset without pulling in host infrastructure", async () => {
		// The static checks above can only see this directory. This is the actual contract:
		// whatever the relative imports transitively reach, the bundle must contain no
		// database, ORM or host-settings code.
		const built = await Bun.build({
			entrypoints: PLUGIN_BUNDLE_ENTRYPOINTS.map((name) => join(KIRO_DIR, name)),
			target: "bun",
		});
		expect(built.success, built.logs.map((entry) => String(entry)).join("\n")).toBe(true);

		for (const output of built.outputs) {
			const code = await output.text();
			for (const marker of ["bun:sqlite", "drizzle-orm"]) {
				expect(code, `bundle must not contain ${marker}`).not.toInclude(marker);
			}
			// A settings-graph leak shows up as the host's own config file name. This is what
			// keeps `index.ts` (which writes settings.json) from creeping into the subset.
			expect(code, "bundle must not embed the host settings file").not.toInclude("settings.json");
		}
	});

	it("keeps the host-only barrel out of the plugin subset", async () => {
		// Asserts the exclusion is load-bearing rather than incidental: if `index.ts` ever
		// stopped touching host settings this test would fail and the exemption could be
		// removed deliberately, instead of the guard quietly protecting nothing.
		const source = readFileSync(join(KIRO_DIR, "index.ts"), "utf8");
		expect(source, "index.ts is exempt because it persists host settings").toInclude(
			"settings.json",
		);
	});
});
