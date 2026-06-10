import { Database } from "bun:sqlite";
import { Buffer } from "node:buffer";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repoRoot = resolve(import.meta.dir, "..", "..");
const goRoot = resolve(repoRoot, "go_backend");
const packageJson = JSON.parse(readFileSync(resolve(repoRoot, "package.json"), "utf-8")) as {
	version?: string;
};
const contractAppVersion = packageJson.version ?? "0.0.0";
const contractNextVersion = nextPatchVersion(contractAppVersion);

type JsonSample = {
	status: number;
	contentType: string;
	keys: string[];
	kind: string;
	body: unknown;
};
type SseSample = {
	status: number;
	contentType: string;
	events: string[];
	terminal: string;
	bodyPreview: string;
};
type WsSample = { opened: boolean; firstMessageType?: string; error?: string };
type BackendSample = {
	json: Record<string, JsonSample>;
	sse: Record<string, SseSample>;
	ws: Record<string, WsSample>;
};

function nextPatchVersion(version: string): string {
	const parts = version.split(".").map((part) => Number.parseInt(part, 10));
	if (parts.length !== 3 || parts.some((part) => !Number.isFinite(part))) return "9.9.9";
	return `${parts[0]}.${parts[1]}.${parts[2] + 1}`;
}

function envFor(
	home: string,
	port: number,
	extra: Record<string, string> = {},
): Record<string, string> {
	return {
		...process.env,
		HOME: home,
		NARRAFORK_ALLOW_MULTIPLE: "1",
		NARRAFORK_DISABLE_OPEN_BROWSER: "1",
		HOST: "127.0.0.1",
		PORT: String(port),
		APP_VERSION: contractAppVersion,
		NARRAFORK_VERSION: contractAppVersion,
		...extra,
	};
}

function providerFixtureEnv(providerFixtureUrl: string): Record<string, string> {
	return {
		NARRAFORK_CONTRACT_PROVIDER_FIXTURE_URL: providerFixtureUrl,
		NARRAFORK_CLINE_API_BASE_URL: `${providerFixtureUrl}/cline`,
		NARRAFORK_CLINE_OPENROUTER_MODELS_URL: `${providerFixtureUrl}/openrouter/api/v1/models`,
		NARRAFORK_CLINE_RECOMMENDED_MODELS_URL: `${providerFixtureUrl}/cline/api/v1/ai/cline/recommended-models`,
	};
}

async function freePort(): Promise<number> {
	return await new Promise((resolvePort, reject) => {
		const server = createServer();
		server.once("error", reject);
		server.listen(0, "127.0.0.1", () => {
			const addr = server.address();
			if (!addr || typeof addr === "string") {
				reject(new Error("could not allocate port"));
				return;
			}
			const port = addr.port;
			server.close(() => resolvePort(port));
		});
	});
}

function startProviderModelFixtureServer(): { url: string; stop: () => void } {
	const server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		async fetch(req) {
			const url = new URL(req.url);
			if (url.pathname === "/cline/api/v1/auth/authorize") {
				const callbackUrl = url.searchParams.get("callback_url") ?? "";
				const redirectUrl = new URL("/cline/oauth/authorize", url.origin);
				redirectUrl.searchParams.set("callback_url", callbackUrl);
				redirectUrl.searchParams.set("state", "cline-browser-fixture");
				return Response.json({ redirect_url: redirectUrl.toString() });
			}
			if (url.pathname === "/cline/api/v1/auth/refresh") {
				const body = await req.json().catch(() => ({}));
				if (recordBody(body).refreshToken !== "cline-expired-refresh-token") {
					return Response.json({ error: "bad cline refresh token", body }, { status: 401 });
				}
				return Response.json({
					success: true,
					data: {
						accessToken: "cline-refreshed-access-token",
						refreshToken: "cline-refreshed-refresh-token",
						expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(),
						userInfo: { email: "cline-refreshed@example.com", name: "Cline Refreshed User" },
					},
				});
			}
			if (url.pathname === "/cline/api/v1/users/me") {
				const auth = req.headers.get("authorization") ?? "";
				if (!auth.startsWith("Bearer workos:cline-")) {
					return Response.json({ error: "bad cline auth" }, { status: 401 });
				}
				const refreshUser =
					auth.includes("cline-expired-access-token") ||
					auth.includes("cline-refreshed-access-token");
				return Response.json({
					data: refreshUser
						? {
								id: "cline-refresh-user-fixture",
								email: "cline-refresh-userinfo@example.com",
								displayName: "Cline Refresh UserInfo",
								name: "Cline Refresh UserInfo Fallback",
							}
						: {
								id: "cline-user-fixture",
								email: "cline-updated@example.com",
								displayName: "Cline Updated User",
								name: "Cline Updated Fallback",
							},
				});
			}
			if (url.pathname === "/cline/api/v1/users/cline-user-fixture/balance") {
				if (!req.headers.get("authorization")?.startsWith("Bearer workos:cline-")) {
					return Response.json({ error: "bad cline auth" }, { status: 401 });
				}
				return Response.json({
					success: true,
					data: { balance: 123456, userId: "cline-user-fixture" },
				});
			}
			if (url.pathname === "/cline/api/v1/users/cline-refresh-user-fixture/balance") {
				if (req.headers.get("authorization") !== "Bearer workos:cline-refreshed-access-token") {
					return Response.json({ error: "bad cline refreshed auth" }, { status: 401 });
				}
				return Response.json({
					success: true,
					data: { balance: 654321, userId: "cline-refresh-user-fixture" },
				});
			}
			if (url.pathname === "/cline/api/v1/ai/cline/recommended-models") {
				return Response.json({
					recommended: [
						{
							id: "cline/recommended-fixture",
							name: "Cline Recommended Fixture",
							description: "Fixture recommended model",
							tags: ["BEST"],
						},
					],
					free: [
						{
							id: "cline/free-fixture",
							name: "Cline Free Fixture",
							tags: ["FREE"],
						},
						{
							id: "cline/free-live-fixture",
							name: "Cline Free Live Fixture",
							description: "Fixture live free model",
							tags: ["FREE", "LIVE"],
						},
					],
				});
			}
			if (url.pathname === "/openrouter/api/v1/models") {
				return Response.json({
					data: [
						{
							id: "cline/refresh-b",
							name: "Cline Refresh B",
							context_length: 200000,
							pricing: { prompt: "0.000002", completion: "0.000004" },
						},
						{
							id: "cline/refresh-a",
							name: "Cline Refresh A",
							context_length: 100000,
							pricing: { prompt: "0.000001", completion: "0.000003" },
						},
						{ id: "cline/refresh-a", name: "Duplicate Cline Refresh A" },
					],
				});
			}
			if (url.pathname === "/openai/v1/models") {
				if (req.headers.get("authorization") !== "Bearer sk-openai-secret") {
					return Response.json({ error: "bad auth" }, { status: 401 });
				}
				return Response.json({
					data: [
						{ id: "gpt-contract-b", created: 2, object: "model" },
						{ id: "gpt-contract-a", created: 1, object: "model" },
						{ id: "gpt-contract-a", created: 1, object: "model" },
					],
				});
			}
			if (url.pathname === "/anthropic/v1/models") {
				if (
					req.headers.get("x-api-key") !== "anthropic-key" ||
					!req.headers.get("anthropic-version")
				) {
					return Response.json({ error: "bad auth" }, { status: 401 });
				}
				return Response.json({
					data: [
						{ id: "claude-contract-b", display_name: "Claude Contract B" },
						{ id: "claude-contract-a", display_name: "Claude Contract A" },
						{ id: "claude-contract-a", display_name: "Claude Contract A" },
					],
				});
			}
			if (url.pathname === "/nkp/v1/kiro/models") {
				if (req.headers.get("authorization") !== "Bearer nkp-key") {
					return Response.json({ error: "bad auth" }, { status: 401 });
				}
				return Response.json({
					data: [
						{ id: "nkp-contract-b", model_name: "Claude NKP B", rate_multiplier: 2 },
						{ model_id: "nkp-contract-a", model_name: "Claude NKP A" },
						{ id: "nkp-contract-a", model_name: "Claude NKP A" },
					],
				});
			}
			if (url.pathname === "/nkp/v1/quota") {
				if (req.headers.get("authorization") !== "Bearer nkp-key") {
					return Response.json({ error: "bad auth" }, { status: 401 });
				}
				return Response.json({ quotaBalance: 12.34, quotaTotalGranted: 56.78 });
			}
			if (url.pathname === "/nug/api/auth/login") {
				if (req.method !== "POST") return new Response("method not allowed", { status: 405 });
				return Response.json(
					{ user: { id: "nug-user-1", username: "alice" } },
					{ headers: { "Set-Cookie": "nug_session=session-login-token; Path=/; HttpOnly" } },
				);
			}
			if (url.pathname === "/nug/api/api-keys") {
				if (req.method !== "POST") return new Response("method not allowed", { status: 405 });
				const cookie = req.headers.get("cookie") ?? "";
				if (
					!cookie.includes("nug_session=session-login-token") &&
					!cookie.includes("nug_session=manual-session")
				) {
					return Response.json({ error: "missing session" }, { status: 401 });
				}
				return Response.json({ apiKey: "nug-generated-key", id: "nug-key-1" });
			}
			if (url.pathname === "/nug/api/oauth/token") {
				if (req.method !== "POST") return new Response("method not allowed", { status: 405 });
				const body = (await req.json().catch(() => ({}))) as Record<string, unknown>;
				if (
					body.code !== "oauth-code" ||
					body.client_id !== "nug-oauth-client" ||
					body.client_secret !== "nug-oauth-client-secret" ||
					typeof body.redirect_uri !== "string" ||
					!body.redirect_uri.endsWith("/api/nug/oauth/callback")
				) {
					return Response.json({ error: "bad oauth token request", body }, { status: 400 });
				}
				return Response.json({
					api_key: "nug-oauth-api-key",
					device_id: "nug-oauth-device",
					username: "oauth-alice",
					user_id: "nug-oauth-user-1",
				});
			}
			if (url.pathname.startsWith("/nug/v1/")) {
				if (req.headers.get("authorization") !== "Bearer sk-nug-secret") {
					return Response.json({ error: "bad auth" }, { status: 401 });
				}
				if (url.pathname === "/nug/v1/models") {
					return Response.json({
						models: [
							{ id: "nug-contract-b", owned_by: "nug" },
							{ id: "nug-contract-a", owned_by: "nug" },
							{ id: "nug-contract-a", owned_by: "dupe" },
						],
					});
				}
				if (url.pathname === "/nug/v1/quota") {
					return Response.json({ balance: 12.5, totalGranted: 50, username: "nug-fixture" });
				}
				if (url.pathname === "/nug/v1/channels/health") {
					return Response.json({
						channels: [
							{
								channelType: "kiro",
								totalCredentials: 2,
								availableCredentials: 1,
								disabledCredentials: 1,
								availabilityRate: 0.5,
								currentConcurrency: 1,
								maxConcurrency: 4,
								queueDepth: 0,
							},
						],
					});
				}
				if (url.pathname === "/nug/v1/usage") {
					if (url.searchParams.get("limit") !== "2" || url.searchParams.get("offset") !== "0") {
						return Response.json({ error: "bad query" }, { status: 400 });
					}
					return Response.json({
						events: [
							{
								channel_type: "kiro",
								model_id: "claude-nug-fixture",
								input_tokens: "11",
								output_tokens: 22,
								cache_read_tokens: 3,
								quota_cost: "6.7",
								meter_usage: 8,
								status: "ok",
								created_at: "2026-05-29T00:00:00.000Z",
							},
						],
						total: 1,
					});
				}
				if (url.pathname === "/nug/v1/usage/summary") {
					if (url.searchParams.get("period") !== "month") {
						return Response.json({ error: "bad period" }, { status: 400 });
					}
					return Response.json({
						request_count: "3",
						total_meter_usage: "8.9",
						total_quota_cost: "6.7",
						total_input_tokens: "11",
						total_output_tokens: "22",
						total_cache_write_tokens: "5",
						total_cache_read_tokens: "7",
					});
				}
			}
			return new Response("not found", { status: 404 });
		},
	});
	return { url: server.url.origin, stop: () => server.stop(true) };
}

function startUpdateFixtureServer(): { url: string; stop: () => void } {
	const artifact = new TextEncoder().encode("narrafork contract update artifact\n");
	const artifactSha512 = createHash("sha512").update(artifact).digest("base64");
	const server = Bun.serve({
		hostname: "127.0.0.1",
		port: 0,
		fetch(req) {
			const url = new URL(req.url);
			if (url.pathname === "/api/v2/products/narrafork-no-update/releases/latest") {
				return Response.json({ updateAvailable: false, version: contractAppVersion });
			}
			if (url.pathname === "/api/v2/products/narrafork-download-error/releases/latest") {
				return Response.json({
					updateAvailable: true,
					version: contractNextVersion,
					releaseDate: "2026-05-29T00:00:00.000Z",
					releaseNotes: "Contract update download-error fixture",
					file: {
						filename: "missing-contract-update.bin",
						size: artifact.byteLength,
						sha512: artifactSha512,
					},
				});
			}
			if (url.pathname === "/api/v2/products/narrafork/releases/latest") {
				return Response.json({
					updateAvailable: true,
					version: contractNextVersion,
					releaseDate: "2026-05-29T00:00:00.000Z",
					releaseNotes: "Contract update fixture",
					file: {
						filename: "narrafork-contract-update.bin",
						size: artifact.byteLength,
						sha512: artifactSha512,
					},
				});
			}
			if (url.pathname.endsWith("/narrafork-contract-update.bin")) {
				return new Response(artifact, {
					headers: { "Content-Type": "application/octet-stream" },
				});
			}
			return new Response("not found", { status: 404 });
		},
	});
	return { url: server.url.origin, stop: () => server.stop(true) };
}

function writeTsFetchPreload(home: string): string {
	const preloadPath = join(home, "contract-fetch-preload.ts");
	writeFileSync(
		preloadPath,
		`const fixtureUrl = process.env.NARRAFORK_CONTRACT_PROVIDER_FIXTURE_URL;\n` +
			`const originalFetch = globalThis.fetch.bind(globalThis);\n` +
			`globalThis.fetch = (input, init) => {\n` +
			`\tconst rawUrl = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;\n` +
			`\tif (fixtureUrl) {\n` +
			`\t\tlet url;\n` +
			`\t\ttry {\n` +
			`\t\t\turl = new URL(rawUrl);\n` +
			`\t\t} catch {\n` +
			`\t\t\treturn originalFetch(input, init);\n` +
			`\t\t}\n` +
			`\t\tif (url.origin === "https://openrouter.ai" && url.pathname === "/api/v1/models") {\n` +
			`\t\t\treturn originalFetch(fixtureUrl + "/openrouter/api/v1/models", init);\n` +
			`\t\t}\n` +
			`\t\tif (url.origin === "https://api.cline.bot" && url.pathname === "/api/v1/auth/refresh") {\n` +
			`\t\t\treturn originalFetch(fixtureUrl + "/cline/api/v1/auth/refresh", init);\n` +
			`\t\t}\n` +
			`\t\tif (url.origin === "https://api.cline.bot" && url.pathname.startsWith("/api/v1/users/")) {\n` +
			`\t\t\treturn originalFetch(fixtureUrl + "/cline" + url.pathname, init);\n` +
			`\t\t}\n` +
			`\t\tif (url.origin === "https://api.cline.bot" && url.pathname === "/api/v1/ai/cline/recommended-models") {\n` +
			`\t\t\treturn originalFetch(fixtureUrl + "/cline/api/v1/ai/cline/recommended-models", init);\n` +
			`\t\t}\n` +
			`\t}\n` +
			`\treturn originalFetch(input, init);\n` +
			`};\n`,
	);
	return preloadPath;
}

function spawnTS(
	home: string,
	port: number,
	preloadPath: string,
	providerFixtureUrl: string,
): Bun.Subprocess {
	return Bun.spawn(
		["bun", "--preload", preloadPath, "server/main.ts", `--port=${port}`, "--host=127.0.0.1"],
		{
			cwd: repoRoot,
			env: envFor(home, port, providerFixtureEnv(providerFixtureUrl)),
			stdout: "pipe",
			stderr: "pipe",
		},
	);
}

async function buildGoBinary(home: string): Promise<string> {
	const binPath = join(home, "narrafork-go-contract");
	const proc = Bun.spawn(["go", "build", "-o", binPath, "./cmd/narrafork-go"], {
		cwd: goRoot,
		stdout: "pipe",
		stderr: "pipe",
	});
	const code = await proc.exited;
	if (code !== 0) {
		const logs = await processLogs(proc);
		throw new Error(
			`go build failed with ${code}\nstdout:\n${logs.stdout}\nstderr:\n${logs.stderr}`,
		);
	}
	return binPath;
}

function spawnGo(
	home: string,
	port: number,
	binPath: string,
	providerFixtureUrl: string,
): Bun.Subprocess {
	return Bun.spawn([binPath, "-port", String(port), "-host", "127.0.0.1"], {
		cwd: goRoot,
		env: envFor(home, port, providerFixtureEnv(providerFixtureUrl)),
		stdout: "pipe",
		stderr: "pipe",
	});
}

async function waitHealth(baseUrl: string, label: string): Promise<void> {
	const deadline = Date.now() + 120_000;
	let lastErr = "";
	while (Date.now() < deadline) {
		try {
			const res = await fetch(`${baseUrl}/api/health`);
			if (res.ok) return;
			lastErr = `status ${res.status}`;
		} catch (err) {
			lastErr = err instanceof Error ? err.message : String(err);
		}
		await Bun.sleep(150);
	}
	throw new Error(`${label} did not become healthy: ${lastErr}`);
}

async function stopProcess(proc: Bun.Subprocess): Promise<void> {
	try {
		proc.kill("SIGTERM");
		await Promise.race([proc.exited, Bun.sleep(5_000)]);
	} catch {
		// best effort
	}
	try {
		proc.kill("SIGKILL");
	} catch {
		// already exited
	}
}

async function processLogs(proc: Bun.Subprocess): Promise<{ stdout: string; stderr: string }> {
	const [stdout, stderr] = await Promise.all([
		proc.stdout ? new Response(proc.stdout).text().catch(() => "") : Promise.resolve(""),
		proc.stderr ? new Response(proc.stderr).text().catch(() => "") : Promise.resolve(""),
	]);
	return { stdout: stdout.slice(-4000), stderr: stderr.slice(-4000) };
}

async function registerUser(baseUrl: string, username: string): Promise<string> {
	const res = await fetch(`${baseUrl}/api/auth/register`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ username, password: "password123", language: "en" }),
	});
	if (!res.ok) throw new Error(`register ${username} failed: ${res.status} ${await res.text()}`);
	const body = (await res.json()) as { token?: string };
	if (!body.token) throw new Error(`register ${username} response did not include token`);
	return body.token;
}

async function registerAdmin(baseUrl: string): Promise<string> {
	return registerUser(baseUrl, "admin");
}

function writeSkillFixture(dir: string, name: string, description: string, content: string): void {
	mkdirSync(dir, { recursive: true });
	writeFileSync(
		join(dir, "SKILL.md"),
		`---\nname: ${JSON.stringify(name)}\ndescription: ${JSON.stringify(description)}\n---\n\n${content}\n`,
		"utf-8",
	);
}

function seedClineModelCacheFixture(home: string): void {
	const dataDir = join(home, ".narrafork");
	mkdirSync(dataDir, { recursive: true });
	writeFileSync(
		join(dataDir, "cline-models.json"),
		JSON.stringify(
			{
				"cline-cache-fixture": [
					{
						id: "cline/free-fixture",
						name: "Cline Free Fixture",
						contextLength: 123456,
					},
					{
						id: "cline/paid-fixture",
						name: "Cline Paid Fixture",
						contextLength: 654321,
					},
				],
			},
			null,
			2,
		),
		"utf-8",
	);
}

function seedPrestartProviderFixtures(home: string): void {
	const dataDir = join(home, ".narrafork");
	mkdirSync(dataDir, { recursive: true });
	writeFileSync(
		join(dataDir, "settings.json"),
		JSON.stringify(
			{
				clineProviders: [
					{
						id: "cline-cache-fixture",
						name: "Cline Cache Fixture",
						prefix: "clinecache",
						baseUrl: "https://api.cline.bot/api/v1",
						defaultModel: "cline/free-fixture",
						enabledModels: [],
					},
				],
			},
			null,
			2,
		),
		"utf-8",
	);
	seedClineModelCacheFixture(home);
	writeFileSync(
		join(dataDir, "custom-api-quotas.json"),
		JSON.stringify(
			{
				"openai-secret-fixture": {
					quotaBalance: "$42.50",
					detailedQuotaBalance: "42.50 credits\nreset soon",
					fetchedAt: 1_700_000_000_000,
				},
			},
			null,
			2,
		),
		"utf-8",
	);
	writeFileSync(
		join(dataDir, "codex-credentials.json"),
		JSON.stringify(
			[
				{
					id: "codex-contract",
					displayName: "Codex Contract Fixture",
					accessToken: "codex-access-fixture",
					refreshToken: "codex-refresh-fixture",
					accountId: "codex-account-fixture",
					email: "codex-contract@example.com",
					sub: "codex-sub-fixture",
					priority: 0,
					disabled: false,
				},
				{
					id: "codex-invalid-no-token",
					displayName: "Codex Invalid No Token",
				},
				{
					id: "codex-duplicate-refresh",
					displayName: "Codex Duplicate Refresh",
					accessToken: "codex-duplicate-access",
					refreshToken: "codex-refresh-fixture",
					accountId: "codex-duplicate-account",
					email: "codex-duplicate@example.com",
					sub: "codex-duplicate-sub",
				},
			],
			null,
			2,
		),
		"utf-8",
	);
	writeFileSync(
		join(dataDir, "cline-recommended-models.json"),
		JSON.stringify(
			{
				recommended: [],
				free: [
					{
						id: "cline/free-fixture",
						name: "Cline Free Fixture",
						tags: ["FREE"],
					},
				],
			},
			null,
			2,
		),
		"utf-8",
	);
}

function writeMcpHelperFixture(home: string): void {
	writeFileSync(
		join(home, "mcp-contract-helper.mjs"),
		`import readline from "node:readline";
const tools = [
  {
    name: "read_file",
    description: "Read a contract fixture file",
    inputSchema: {
      type: "object",
      properties: { path: { type: "string", description: "Path to read" } },
      required: ["path"],
    },
  },
  {
    name: "ask_tool",
    description: "Ask before acting",
    inputSchema: {
      type: "object",
      properties: { path: { type: "string" } },
    },
  },
];
function send(message) {
  process.stdout.write(JSON.stringify(message) + "\\n");
}
const rl = readline.createInterface({ input: process.stdin });
rl.on("line", (line) => {
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    return;
  }
  const id = request.id;
  if (request.method === "initialize") {
    send({
      jsonrpc: "2.0",
      id,
      result: {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "contract-helper", version: "1.0.0" },
      },
    });
    return;
  }
  if (request.method === "notifications/initialized") {
    return;
  }
  if (request.method === "tools/list") {
    send({ jsonrpc: "2.0", id, result: { tools } });
    return;
  }
  if (request.method === "tools/call") {
    const params = request.params || {};
    const args = params.arguments || {};
    send({
      jsonrpc: "2.0",
      id,
      result: {
        content: [{ type: "text", text: "called " + params.name + " " + (args.path || "") }],
        isError: false,
      },
    });
    return;
  }
  send({ jsonrpc: "2.0", id, error: { code: -32601, message: "method not found" } });
});
`,
		"utf-8",
	);
}

const gitPanelFixtureChapterId = "c_search_fixture";
const gitPanelFixtureBaseTag = "git-panel-base";

function gitPanelFixtureRepoPath(home: string): string {
	return join(home, "git-panel-fixture-repo");
}

function projectCreateFixtureRepoPath(home: string): string {
	return join(home, "project-create-fixture-repo");
}

function chapterCreateFixtureRepoPath(home: string): string {
	return join(home, "chapter-create-fixture-repo");
}

function chapterDormantWakeFixtureRepoPath(home: string): string {
	return join(home, "chapter-dormant-wake-fixture-repo");
}

function chapterDormantWakeFixtureWorktreePath(home: string): string {
	return join(chapterDormantWakeFixtureRepoPath(home), ".worktrees", "dormant-wake-fixture");
}

function projectDeleteFixtureRepoPath(home: string): string {
	return join(home, "project-delete-fixture-repo");
}

function projectRealCleanupWarningRepoPath(home: string): string {
	return join(home, "project-real-cleanup-warning-repo");
}

function projectRealCleanupWarningWorktreePath(home: string): string {
	return join(projectRealCleanupWarningRepoPath(home), ".worktrees", "stale-non-worktree");
}

function projectDeleteFixtureWorktreePath(home: string): string {
	return join(projectDeleteFixtureRepoPath(home), ".worktrees", "delete-branch");
}

type GitFixtureResult = { success: boolean; stdout: string; stderr: string; exitCode: number };

function runGitPanelFixture(
	repoPath: string,
	args: string[],
	options: { allowFailure?: boolean } = {},
): GitFixtureResult {
	const result = Bun.spawnSync(["git", ...args], {
		cwd: repoPath,
		env: { ...process.env, GIT_TERMINAL_PROMPT: "0" },
		stdout: "pipe",
		stderr: "pipe",
	});
	const stdout = Buffer.from(result.stdout).toString("utf-8").trim();
	const stderr = Buffer.from(result.stderr).toString("utf-8").trim();
	const exitCode = result.exitCode ?? 0;
	const success = exitCode === 0;
	if (!success && !options.allowFailure) {
		throw new Error(
			`git fixture command failed: git ${args.join(" ")} (exit ${exitCode})\nstdout=${stdout}\nstderr=${stderr}`,
		);
	}
	return { success, stdout, stderr, exitCode };
}

function gitPanelHeadCommit(repoPath: string): string {
	return runGitPanelFixture(repoPath, ["rev-parse", "HEAD"]).stdout;
}

function ensureProjectCreateFixtureRepo(home: string): string {
	const repoPath = projectCreateFixtureRepoPath(home);
	rmSync(repoPath, { recursive: true, force: true });
	mkdirSync(repoPath, { recursive: true });
	runGitPanelFixture(repoPath, ["init"]);
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	writeFileSync(join(repoPath, "tracked.txt"), "project create base\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed project create fixture"]);
	writeFileSync(join(repoPath, "second.txt"), "project create second\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "second.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: add project create second commit"]);
	runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	return repoPath;
}

function ensureChapterCreateFixtureRepo(home: string): string {
	const repoPath = chapterCreateFixtureRepoPath(home);
	rmSync(repoPath, { recursive: true, force: true });
	mkdirSync(repoPath, { recursive: true });
	runGitPanelFixture(repoPath, ["init"]);
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	writeFileSync(join(repoPath, "tracked.txt"), "chapter create base\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed chapter create fixture"]);
	writeFileSync(join(repoPath, "second.txt"), "chapter create second\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "second.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: add chapter create second commit"]);
	runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	return repoPath;
}

function ensureChapterDormantWakeFixtureRepo(home: string): string {
	const repoPath = chapterDormantWakeFixtureRepoPath(home);
	const worktreePath = chapterDormantWakeFixtureWorktreePath(home);
	rmSync(repoPath, { recursive: true, force: true });
	mkdirSync(repoPath, { recursive: true });
	runGitPanelFixture(repoPath, ["init"]);
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	writeFileSync(join(repoPath, "tracked.txt"), "dormant wake base\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed dormant wake fixture"]);
	runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	runGitPanelFixture(repoPath, ["branch", "dormant-wake-fixture"]);
	runGitPanelFixture(repoPath, ["worktree", "add", worktreePath, "dormant-wake-fixture"]);
	writeFileSync(join(worktreePath, "wake.txt"), "wake branch content\n", "utf-8");
	runGitPanelFixture(worktreePath, ["add", "wake.txt"]);
	runGitPanelFixture(worktreePath, ["commit", "-m", "chore: add dormant wake branch commit"]);
	return repoPath;
}

function ensureProjectRealCleanupWarningRepo(home: string): string {
	const repoPath = projectRealCleanupWarningRepoPath(home);
	const worktreePath = projectRealCleanupWarningWorktreePath(home);
	rmSync(repoPath, { recursive: true, force: true });
	mkdirSync(repoPath, { recursive: true });
	runGitPanelFixture(repoPath, ["init"]);
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	writeFileSync(join(repoPath, "tracked.txt"), "project real cleanup warning base\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed real cleanup warning fixture"]);
	runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	mkdirSync(worktreePath, { recursive: true });
	return repoPath;
}

function ensureProjectDeleteFixtureRepo(home: string): string {
	const repoPath = projectDeleteFixtureRepoPath(home);
	const worktreePath = projectDeleteFixtureWorktreePath(home);
	rmSync(repoPath, { recursive: true, force: true });
	mkdirSync(repoPath, { recursive: true });
	runGitPanelFixture(repoPath, ["init"]);
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	writeFileSync(join(repoPath, "tracked.txt"), "project delete base\n", "utf-8");
	runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
	runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed project delete fixture"]);
	runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	runGitPanelFixture(repoPath, ["branch", "delete-fixture"]);
	mkdirSync(join(repoPath, ".worktrees"), { recursive: true });
	runGitPanelFixture(repoPath, ["worktree", "add", worktreePath, "delete-fixture"]);
	return repoPath;
}

function ensureGitPanelFixtureRepo(home: string): string {
	const repoPath = gitPanelFixtureRepoPath(home);
	mkdirSync(repoPath, { recursive: true });
	if (!existsSync(join(repoPath, ".git"))) {
		runGitPanelFixture(repoPath, ["init"]);
	}
	runGitPanelFixture(repoPath, ["config", "user.email", "contract@example.test"]);
	runGitPanelFixture(repoPath, ["config", "user.name", "Contract Fixture"]);
	const hasHead = runGitPanelFixture(repoPath, ["rev-parse", "--verify", "HEAD"], {
		allowFailure: true,
	}).success;
	if (!hasHead) {
		writeFileSync(join(repoPath, "tracked.txt"), "base\n", "utf-8");
		runGitPanelFixture(repoPath, ["add", "tracked.txt"]);
		runGitPanelFixture(repoPath, ["commit", "-m", "chore: seed git panel fixture"]);
		runGitPanelFixture(repoPath, ["branch", "-M", "main"]);
	}
	const hasBaseTag = runGitPanelFixture(
		repoPath,
		["rev-parse", "--verify", gitPanelFixtureBaseTag],
		{ allowFailure: true },
	).success;
	if (!hasBaseTag) {
		runGitPanelFixture(repoPath, ["tag", "-f", gitPanelFixtureBaseTag, "HEAD"]);
	}
	resetGitPanelFixture(home, { dirty: false });
	return repoPath;
}

function resetGitPanelFixture(home: string, options: { dirty: boolean }): string {
	const repoPath = gitPanelFixtureRepoPath(home);
	if (!existsSync(join(repoPath, ".git"))) {
		throw new Error(`git panel fixture repo is missing at ${repoPath}`);
	}
	runGitPanelFixture(repoPath, ["checkout", "-f", "-B", "main", gitPanelFixtureBaseTag]);
	runGitPanelFixture(repoPath, ["reset", "--hard", gitPanelFixtureBaseTag]);
	runGitPanelFixture(repoPath, ["clean", "-fd"]);
	rmSync(join(repoPath, "notes.txt"), { force: true });
	if (options.dirty) {
		writeFileSync(join(repoPath, "tracked.txt"), "base\nchanged\n", "utf-8");
		writeFileSync(join(repoPath, "notes.txt"), "new note\n", "utf-8");
	}
	return repoPath;
}

function seedSearchFixture(home: string): void {
	writeMcpHelperFixture(home);
	const projectRepo = join(home, "search-fixture-repo");
	const gitPanelRepo = ensureGitPanelFixtureRepo(home);
	const gitPanelHead = gitPanelHeadCommit(gitPanelRepo);
	const fsFixtureDir = join(home, "fs-contract");
	mkdirSync(join(fsFixtureDir, "visible"), { recursive: true });
	mkdirSync(join(fsFixtureDir, ".hidden"), { recursive: true });
	writeFileSync(join(fsFixtureDir, "note.txt"), "fs contract text fixture\n", "utf-8");
	mkdirSync(join(home, ".narrafork", "uploads", "n_upload_fixture"), { recursive: true });
	writeFileSync(join(home, ".narrafork", "uploads", "n_upload_fixture", "img1234.png"), "PNGDATA");
	mkdirSync(join(home, ".narrafork", "uploads", "avatars", "u_upload_fixture"), {
		recursive: true,
	});
	writeFileSync(
		join(home, ".narrafork", "uploads", "avatars", "u_upload_fixture", "avatar1234.png"),
		"AVATARPNG",
	);
	writeSkillFixture(
		join(home, ".narrafork", "skills", "global-contract"),
		"global-contract",
		"Global contract skill",
		"Global contract instructions",
	);
	writeSkillFixture(
		join(projectRepo, ".narrafork", "skills", "project-contract"),
		"project-contract",
		"Project contract skill",
		"Project contract instructions",
	);

	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		db.exec("PRAGMA busy_timeout = 5000");
		const now = "2026-05-27T00:00:00.000Z";
		const largeRecent = "2026-05-29T00:00:00.000Z";
		const largeMiddle = "2026-05-28T12:00:00.000Z";
		const largeOlder = "2026-05-26T00:00:00.000Z";
		db.run(
			`INSERT OR IGNORE INTO projects (id, name, description, status, flow_mode, git_path, default_branch, created_at, updated_at)
			 VALUES (?, ?, ?, 'active', 'classic', ?, 'main', ?, ?)`,
			[
				"p_search_fixture",
				"Search Fixture Project",
				"Project used by TS-Go contract search fixture",
				projectRepo,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
			[
				"c_search_fixture",
				"p_search_fixture",
				"叙事分叉 Alpha Chapter",
				"Needle description body for contract search parity",
				"search-fixture",
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
			[
				"c_graph_target",
				"p_search_fixture",
				"Graph Target Chapter",
				"Graph dependency target for contract parity",
				"graph-target",
				now,
				now,
			],
		);
		db.run(
			`UPDATE chapters SET start_commit_sha = ?, anchor_commit_sha = ?, axis_offset = ?, cross_offset = ?, panel_width = ?, panel_height = ? WHERE id = ?`,
			["commit-alpha", "commit-alpha", 10, 20, 320, 180, "c_search_fixture"],
		);
		db.run(
			`UPDATE chapters SET start_commit_sha = ?, anchor_commit_sha = ?, axis_offset = ?, cross_offset = ? WHERE id = ?`,
			["commit-alpha", "commit-alpha", 40, 60, "c_graph_target"],
		);
		for (const [chapterId, title, branch] of [
			["c_dep_lifecycle_source", "Dependency Lifecycle Source", "dep-life-source"],
			["c_dep_lifecycle_target", "Dependency Lifecycle Target", "dep-life-target"],
		]) {
			db.run(
				`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
				 VALUES (?, ?, ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
				[
					chapterId,
					"p_search_fixture",
					title,
					"Dependency lifecycle E2E contract fixture",
					branch,
					now,
					now,
				],
			);
		}
		db.run(
			`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
			[
				"c_delete_fixture",
				"p_search_fixture",
				"Delete Fixture Chapter",
				"Chapter delete TS-Go diff fixture",
				"delete-fixture",
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, message_count, status, substatus, plan_mode, prune_enabled, variant, traits, created_at, updated_at)
			 VALUES (?, ?, 'primary', ?, 'fresh', 'claude-sonnet-4.5', 'default', 0, 0, 'inherit', 'inherit', 0, 'idle', '[]', 0, 1, 'primary', '[]', ?, ?)`,
			["n_delete_fixture", "c_delete_fixture", "Delete Fixture Narrator", now, now],
		);
		db.run(
			`UPDATE chapters SET worktree_path = ?, head_commit_sha = ?, commit_count = ? WHERE id = ?`,
			[gitPanelRepo, gitPanelHead, 1, gitPanelFixtureChapterId],
		);
		db.run(
			`INSERT OR IGNORE INTO exploration_groups (id, project_id, title, description, base_chapter_id, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, 'active', ?, ?)`,
			[
				"eg_graph_fixture",
				"p_search_fixture",
				"Graph Exploration Fixture",
				"Exploration group for graph contract parity",
				"c_search_fixture",
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at)
			 VALUES (?, ?, ?, ?, 'dependency', ?, ?)`,
			[
				"e_graph_dependency",
				"p_search_fixture",
				"c_search_fixture",
				"c_graph_target",
				JSON.stringify({ description: "Graph dependency", lastSyncedCommit: null }),
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO volume_snapshots (id, project_id, name, description, source_chapter_id, service_name, container_path, size_bytes, created_by, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			[
				"vs_contract_fixture",
				"p_search_fixture",
				"Contract DB Snapshot",
				"Snapshot fixture for contract parity",
				"c_search_fixture",
				"db",
				"/var/lib/postgresql/data",
				1234,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO volume_snapshot_applications (id, snapshot_id, chapter_id, applied_at, applied_by)
			 VALUES (?, ?, ?, ?, NULL)`,
			["vsa_contract_fixture", "vs_contract_fixture", "c_graph_target", now],
		);
		db.run(
			`INSERT OR IGNORE INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, message_count, status, substatus, plan_mode, prune_enabled, variant, traits, created_at, updated_at)
			 VALUES (?, ?, 'primary', ?, 'fresh', 'claude-sonnet-4.5', 'default', 0, 0, 'inherit', 'inherit', 1, 'idle', '["unread"]', 0, 1, 'primary', '[]', ?, ?)`,
			["n_search_fixture", "c_search_fixture", "叙事分叉 Alpha Narrator", now, now],
		);
		db.run(`UPDATE narrators SET status = 'idle', substatus = '["unread"]' WHERE id = ?`, [
			"n_search_fixture",
		]);
		db.run(
			`INSERT OR IGNORE INTO narrator_messages (id, narrator_id, role, content_json, content_text, model, created_at)
			 VALUES (?, ?, 'user', ?, ?, 'claude-sonnet-4.5', ?)`,
			[
				"m_search_fixture",
				"n_search_fixture",
				JSON.stringify([{ type: "text", text: "叙事分叉 message content with Alpha contract" }]),
				"叙事分叉 message content with Alpha contract",
				now,
			],
		);
		for (const [id, title, description, branch, createdAt, updatedAt] of [
			[
				"c_search_large_title",
				"auroraanchor Chapter North",
				"auroraanchor ranking fixture chapter with matching title for expanded search parity",
				"search-large-title",
				largeMiddle,
				largeRecent,
			],
			[
				"c_search_large_description",
				"Expanded Search South",
				"auroraanchor chapter description collision for realistic relevance parity",
				"search-large-description",
				largeOlder,
				largeMiddle,
			],
			[
				"c_search_tie_recent",
				"chrononeedle Tie Chapter",
				"chrononeedle identical timestamp ordering body",
				"search-tie-recent",
				largeMiddle,
				largeRecent,
			],
			[
				"c_search_tie_older",
				"chrononeedle Tie Chapter",
				"chrononeedle identical timestamp ordering body",
				"search-tie-older",
				largeMiddle,
				largeOlder,
			],
		] as const) {
			db.run(
				`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
				 VALUES (?, 'p_search_fixture', ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
				[id, title, description, branch, createdAt, updatedAt],
			);
		}
		for (const [id, chapterId, title, createdAt, updatedAt, lastMessageAt] of [
			[
				"n_search_large_primary",
				"c_search_large_title",
				"auroraanchor Primary Narrator",
				largeMiddle,
				largeRecent,
				largeRecent,
			],
			[
				"n_search_large_secondary",
				"c_search_large_description",
				"auroraanchor Secondary Narrator",
				largeOlder,
				largeMiddle,
				largeMiddle,
			],
		] as const) {
			db.run(
				`INSERT OR IGNORE INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, message_count, status, substatus, plan_mode, prune_enabled, variant, traits, created_at, updated_at, last_message_at)
				 VALUES (?, ?, 'primary', ?, 'fresh', 'claude-sonnet-4.5', 'default', 0, 0, 'inherit', 'inherit', 1, 'idle', '[]', 0, 1, 'primary', '[]', ?, ?, ?)`,
				[id, chapterId, title, createdAt, updatedAt, lastMessageAt],
			);
		}
		for (const [id, narratorId, text, createdAt] of [
			[
				"m_search_large_primary",
				"n_search_large_primary",
				"auroraanchor message collision with project chapter narrator context",
				largeRecent,
			],
			[
				"m_search_large_secondary",
				"n_search_large_secondary",
				"auroraanchor message collision with older timestamp context",
				largeMiddle,
			],
		] as const) {
			db.run(
				`INSERT OR IGNORE INTO narrator_messages (id, narrator_id, role, content_json, content_text, model, created_at)
				 VALUES (?, ?, 'assistant', ?, ?, 'claude-sonnet-4.5', ?)`,
				[id, narratorId, JSON.stringify([{ type: "text", text }]), text, createdAt],
			);
		}
	} finally {
		db.close();
	}
}

function resetProjectCreateFixture(home: string): void {
	ensureProjectCreateFixtureRepo(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		db.run(
			`DELETE FROM chapter_commits WHERE chapter_id IN (SELECT id FROM chapters WHERE project_id IN (SELECT id FROM projects WHERE name = 'Project Create Fixture'))`,
		);
		db.run(
			`DELETE FROM narrators WHERE chapter_id IN (SELECT id FROM chapters WHERE project_id IN (SELECT id FROM projects WHERE name = 'Project Create Fixture'))`,
		);
		db.run(
			`DELETE FROM chapters WHERE project_id IN (SELECT id FROM projects WHERE name = 'Project Create Fixture')`,
		);
		db.run(`DELETE FROM projects WHERE name = 'Project Create Fixture'`);
	} finally {
		db.close();
	}
}

function resetChapterCreateFixture(home: string): void {
	const repoPath = ensureChapterCreateFixtureRepo(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		db.run(
			`DELETE FROM chapter_commits WHERE chapter_id IN (SELECT id FROM chapters WHERE project_id = 'p_chapter_create_fixture')`,
		);
		db.run(
			`DELETE FROM narrators WHERE chapter_id IN (SELECT id FROM chapters WHERE project_id = 'p_chapter_create_fixture')`,
		);
		db.run(`DELETE FROM chapters WHERE project_id = 'p_chapter_create_fixture'`);
		db.run(`DELETE FROM projects WHERE id = 'p_chapter_create_fixture'`);
		const now = "2026-05-27T00:00:00.000Z";
		db.run(
			`INSERT INTO projects (id, name, description, status, flow_mode, git_path, default_branch, chapter_settings, created_at, updated_at)
			 VALUES ('p_chapter_create_fixture', 'Chapter Create Fixture Project', 'Chapter create TS-Go DB/Git/FS diff fixture', 'active', 'classic', ?, 'main', ?, ?, ?)`,
			[repoPath, JSON.stringify({ autoCreateNarrator: true }), now, now],
		);
	} finally {
		db.close();
	}
}

function chapterCreateFixtureState(home: string): JsonSample {
	const repoPath = chapterCreateFixtureRepoPath(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const chapter = db
			.query(
				`SELECT id, branch, worktree_path, base_branch, status, role, COALESCE(is_root, 0) AS is_root, COALESCE(head_commit_sha, '') AS head_commit_sha, COALESCE(commit_count, 0) AS commit_count
				 FROM chapters WHERE project_id = 'p_chapter_create_fixture' AND title = 'Chapter Create Fixture' ORDER BY created_at DESC LIMIT 1`,
			)
			.get() as {
			id?: string;
			branch?: string;
			worktree_path?: string;
			base_branch?: string;
			status?: string;
			role?: string;
			is_root?: number;
			head_commit_sha?: string;
			commit_count?: number;
		} | null;
		const chapterID = chapter?.id ?? "__missing__";
		const branch = chapter?.branch ?? "";
		const worktreePath = chapter?.worktree_path ?? "";
		const branchList = branch
			? runGitPanelFixture(repoPath, ["branch", "--list", branch], { allowFailure: true }).stdout
			: "";
		const narrator = db
			.query(
				`SELECT cwd FROM narrators WHERE chapter_id = '${chapterID}' AND variant = 'primary' LIMIT 1`,
			)
			.get() as { cwd?: string | null } | null;
		const repoHead = existsSync(join(repoPath, ".git")) ? gitPanelHeadCommit(repoPath) : "";
		return jsonSampleFromBody({
			projectRows: countRows(db, "projects", "id = 'p_chapter_create_fixture'"),
			chapterRows: countRows(
				db,
				"chapters",
				"project_id = 'p_chapter_create_fixture' AND title = 'Chapter Create Fixture'",
			),
			narratorRows: countRows(db, "narrators", `chapter_id = '${chapterID}'`),
			commitRows: countRows(db, "chapter_commits", `chapter_id = '${chapterID}'`),
			manualCommitRows: countRows(
				db,
				"chapter_commits",
				`chapter_id = '${chapterID}' AND source = 'manual'`,
			),
			repoExists: existsSync(join(repoPath, ".git")),
			branchExists: branchList.includes(branch) && branch !== "",
			branchHasExpectedPrefix: branch.startsWith("chapter/chapter-create-fixture-"),
			worktreeExists: worktreePath !== "" && existsSync(worktreePath),
			worktreePathInRepo: worktreePath.startsWith(join(repoPath, ".worktrees")),
			worktreeHasTrackedFiles:
				worktreePath !== "" &&
				existsSync(join(worktreePath, "tracked.txt")) &&
				existsSync(join(worktreePath, "second.txt")),
			baseBranch: chapter?.base_branch ?? "",
			status: chapter?.status ?? "",
			role: chapter?.role ?? "",
			isRoot: Number(chapter?.is_root ?? 0),
			headMatchesRepo: (chapter?.head_commit_sha ?? "") === repoHead,
			commitCount: Number(chapter?.commit_count ?? 0),
			narratorCwdNull: narrator !== null && narrator.cwd === null,
		});
	} finally {
		db.close();
	}
}

function resetChapterDormantWakeFixture(home: string): void {
	const repoPath = ensureChapterDormantWakeFixtureRepo(home);
	const worktreePath = chapterDormantWakeFixtureWorktreePath(home);
	const head = runGitPanelFixture(worktreePath, ["rev-parse", "HEAD"]).stdout;
	const commitCount = Number(
		runGitPanelFixture(worktreePath, ["rev-list", "--count", "HEAD"]).stdout,
	);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		db.run(`DELETE FROM chapter_commits WHERE chapter_id = 'c_dormant_wake_fixture'`);
		db.run(`DELETE FROM chapters WHERE project_id = 'p_dormant_wake_fixture'`);
		db.run(`DELETE FROM projects WHERE id = 'p_dormant_wake_fixture'`);
		const now = "2026-05-27T00:00:00.000Z";
		db.run(
			`INSERT INTO projects (id, name, description, status, flow_mode, git_path, default_branch, created_at, updated_at)
			 VALUES ('p_dormant_wake_fixture', 'Chapter Dormant Wake Fixture Project', 'Chapter dormant/wake TS-Go DB/FS diff fixture', 'active', 'classic', ?, 'main', ?, ?)`,
			[repoPath, now, now],
		);
		db.run(
			`INSERT INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, start_commit_sha, commit_count, created_at, updated_at)
			 VALUES ('c_dormant_wake_fixture', 'p_dormant_wake_fixture', 'Dormant Wake Fixture', 'Chapter dormant/wake TS-Go DB/FS diff fixture', 'active', 'branch', 'dormant-wake-fixture', ?, 'main', 0, ?, ?, ?, ?, ?)`,
			[worktreePath, head, head, commitCount, now, now],
		);
	} finally {
		db.close();
	}
}

function chapterDormantWakeFixtureState(home: string): JsonSample {
	const repoPath = chapterDormantWakeFixtureRepoPath(home);
	const knownWorktreePath = chapterDormantWakeFixtureWorktreePath(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const chapter = db
			.query(
				`SELECT id, status, branch, worktree_path, COALESCE(head_commit_sha, '') AS head_commit_sha, COALESCE(commit_count, 0) AS commit_count
				 FROM chapters WHERE id = 'c_dormant_wake_fixture' LIMIT 1`,
			)
			.get() as {
			id?: string;
			status?: string;
			branch?: string;
			worktree_path?: string | null;
			head_commit_sha?: string;
			commit_count?: number;
		} | null;
		const branch = chapter?.branch ?? "";
		const branchHead = branch
			? runGitPanelFixture(repoPath, ["rev-parse", branch], { allowFailure: true }).stdout
			: "";
		const worktreePath = chapter?.worktree_path ?? "";
		return jsonSampleFromBody({
			projectRows: countRows(db, "projects", "id = 'p_dormant_wake_fixture'"),
			chapterRows: countRows(db, "chapters", "id = 'c_dormant_wake_fixture'"),
			commitRows: countRows(db, "chapter_commits", "chapter_id = 'c_dormant_wake_fixture'"),
			manualCommitRows: countRows(
				db,
				"chapter_commits",
				"chapter_id = 'c_dormant_wake_fixture' AND source = 'manual'",
			),
			status: chapter?.status ?? "",
			branchExists:
				branch !== "" &&
				runGitPanelFixture(repoPath, ["branch", "--list", branch], {
					allowFailure: true,
				}).stdout.includes(branch),
			worktreePathNull: chapter !== null && !chapter.worktree_path,
			worktreeExists: worktreePath !== "" && existsSync(worktreePath),
			knownWorktreeExists: existsSync(knownWorktreePath),
			worktreePathInRepo: worktreePath.startsWith(join(repoPath, ".worktrees")),
			headMatchesBranch: branchHead !== "" && (chapter?.head_commit_sha ?? "") === branchHead,
			commitCount: Number(chapter?.commit_count ?? 0),
		});
	} finally {
		db.close();
	}
}

function dormantWakeResponseSample(sample: JsonSample, _home: string): JsonSample {
	const body = recordBody(sample.body);
	return jsonSampleFromBody(
		{
			ok: sample.status === 200 && (body.ok === true || typeof body.id === "string"),
		},
		sample.status,
	);
}

async function createChapterCreateFixture(
	baseUrl: string,
	token: string,
	home: string,
): Promise<JsonSample> {
	const sample = await sampleJSON(baseUrl, "/api/chapters", token, {
		method: "POST",
		body: {
			projectId: "p_chapter_create_fixture",
			title: "Chapter Create Fixture",
			description: "Chapter create TS-Go DB/Git/FS diff fixture",
		},
	});
	const body = recordBody(sample.body);
	const branch = typeof body.branch === "string" ? body.branch : "";
	const worktreePath = typeof body.worktreePath === "string" ? body.worktreePath : "";
	return jsonSampleFromBody(
		{
			idIsString: typeof body.id === "string",
			projectId: body.projectId,
			title: body.title,
			status: body.status,
			role: body.role,
			branchHasExpectedPrefix: branch.startsWith("chapter/chapter-create-fixture-"),
			worktreePathInFixtureRepo: worktreePath.startsWith(
				join(chapterCreateFixtureRepoPath(home), ".worktrees"),
			),
		},
		sample.status,
	);
}

function seedProjectRealCleanupWarningFixture(home: string): void {
	const repoPath = ensureProjectRealCleanupWarningRepo(home);
	const worktreePath = projectRealCleanupWarningWorktreePath(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const now = "2026-05-27T00:00:00.000Z";
		db.run(
			`INSERT OR REPLACE INTO projects (id, name, description, status, flow_mode, git_path, default_branch, created_at, updated_at)
			 VALUES (?, ?, ?, 'active', 'classic', ?, 'main', ?, ?)`,
			[
				"p_real_cleanup_fixture",
				"Project Real Cleanup Warning Fixture",
				"Project delete real git cleanup warning fixture",
				repoPath,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR REPLACE INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, is_root, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'trunk', 'main', ?, 'main', 1, ?, ?)`,
			[
				"c_real_cleanup_root_fixture",
				"p_real_cleanup_fixture",
				"Project Real Cleanup Root",
				"Root chapter for real cleanup fixture",
				repoPath,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR REPLACE INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, is_root, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', 'stale-cleanup-fixture', ?, 'main', 0, ?, ?)`,
			[
				"c_real_cleanup_branch_fixture",
				"p_real_cleanup_fixture",
				"Project Real Cleanup Branch",
				"Branch with a stale non-worktree path for real git cleanup warning parity",
				worktreePath,
				now,
				now,
			],
		);
	} finally {
		db.close();
	}
}

function seedProjectDeleteFixture(home: string): void {
	const repoPath = ensureProjectDeleteFixtureRepo(home);
	const worktreePath = projectDeleteFixtureWorktreePath(home);
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const now = "2026-05-27T00:00:00.000Z";
		db.run(
			`INSERT OR REPLACE INTO projects (id, name, description, status, flow_mode, git_path, default_branch, created_at, updated_at)
			 VALUES (?, ?, ?, 'active', 'classic', ?, 'main', ?, ?)`,
			[
				"p_delete_fixture",
				"Project Delete Fixture",
				"Project delete TS-Go DB/FS diff fixture",
				repoPath,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR REPLACE INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, is_root, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'trunk', 'main', ?, 'main', 1, ?, ?)`,
			[
				"c_project_delete_root_fixture",
				"p_delete_fixture",
				"Project Delete Root",
				"Root chapter for project delete fixture",
				repoPath,
				now,
				now,
			],
		);
		db.run(
			`INSERT OR REPLACE INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, is_root, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', 'delete-fixture', ?, 'main', 0, ?, ?)`,
			[
				"c_project_delete_branch_fixture",
				"p_delete_fixture",
				"Project Delete Branch",
				"Branch/worktree chapter for project delete fixture",
				worktreePath,
				now,
				now,
			],
		);
		const admin = db.query("SELECT id FROM users WHERE username = 'admin' LIMIT 1").get() as {
			id?: string;
		} | null;
		if (admin?.id) {
			db.run(
				`INSERT OR REPLACE INTO user_preferences (id, user_id, recent_tabs, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?)`,
				[
					"pref_project_delete_fixture",
					admin.id,
					JSON.stringify([
						{ type: "project", id: "p_delete_fixture", title: "Project Delete Fixture" },
						{
							type: "chapter",
							id: "c_project_delete_branch_fixture",
							title: "Project Delete Branch",
						},
					]),
					now,
					now,
				],
			);
		}
	} finally {
		db.close();
	}
}

function seedChapterDeleteFixture(home: string): void {
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const now = "2026-05-27T00:00:00.000Z";
		db.run(
			`INSERT OR IGNORE INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'active', 'branch', ?, 'main', ?, ?)`,
			[
				"c_delete_fixture",
				"p_search_fixture",
				"Delete Fixture Chapter",
				"Chapter delete TS-Go diff fixture",
				"delete-fixture",
				now,
				now,
			],
		);
		db.run(
			`INSERT OR IGNORE INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, message_count, status, substatus, plan_mode, prune_enabled, variant, traits, created_at, updated_at)
			 VALUES (?, ?, 'primary', ?, 'fresh', 'claude-sonnet-4.5', 'default', 0, 0, 'inherit', 'inherit', 0, 'idle', '[]', 0, 1, 'primary', '[]', ?, ?)`,
			["n_delete_fixture", "c_delete_fixture", "Delete Fixture Narrator", now, now],
		);
	} finally {
		db.close();
	}
}

function bodyKind(body: unknown): string {
	if (Array.isArray(body)) return "array";
	if (body && typeof body === "object") return "object";
	return typeof body;
}

function countRows(db: Database, table: string, where: string): number {
	const row = db.query(`SELECT COUNT(*) AS count FROM ${table} WHERE ${where}`).get() as
		| { count?: number }
		| undefined;
	return Number(row?.count ?? 0);
}

function projectCreateFixtureState(home: string): JsonSample {
	const repoPath = projectCreateFixtureRepoPath(home);
	const backupPath = join(repoPath, ".narrafork", "project.db");
	const gitignorePath = join(repoPath, ".gitignore");
	const gitignore = existsSync(gitignorePath) ? readFileSync(gitignorePath, "utf-8") : "";
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const project = db
			.query(
				`SELECT id, git_path, default_branch FROM projects WHERE name = 'Project Create Fixture' ORDER BY created_at DESC LIMIT 1`,
			)
			.get() as { id?: string; git_path?: string; default_branch?: string } | null;
		const projectID = project?.id ?? "__missing__";
		return jsonSampleFromBody({
			projectRows: countRows(db, "projects", "name = 'Project Create Fixture'"),
			rootChapterRows: countRows(
				db,
				"chapters",
				`project_id = '${projectID}' AND is_root = 1 AND role = 'trunk'`,
			),
			rootCommitRows: countRows(
				db,
				"chapter_commits",
				`chapter_id IN (SELECT id FROM chapters WHERE project_id = '${projectID}' AND is_root = 1)`,
			),
			manualRootCommitRows: countRows(
				db,
				"chapter_commits",
				`chapter_id IN (SELECT id FROM chapters WHERE project_id = '${projectID}' AND is_root = 1) AND source = 'manual'`,
			),
			backupDbExists: existsSync(backupPath),
			gitignoreHasNarrafork: gitignore.includes(".narrafork/"),
			gitignoreHasWorktrees: gitignore.includes(".worktrees/"),
			repoExists: existsSync(join(repoPath, ".git")),
			gitPathMatchesFixture: project?.git_path === repoPath,
			defaultBranch: project?.default_branch ?? "",
		});
	} finally {
		db.close();
	}
}

function projectRealCleanupWarningFixtureState(home: string): JsonSample {
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		return jsonSampleFromBody({
			projectRows: countRows(db, "projects", "id = 'p_real_cleanup_fixture'"),
			chapterRows: countRows(
				db,
				"chapters",
				"id IN ('c_real_cleanup_root_fixture','c_real_cleanup_branch_fixture')",
			),
			rootRepoExists: existsSync(projectRealCleanupWarningRepoPath(home)),
			staleWorktreeExists: existsSync(projectRealCleanupWarningWorktreePath(home)),
		});
	} finally {
		db.close();
	}
}

function projectDeleteFixtureState(home: string): JsonSample {
	const db = new Database(join(home, ".narrafork", "narrafork.db"));
	try {
		const prefs = db
			.query(`SELECT recent_tabs FROM user_preferences WHERE id = 'pref_project_delete_fixture'`)
			.get() as { recent_tabs?: string } | null;
		return jsonSampleFromBody({
			projectRows: countRows(db, "projects", "id = 'p_delete_fixture'"),
			chapterRows: countRows(
				db,
				"chapters",
				"id IN ('c_project_delete_root_fixture','c_project_delete_branch_fixture')",
			),
			recentTabsContainDeletedProject: (prefs?.recent_tabs ?? "").includes("p_delete_fixture"),
			rootRepoExists: existsSync(projectDeleteFixtureRepoPath(home)),
			branchWorktreeExists: existsSync(projectDeleteFixtureWorktreePath(home)),
		});
	} finally {
		db.close();
	}
}

function seededSettingsProviders(
	masked = false,
	openAIBaseUrl = "https://openai.example.test",
	anthropicBaseUrl = "https://anthropic.example.test",
	nkpBaseUrl = "https://nkp.example.test",
	nugBaseUrl = "https://nug.example.test",
): Record<string, unknown> {
	return {
		openaiProviders: [
			{
				id: "openai-secret-fixture",
				name: "OpenAI Secret Fixture",
				prefix: "seedopenai",
				apiKey: masked ? "********cret" : "sk-openai-secret",
				baseUrl: openAIBaseUrl,
				defaultModel: "gpt-test",
			},
			{
				id: "openai-no-key-fixture",
				name: "OpenAI No Key Fixture",
				prefix: "seedopenainokey",
				apiKey: "",
				baseUrl: openAIBaseUrl,
				defaultModel: "gpt-no-key-test",
			},
		],
		anthropicProviders: [
			{
				id: "anthropic-secret-fixture",
				name: "Anthropic Secret Fixture",
				prefix: "seedanthropic",
				apiKey: masked ? "********cret" : "anthropic-key",
				baseUrl: anthropicBaseUrl,
				defaultModel: "claude-test",
			},
			{
				id: "anthropic-no-key-fixture",
				name: "Anthropic No Key Fixture",
				prefix: "seedanthropicnokey",
				apiKey: "",
				baseUrl: anthropicBaseUrl,
				defaultModel: "claude-no-key-test",
			},
		],
		nkpProviders: [
			{
				id: "nkp-secret-fixture",
				name: "NKP Secret Fixture",
				prefix: "seednkp",
				apiKey: masked ? "********cret" : "nkp-key",
				baseUrl: nkpBaseUrl,
				defaultModel: "nkp-test",
			},
			{
				id: "nkp-no-key-fixture",
				name: "NKP No Key Fixture",
				prefix: "seednkpnokey",
				apiKey: "",
				baseUrl: nkpBaseUrl,
				defaultModel: "nkp-no-key-test",
			},
		],
		nugProviders: [
			{
				id: "nug-secret-fixture",
				name: "NUG Secret Fixture",
				prefix: "seednug",
				apiKey: masked ? "********cret" : "sk-nug-secret",
				oauthClientSecret: masked ? "********cret" : "nug-oauth-secret",
				baseUrl: nugBaseUrl,
				defaultModel: "nug-test",
			},
			{
				id: "nug-login-fixture",
				name: "NUG Login Fixture",
				prefix: "seednuglogin",
				apiKey: "",
				baseUrl: nugBaseUrl,
				defaultModel: "nug-login-test",
			},
			{
				id: "nug-oauth-fixture",
				name: "NUG OAuth Fixture",
				prefix: "seednugoauth",
				apiKey: "",
				oauthClientId: "nug-oauth-client",
				oauthClientSecret: masked ? "********cret" : "nug-oauth-client-secret",
				baseUrl: nugBaseUrl,
				defaultModel: "nug-oauth-test",
			},
		],
	};
}

function jsonSampleFromBody(body: unknown, status = 200): JsonSample {
	return {
		status,
		contentType: "application/json",
		keys: body && typeof body === "object" && !Array.isArray(body) ? Object.keys(body).sort() : [],
		kind: bodyKind(body),
		body,
	};
}

async function jsonSampleFromResponse(res: Response): Promise<JsonSample> {
	const contentType = res.headers.get("content-type") ?? "";
	const rawBody = await res.text();
	let body: unknown = rawBody;
	try {
		body = rawBody ? JSON.parse(rawBody) : null;
	} catch {
		body = rawBody;
	}
	return {
		status: res.status,
		contentType,
		keys: body && typeof body === "object" && !Array.isArray(body) ? Object.keys(body).sort() : [],
		kind: bodyKind(body),
		body,
	};
}

async function sampleJSON(
	baseUrl: string,
	path: string,
	token?: string,
	options: { method?: string; body?: unknown; rawBody?: string } = {},
): Promise<JsonSample> {
	const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
	if (options.body !== undefined || options.rawBody !== undefined) {
		headers["Content-Type"] = "application/json";
	}
	const res = await fetch(`${baseUrl}${path}`, {
		method: options.method,
		headers,
		body:
			options.rawBody !== undefined
				? options.rawBody
				: options.body !== undefined
					? JSON.stringify(options.body)
					: undefined,
	});
	return jsonSampleFromResponse(res);
}

async function sampleRedirect(
	baseUrl: string,
	path: string,
	token?: string,
	options: { method?: string; body?: unknown } = {},
): Promise<JsonSample> {
	const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
	if (options.body !== undefined) headers["Content-Type"] = "application/json";
	const res = await fetch(`${baseUrl}${path}`, {
		method: options.method,
		headers,
		body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
		redirect: "manual",
	});
	const location = res.headers.get("location") ?? "";
	return {
		status: res.status,
		contentType: res.headers.get("content-type") ?? "",
		keys: ["location"],
		kind: "object",
		body: { location },
	};
}

function buildClineCallbackUrl(
	kind = "callback",
	callbackBase = "http://localhost:19876/auth/callback",
): string {
	const payload = {
		accessToken: `cline-${kind}-access-token`,
		refreshToken: `cline-${kind}-refresh-token`,
		email: kind === "callback" ? "cline-alice@example.com" : `cline-${kind}@example.com`,
		name:
			kind === "callback" ? "Cline Alice" : kind === "browser" ? "Cline Browser" : `Cline ${kind}`,
		expiresAt: kind.includes("expired") ? "2000-01-01T00:00:00.000Z" : "2099-01-01T00:00:00.000Z",
	};
	const code = Buffer.from(JSON.stringify(payload), "utf-8").toString("base64");
	const url = new URL(callbackBase);
	url.searchParams.set("code", code);
	return url.toString();
}

async function sampleAbsoluteURL(url: string): Promise<JsonSample> {
	const res = await fetch(url);
	return jsonSampleFromResponse(res);
}

async function waitClineBrowserAuthSideEffects(
	baseUrl: string,
	token: string,
): Promise<{ status: JsonSample; settings: JsonSample }> {
	let status = await sampleJSON(baseUrl, "/api/cline/status", token);
	let settings = await sampleJSON(baseUrl, "/api/settings", token);
	for (let attempt = 0; attempt < 50; attempt++) {
		const provider = findSettingsProvider(
			{ json: { clineSettings: settings }, sse: {}, ws: {} },
			"clineSettings",
			"clineProviders",
			"cline-cache-fixture",
		);
		const accessToken = provider?.accessToken;
		const enabledModels = provider?.enabledModels;
		if (
			recordBody(status.body).authenticated === true &&
			typeof accessToken === "string" &&
			accessToken.startsWith("********") &&
			Array.isArray(enabledModels) &&
			enabledModels.length > 0
		) {
			return { status, settings };
		}
		await new Promise((resolve) => setTimeout(resolve, 100));
		status = await sampleJSON(baseUrl, "/api/cline/status", token);
		settings = await sampleJSON(baseUrl, "/api/settings", token);
	}
	return { status, settings };
}

async function sampleNugOAuthFlow(
	baseUrl: string,
	userToken: string,
	adminToken: string,
): Promise<Record<string, JsonSample>> {
	const userNugOAuthStartValid = await sampleJSON(
		baseUrl,
		"/api/nug/providers/nug-oauth-fixture/oauth/start",
		userToken,
	);
	const state = recordBody(userNugOAuthStartValid.body).state;
	const callbackPath =
		typeof state === "string"
			? `/api/nug/oauth/callback?code=oauth-code&state=${encodeURIComponent(state)}`
			: "/api/nug/oauth/callback?code=oauth-code&state=missing";
	const nugOAuthCallbackValid = await sampleRedirect(baseUrl, callbackPath);
	const settingsAfterNugOAuth = await sampleJSON(baseUrl, "/api/settings", adminToken);
	return { userNugOAuthStartValid, nugOAuthCallbackValid, settingsAfterNugOAuth };
}

async function sampleMultipart(
	baseUrl: string,
	path: string,
	token: string,
	configure: (formData: FormData) => void,
): Promise<JsonSample> {
	const formData = new FormData();
	configure(formData);
	const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
	const res = await fetch(`${baseUrl}${path}`, {
		method: "POST",
		headers,
		body: formData,
	});
	return jsonSampleFromResponse(res);
}

async function seedSettingsSecrets(
	baseUrl: string,
	token: string,
	openAIBaseUrl: string,
	anthropicBaseUrl: string,
	nkpBaseUrl: string,
	nugBaseUrl: string,
): Promise<void> {
	const res = await sampleJSON(baseUrl, "/api/settings", token, {
		method: "PATCH",
		body: seededSettingsProviders(false, openAIBaseUrl, anthropicBaseUrl, nkpBaseUrl, nugBaseUrl),
	});
	if (res.status !== 200) {
		throw new Error(`seed settings secrets failed: ${res.status} ${JSON.stringify(res.body)}`);
	}
}

async function configureUpdateServer(
	baseUrl: string,
	token: string,
	serverUrl: string,
	product = "narrafork",
): Promise<void> {
	const res = await sampleJSON(baseUrl, "/api/settings", token, {
		method: "PATCH",
		body: {
			update: { serverUrl, product, channel: "stable" },
		},
	});
	if (res.status !== 200) {
		throw new Error(`configure update server failed: ${res.status} ${JSON.stringify(res.body)}`);
	}
}

async function readSSEText(
	res: Response,
	contentType: string,
	stopOnEvents: string[],
): Promise<string> {
	if (!contentType.includes("text/event-stream") || !res.body) return await res.text();
	const reader = res.body.getReader();
	const decoder = new TextDecoder();
	let text = "";
	const deadline = Date.now() + 30_000;
	try {
		while (Date.now() < deadline) {
			const timeoutMs = Math.max(1, deadline - Date.now());
			const result = (await Promise.race([
				reader.read(),
				new Promise((resolve) =>
					setTimeout(() => resolve({ done: true, timeout: true }), timeoutMs),
				),
			])) as ReadableStreamReadResult<Uint8Array> & { timeout?: boolean };
			if (result.timeout) break;
			if (result.value) text += decoder.decode(result.value, { stream: !result.done });
			const events = [...text.matchAll(/^event:\s*(.+)$/gm)].map((m) => m[1].trim());
			if (stopOnEvents.some((event) => events.includes(event))) break;
			if (result.done) break;
		}
	} finally {
		await reader.cancel().catch(() => undefined);
	}
	return text;
}

async function sampleSSE(
	baseUrl: string,
	path: string,
	token: string,
	options: { method?: string; body?: unknown; stopOnEvents?: string[] } = {
		method: "POST",
		body: {},
	},
): Promise<SseSample> {
	const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
	if (options.body !== undefined) headers["Content-Type"] = "application/json";
	const res = await fetch(`${baseUrl}${path}`, {
		method: options.method,
		headers,
		body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
	});
	const contentType = res.headers.get("content-type") ?? "";
	const text = await readSSEText(res, contentType, options.stopOnEvents ?? ["complete", "error"]);
	const events = [...text.matchAll(/^event:\s*(.+)$/gm)].map((m) => m[1].trim());
	let terminal = "none";
	for (const line of text.split(/\r?\n/)) {
		if (!line.startsWith("data:")) continue;
		try {
			const parsed = JSON.parse(line.slice(5).trim());
			if (parsed && typeof parsed === "object" && "success" in parsed) {
				terminal = String((parsed as { success: unknown }).success);
			}
		} catch {
			// Non-JSON SSE data is still reported through bodyPreview.
		}
	}
	return { status: res.status, contentType, events, terminal, bodyPreview: text.slice(0, 500) };
}

async function sampleWS(baseUrl: string, path: string, token?: string): Promise<WsSample> {
	const query = token ? `?token=${encodeURIComponent(token)}` : "";
	const wsUrl = `${baseUrl.replace(/^http/, "ws")}${path}${query}`;
	return await new Promise((resolveSample) => {
		let done = false;
		const finish = (sample: WsSample) => {
			if (done) return;
			done = true;
			try {
				ws.close();
			} catch {
				// ignore
			}
			resolveSample(sample);
		};
		const ws = new WebSocket(wsUrl);
		const timer = setTimeout(() => finish({ opened: ws.readyState === WebSocket.OPEN }), 800);
		ws.onopen = () => {
			if (path.includes("narrator")) {
				ws.send(JSON.stringify({ type: "subscribe", narratorIds: [] }));
			}
		};
		ws.onmessage = (event) => {
			clearTimeout(timer);
			try {
				const parsed = JSON.parse(String(event.data));
				finish({ opened: true, firstMessageType: String(parsed.type ?? "") });
			} catch {
				finish({ opened: true, firstMessageType: "non-json" });
			}
		};
		ws.onerror = () => {
			clearTimeout(timer);
			finish({ opened: false, error: "websocket error" });
		};
	});
}

async function collect(
	baseUrl: string,
	token: string,
	userToken: string,
	home: string,
	updateServerUrl: string,
	openAIBaseUrl: string,
	anthropicBaseUrl: string,
	nkpBaseUrl: string,
	nugBaseUrl: string,
): Promise<BackendSample> {
	const port = new URL(baseUrl).port;
	const favoritePath = `/contract/favorite-${port}`;
	const fsFixtureDir = join(home, "fs-contract");
	const fsMissingDir = join(home, "missing-fs-contract");
	const fsMkdirName = `created-${port}`;
	const gitPanelBase = `/api/chapters/${gitPanelFixtureChapterId}/git`;
	resetGitPanelFixture(home, { dirty: false });
	const gitAICommitMessageEmpty = await sampleJSON(
		baseUrl,
		`${gitPanelBase}/ai-commit-message`,
		token,
		{ method: "POST", body: {} },
	);
	resetGitPanelFixture(home, { dirty: true });
	const gitStatusDirty = await sampleJSON(baseUrl, `${gitPanelBase}/status`, token);
	const gitDiffTracked = await sampleJSON(
		baseUrl,
		`${gitPanelBase}/diff?file=${encodeURIComponent("tracked.txt")}`,
		token,
	);
	const gitDiffUntracked = await sampleJSON(
		baseUrl,
		`${gitPanelBase}/diff?file=${encodeURIComponent("notes.txt")}`,
		token,
	);
	const gitLog = await sampleJSON(baseUrl, `${gitPanelBase}/log?limit=5`, token);
	const gitStageAll = await sampleJSON(baseUrl, `${gitPanelBase}/stage`, token, {
		method: "POST",
		body: { all: true },
	});
	const gitUnstageAll = await sampleJSON(baseUrl, `${gitPanelBase}/unstage`, token, {
		method: "POST",
		body: { all: true },
	});
	resetGitPanelFixture(home, { dirty: true });
	const gitDiscardTracked = await sampleJSON(baseUrl, `${gitPanelBase}/discard`, token, {
		method: "POST",
		body: { files: ["tracked.txt"] },
	});
	resetGitPanelFixture(home, { dirty: true });
	const gitResetHard = await sampleJSON(baseUrl, `${gitPanelBase}/reset`, token, {
		method: "POST",
		body: { target: gitPanelFixtureBaseTag, mode: "hard" },
	});
	resetGitPanelFixture(home, { dirty: true });
	const gitStashListEmpty = await sampleJSON(baseUrl, `${gitPanelBase}/stash/list`, token);
	const gitStashPush = await sampleJSON(baseUrl, `${gitPanelBase}/stash`, token, {
		method: "POST",
		body: { action: "push", message: "contract stash" },
	});
	const gitStashListAfterPush = await sampleJSON(baseUrl, `${gitPanelBase}/stash/list`, token);
	const gitStashDrop = await sampleJSON(baseUrl, `${gitPanelBase}/stash`, token, {
		method: "POST",
		body: { action: "drop", index: 0 },
	});
	resetGitPanelFixture(home, { dirty: true });
	await sampleJSON(baseUrl, `${gitPanelBase}/stash`, token, {
		method: "POST",
		body: { action: "push", message: "contract stash pop" },
	});
	const gitStashPop = await sampleJSON(baseUrl, `${gitPanelBase}/stash`, token, {
		method: "POST",
		body: { action: "pop" },
	});
	resetGitPanelFixture(home, { dirty: true });
	const gitStageAllBeforeCommit = await sampleJSON(baseUrl, `${gitPanelBase}/stage`, token, {
		method: "POST",
		body: { all: true },
	});
	const gitCommit = await sampleJSON(baseUrl, `${gitPanelBase}/commit`, token, {
		method: "POST",
		body: { message: "test: contract git panel" },
	});
	const notificationSoundUploadValid = await sampleMultipart(
		baseUrl,
		"/api/notification-sounds",
		token,
		(formData) => {
			formData.append("file", new Blob(["ID3"], { type: "audio/mpeg" }), "tone.mp3");
		},
	);
	const notificationSoundId = recordBody(notificationSoundUploadValid.body).id;
	const notificationSoundIdPath =
		typeof notificationSoundId === "string"
			? `/api/notification-sounds/${encodeURIComponent(notificationSoundId)}`
			: "/api/notification-sounds/missing";
	const workspaceInitialTree = JSON.stringify({ type: "leaf", narratorId: "n_search_fixture" });
	const workspaceUpdatedTree = JSON.stringify({
		type: "leaf",
		narratorId: "n_search_fixture",
		presentation: "updated",
	});
	const projectRoutineBase = "/api/routines/project/p_search_fixture";
	const projectRoutinesInitial = await sampleJSON(baseUrl, projectRoutineBase, token);
	const projectRoutineInvalidAction = await sampleJSON(
		baseUrl,
		`${projectRoutineBase}/terminal/toggle`,
		token,
		{ method: "POST", body: { action: "pause" } },
	);
	const projectRoutineMissingRoutine = await sampleJSON(
		baseUrl,
		`${projectRoutineBase}/missing/toggle`,
		token,
		{ method: "POST", body: { action: "enable" } },
	);
	const projectRoutineMissingProject = await sampleJSON(
		baseUrl,
		"/api/routines/project/missing/terminal/toggle",
		token,
		{ method: "POST", body: { action: "enable" } },
	);
	const projectRoutineEnableTerminal = await sampleJSON(
		baseUrl,
		`${projectRoutineBase}/terminal/toggle`,
		token,
		{ method: "POST", body: { action: "enable" } },
	);
	const projectRoutinesAfterEnable = await sampleJSON(baseUrl, projectRoutineBase, token);
	const projectRoutineDisableTerminal = await sampleJSON(
		baseUrl,
		`${projectRoutineBase}/terminal/toggle`,
		token,
		{ method: "POST", body: { action: "disable" } },
	);
	const projectRoutinesAfterDisable = await sampleJSON(baseUrl, projectRoutineBase, token);
	const projectRoutineResetTerminal = await sampleJSON(
		baseUrl,
		`${projectRoutineBase}/terminal/toggle`,
		token,
		{ method: "POST", body: { action: "reset" } },
	);
	const projectRoutinesAfterReset = await sampleJSON(baseUrl, projectRoutineBase, token);
	const globalSkillCreateValid = await sampleJSON(baseUrl, "/api/skills/global", token, {
		method: "POST",
		body: {
			name: "Contract Skill",
			description: "Helps contract parity",
			content: "Initial global skill content.",
		},
	});
	const globalSkillCreatedPath = "/api/skills/global/Contract%20Skill";
	const globalSkillDetailCreated = await sampleJSON(baseUrl, globalSkillCreatedPath, token);
	const globalSkillCreateDuplicate = await sampleJSON(baseUrl, "/api/skills/global", token, {
		method: "POST",
		body: {
			name: "Contract Skill",
			description: "Duplicate should fail",
			content: "Duplicate global skill content.",
		},
	});
	const globalSkillToggleInvalidMissingEnabled = await sampleJSON(
		baseUrl,
		`${globalSkillCreatedPath}/toggle`,
		token,
		{ method: "POST", body: {} },
	);
	const globalSkillToggleDisable = await sampleJSON(
		baseUrl,
		`${globalSkillCreatedPath}/toggle`,
		token,
		{ method: "POST", body: { enabled: false } },
	);
	const globalSkillToggleEnable = await sampleJSON(
		baseUrl,
		`${globalSkillCreatedPath}/toggle`,
		token,
		{ method: "POST", body: { enabled: true } },
	);
	const globalSkillUpdateValid = await sampleJSON(baseUrl, globalSkillCreatedPath, token, {
		method: "PUT",
		body: {
			name: "Contract Skill Updated",
			description: "Updated contract parity skill",
			content: "Updated global skill content.",
		},
	});
	const globalSkillOldDetailAfterRename = await sampleJSON(baseUrl, globalSkillCreatedPath, token);
	const globalSkillUpdatedPath = "/api/skills/global/Contract%20Skill%20Updated";
	const globalSkillDetailUpdated = await sampleJSON(baseUrl, globalSkillUpdatedPath, token);
	const globalSkillListAfterUpdate = await sampleJSON(baseUrl, "/api/skills/global", token);
	const globalSkillDeleteValid = await sampleJSON(baseUrl, globalSkillUpdatedPath, token, {
		method: "DELETE",
		body: {},
	});
	const globalSkillDetailAfterDelete = await sampleJSON(baseUrl, globalSkillUpdatedPath, token);
	const globalSkillCreateInvalidMissingDescription = await sampleJSON(
		baseUrl,
		"/api/skills/global",
		token,
		{
			method: "POST",
			body: { name: "Missing Description", content: "No description." },
		},
	);
	const globalSkillUpdateMissing = await sampleJSON(baseUrl, "/api/skills/global/missing", token, {
		method: "PUT",
		body: {
			name: "Missing Updated",
			description: "Missing update should fail",
			content: "Missing update content.",
		},
	});
	const globalSkillDeleteMissing = await sampleJSON(baseUrl, "/api/skills/global/missing", token, {
		method: "DELETE",
		body: {},
	});
	const globalSkillToggleMissing = await sampleJSON(
		baseUrl,
		"/api/skills/global/missing/toggle",
		token,
		{ method: "POST", body: { enabled: true } },
	);
	const customSubagentCreateValid = await sampleJSON(baseUrl, "/api/custom-subagents", token, {
		method: "POST",
		body: {
			name: "Contract Helper",
			description: "Helps contract parity",
			toolAccess: "custom",
			customTools: ["Read", "Grep"],
			defaultModel: "claude-sonnet-4.5",
			prompt: "Initial custom subagent prompt.",
		},
	});
	const customSubagentCreatedPath = "/api/custom-subagents/Contract%20Helper";
	const customSubagentDetailCreated = await sampleJSON(baseUrl, customSubagentCreatedPath, token);
	const customSubagentUpdateValid = await sampleJSON(baseUrl, customSubagentCreatedPath, token, {
		method: "PUT",
		body: {
			name: "Contract Audit",
			description: "Audits contract parity",
			toolAccess: "general",
			customTools: ["Read"],
			defaultModel: "claude-haiku-4.5",
			prompt: "Updated custom subagent prompt.",
		},
	});
	const customSubagentOldDetailAfterRename = await sampleJSON(
		baseUrl,
		customSubagentCreatedPath,
		token,
	);
	const customSubagentUpdatedPath = "/api/custom-subagents/Contract%20Audit";
	const customSubagentDetailUpdated = await sampleJSON(baseUrl, customSubagentUpdatedPath, token);
	const customSubagentListAfterUpdate = await sampleJSON(baseUrl, "/api/custom-subagents", token);
	const customSubagentDeleteValid = await sampleJSON(baseUrl, customSubagentUpdatedPath, token, {
		method: "DELETE",
		body: {},
	});
	const customSubagentDetailAfterDelete = await sampleJSON(
		baseUrl,
		customSubagentUpdatedPath,
		token,
	);
	const workspaceCreateValid = await sampleJSON(baseUrl, "/api/workspaces", token, {
		method: "POST",
		body: { title: "Contract Workspace", tree: workspaceInitialTree },
	});
	const workspaceId = recordBody(workspaceCreateValid.body).id;
	const workspacePath =
		typeof workspaceId === "string"
			? `/api/workspaces/${encodeURIComponent(workspaceId)}`
			: "/api/workspaces/missing";
	const workspaceDetailCreated = await sampleJSON(baseUrl, workspacePath, token);
	const workspaceUpdateValid = await sampleJSON(baseUrl, workspacePath, token, {
		method: "PATCH",
		body: { title: "Contract Workspace Updated", tree: workspaceUpdatedTree },
	});
	const workspaceDetailUpdated = await sampleJSON(baseUrl, workspacePath, token);
	const workspaceListAfterUpdate = await sampleJSON(baseUrl, "/api/workspaces", token);
	const workspaceDeleteValid = await sampleJSON(baseUrl, workspacePath, token, {
		method: "DELETE",
		body: {},
	});
	const workspaceDetailAfterDelete = await sampleJSON(baseUrl, workspacePath, token);
	const hookCreateValid = await sampleJSON(baseUrl, "/api/hooks", token, {
		method: "POST",
		body: {
			event: "PreToolUse",
			matcher: "Read",
			type: "command",
			command: "true",
			timeout: 5,
			enabled: true,
			sortOrder: 2,
		},
	});
	const hookId = recordBody(hookCreateValid.body).id;
	const hookPath =
		typeof hookId === "string" ? `/api/hooks/${encodeURIComponent(hookId)}` : "/api/hooks/missing";
	const hookDetailCreated = await sampleJSON(baseUrl, hookPath, token);
	const hookUpdateValid = await sampleJSON(baseUrl, hookPath, token, {
		method: "PUT",
		body: {
			matcher: "",
			type: "http",
			url: "https://example.com/hook",
			headers: { "X-Test": "yes" },
			timeout: 10,
			enabled: false,
		},
	});
	const hookDetailUpdated = await sampleJSON(baseUrl, hookPath, token);
	const hookListAfterUpdate = await sampleJSON(baseUrl, "/api/hooks", token);
	const hookAllAfterUpdate = await sampleJSON(baseUrl, "/api/hooks/all", token);
	const hookUpdateClearActiveURLInvalid = await sampleJSON(baseUrl, hookPath, token, {
		method: "PUT",
		body: { url: null },
	});
	const hookUpdateMissingInvalidBody = await sampleJSON(baseUrl, "/api/hooks/missing", token, {
		method: "PUT",
		body: { type: "bad" },
	});
	const hookDeleteValid = await sampleJSON(baseUrl, hookPath, token, {
		method: "DELETE",
		body: {},
	});
	const hookDetailAfterDelete = await sampleJSON(baseUrl, hookPath, token);
	const mcpHelperPath = join(home, "mcp-contract-helper.mjs");
	const mcpServers = await sampleJSON(baseUrl, "/api/mcp/servers", token);
	const mcpTools = await sampleJSON(baseUrl, "/api/mcp/tools", token);
	const mcpCreateInvalidArgs = await sampleJSON(baseUrl, "/api/mcp/servers", token, {
		method: "POST",
		body: { name: "bad-args", args: [1], enabled: false },
	});
	const mcpCreateInvalidToolPermissions = await sampleJSON(baseUrl, "/api/mcp/servers", token, {
		method: "POST",
		body: { name: "bad-permissions", toolPermissions: "not-an-array", enabled: false },
	});
	const mcpTestInvalidBody = await sampleJSON(baseUrl, "/api/mcp/servers/test", token, {
		method: "POST",
		body: { name: "bad-test", enabled: "yes" },
	});
	const mcpCreateDisabled = await sampleJSON(baseUrl, "/api/mcp/servers", token, {
		method: "POST",
		body: {
			name: "contract-tools",
			transport: "stdio",
			command: "node",
			args: ["server.js"],
			enabled: false,
			defaultBehavior: "allow",
			toolPermissions: [{ toolName: "read_file", behavior: "ask" }],
		},
	});
	const mcpTestStdioValid = await sampleJSON(baseUrl, "/api/mcp/servers/test", token, {
		method: "POST",
		body: {
			name: "contract-mcp-test",
			transport: "stdio",
			command: process.execPath,
			args: [mcpHelperPath],
			enabled: false,
		},
	});
	const mcpCreateStdioEnabled = await sampleJSON(baseUrl, "/api/mcp/servers", token, {
		method: "POST",
		body: {
			name: "contract-mcp-live",
			transport: "stdio",
			command: process.execPath,
			args: [mcpHelperPath],
			enabled: true,
			defaultBehavior: "allow",
			toolPermissions: [{ toolName: "read_file", behavior: "readOnly", enabled: true }],
		},
	});
	const mcpStdioId = recordBody(mcpCreateStdioEnabled.body).id;
	const mcpStdioPath =
		typeof mcpStdioId === "string"
			? `/api/mcp/servers/${encodeURIComponent(mcpStdioId)}`
			: "/api/mcp/servers/missing";
	const mcpServersAfterStdioCreate = await sampleJSON(baseUrl, "/api/mcp/servers", token);
	const mcpToolsAfterStdioCreate = await sampleJSON(baseUrl, "/api/mcp/tools", token);
	const mcpDeleteStdioValid = await sampleJSON(baseUrl, mcpStdioPath, token, {
		method: "DELETE",
		body: {},
	});
	await configureUpdateServer(baseUrl, token, updateServerUrl);
	let updateCheckTrusted: JsonSample;
	let userUpdateCheckTrusted: JsonSample;
	let updateDownloadTrustedError: SseSample;
	try {
		updateCheckTrusted = await sampleJSON(baseUrl, "/api/update/check", token);
		userUpdateCheckTrusted = await sampleJSON(baseUrl, "/api/update/check", userToken);
		await configureUpdateServer(baseUrl, token, updateServerUrl, "narrafork-download-error");
		updateDownloadTrustedError = await sampleSSE(baseUrl, "/api/update/download", token);
	} finally {
		await configureUpdateServer(baseUrl, token, updateServerUrl, "narrafork-no-update");
	}
	const clineStatus = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineBalance = await sampleJSON(baseUrl, "/api/cline/balance", token);
	const clineRecommendedModelsRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/recommended-models?refresh=1",
		token,
	);
	const clineRecommendedModelsCached = await sampleJSON(
		baseUrl,
		"/api/cline/recommended-models",
		token,
	);
	const clineModels = await sampleJSON(baseUrl, "/api/cline/models", token);
	const clineMissingProviderModels = await sampleJSON(
		baseUrl,
		"/api/cline/providers/missing/models",
		token,
	);
	const clineMissingProviderRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/providers/missing/models/refresh",
		token,
		{ method: "POST", body: {} },
	);
	const clinePoolSearch = await sampleJSON(
		baseUrl,
		"/api/cline/pool/search?q=fixture&limit=5",
		token,
	);
	const clinePoolCount = await sampleJSON(baseUrl, "/api/cline/pool/count", token);
	const clineEnabledModelsInvalid = await sampleJSON(baseUrl, "/api/cline/enabled-models", token, {
		method: "POST",
		body: {},
	});
	const clineEnabledModelsValid = await sampleJSON(baseUrl, "/api/cline/enabled-models", token, {
		method: "POST",
		body: { models: ["cline/free-fixture"] },
	});
	const clineStatusAfterEnabledModels = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineSettingsAfterEnabledModels = await sampleJSON(baseUrl, "/api/settings", token);
	const clineEnabledModelsResetBeforeAuth = await sampleJSON(
		baseUrl,
		"/api/cline/enabled-models",
		token,
		{
			method: "POST",
			body: { models: [] },
		},
	);
	const clineAuthCancel = await sampleJSON(baseUrl, "/api/cline/auth/cancel", token, {
		method: "POST",
		body: {},
	});
	const clineAuthCallbackInvalid = await sampleJSON(baseUrl, "/api/cline/auth/callback", token, {
		method: "POST",
		body: {},
	});
	const clineAuthCallbackValid = await sampleJSON(baseUrl, "/api/cline/auth/callback", token, {
		method: "POST",
		body: { callbackUrl: buildClineCallbackUrl() },
	});
	const clineStatusAfterAuthCallback = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineSettingsAfterAuthCallback = await sampleJSON(baseUrl, "/api/settings", token);
	const clineUserInfoRefreshValid = await sampleJSON(
		baseUrl,
		"/api/cline/user-info/refresh",
		token,
		{ method: "POST", body: {} },
	);
	const clineBalanceAfterUserInfoRefresh = await sampleJSON(baseUrl, "/api/cline/balance", token);
	const clineStatusAfterUserInfoRefresh = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineAuthLogout = await sampleJSON(baseUrl, "/api/cline/auth/logout", token, {
		method: "POST",
		body: {},
	});
	const clineBaseUrl = `${new URL(openAIBaseUrl).origin}/cline`;
	const clineEnabledModelsResetBeforeBrowserAuth = await sampleJSON(
		baseUrl,
		"/api/cline/enabled-models",
		token,
		{
			method: "POST",
			body: { models: [] },
		},
	);
	const clineBrowserAuthStart = await sampleJSON(baseUrl, "/api/cline/auth/browser", token, {
		method: "POST",
		body: { apiBaseUrl: clineBaseUrl },
	});
	const clineAuthorizeUrl = recordBody(clineBrowserAuthStart.body).authorizeUrl;
	const clineCallbackBase =
		typeof clineAuthorizeUrl === "string"
			? new URL(clineAuthorizeUrl).searchParams.get("callback_url")
			: null;
	const clineBrowserCallback = await sampleAbsoluteURL(
		buildClineCallbackUrl("browser", clineCallbackBase ?? "http://localhost:19876/auth/callback"),
	);
	const { status: clineStatusAfterBrowserCallback, settings: clineSettingsAfterBrowserCallback } =
		await waitClineBrowserAuthSideEffects(baseUrl, token);
	const clineBrowserAuthLogout = await sampleJSON(baseUrl, "/api/cline/auth/logout", token, {
		method: "POST",
		body: {},
	});
	const clineAuthCallbackExpired = await sampleJSON(baseUrl, "/api/cline/auth/callback", token, {
		method: "POST",
		body: { callbackUrl: buildClineCallbackUrl("expired") },
	});
	const clineBalanceAfterExpiredAuthRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/balance",
		token,
	);
	const clineStatusAfterExpiredAuthRefresh = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineAuthLogoutAfterExpiredRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/auth/logout",
		token,
		{
			method: "POST",
			body: {},
		},
	);
	const clineAuthCallbackInvalidRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/auth/callback",
		token,
		{
			method: "POST",
			body: { callbackUrl: buildClineCallbackUrl("invalid-refresh-expired") },
		},
	);
	const clineBalanceAfterInvalidRefresh = await sampleJSON(baseUrl, "/api/cline/balance", token);
	const clineStatusAfterInvalidRefresh = await sampleJSON(baseUrl, "/api/cline/status", token);
	const clineProviderRefreshValid = await sampleJSON(
		baseUrl,
		"/api/cline/providers/cline-cache-fixture/models/refresh",
		token,
		{ method: "POST", body: {} },
	);
	const clineProviderModelsAfterRefresh = await sampleJSON(
		baseUrl,
		"/api/cline/providers/cline-cache-fixture/models",
		token,
	);
	const clineModelsAfterProviderRefresh = await sampleJSON(baseUrl, "/api/cline/models", token);
	const dependencyEdgeCreateLifecycle = await sampleJSON(baseUrl, "/api/chapter-edges", token, {
		method: "POST",
		body: {
			sourceId: "c_dep_lifecycle_source",
			targetId: "c_dep_lifecycle_target",
			type: "dependency",
			metadata: { description: "Contract lifecycle dependency" },
		},
	});
	const dependencyEdgeCreatedId =
		typeof recordBody(dependencyEdgeCreateLifecycle.body).id === "string"
			? (recordBody(dependencyEdgeCreateLifecycle.body).id as string)
			: "missing-dependency-edge-id";
	const chapterEdgesAfterLifecycleCreate = await sampleJSON(
		baseUrl,
		"/api/chapter-edges?projectId=p_search_fixture&type=dependency",
		token,
	);
	const projectGraphAfterDependencyCreate = await sampleJSON(
		baseUrl,
		"/api/projects/p_search_fixture/graph",
		token,
	);
	const dependencyEdgeDeleteLifecycle = await sampleJSON(
		baseUrl,
		`/api/chapter-edges/${dependencyEdgeCreatedId}`,
		token,
		{ method: "DELETE" },
	);
	const chapterEdgesAfterLifecycleDelete = await sampleJSON(
		baseUrl,
		"/api/chapter-edges?projectId=p_search_fixture&type=dependency",
		token,
	);
	const projectGraphAfterDependencyDelete = await sampleJSON(
		baseUrl,
		"/api/projects/p_search_fixture/graph",
		token,
	);
	return {
		json: {
			health: await sampleJSON(baseUrl, "/api/health"),
			authStatus: await sampleJSON(baseUrl, "/api/auth/status"),
			unauthProjects: await sampleJSON(baseUrl, "/api/projects"),
			unauthSettings: await sampleJSON(baseUrl, "/api/settings"),
			unauthStorageCached: await sampleJSON(baseUrl, "/api/storage/cached"),
			unauthRuntimeScan: await sampleJSON(baseUrl, "/api/runtime/scan"),
			unauthBenchmarkSuites: await sampleJSON(baseUrl, "/api/benchmarks/suites"),
			projects: await sampleJSON(baseUrl, "/api/projects", token),
			settings: await sampleJSON(baseUrl, "/api/settings", token),
			userSettings: await sampleJSON(baseUrl, "/api/settings", userToken),
			gitAICommitMessageEmpty,
			gitStatusDirty,
			gitDiffTracked,
			gitDiffUntracked,
			gitLog,
			gitStageAll,
			gitUnstageAll,
			gitDiscardTracked,
			gitResetHard,
			gitStashListEmpty,
			gitStashPush,
			gitStashListAfterPush,
			gitStashDrop,
			gitStashPop,
			gitStageAllBeforeCommit,
			gitCommit,
			settingsMaskedProviderPatch: await sampleJSON(baseUrl, "/api/settings", token, {
				method: "PATCH",
				body: seededSettingsProviders(
					true,
					openAIBaseUrl,
					anthropicBaseUrl,
					nkpBaseUrl,
					nugBaseUrl,
				),
			}),
			settingsContextThresholds: await sampleJSON(
				baseUrl,
				"/api/settings/context-thresholds?model=unknown&provider=openai",
				token,
			),
			settingsTestModelMissing: await sampleJSON(baseUrl, "/api/settings/test-model", token, {
				method: "POST",
				body: { model: "missing:model", prompt: "hello" },
			}),
			settingsRetryRuleInvalid: await sampleJSON(baseUrl, "/api/settings/retry-rules", token, {
				method: "POST",
				body: {},
			}),
			settingsRetryRuleValid: await sampleJSON(baseUrl, "/api/settings/retry-rules", token, {
				method: "POST",
				body: { domain: "retry.example.test", statusCode: 503, note: "contract" },
			}),
			userPreferences: await sampleJSON(baseUrl, "/api/user-preferences", token),
			recentTabUpsert: await sampleJSON(baseUrl, "/api/user-preferences/recent-tabs", token, {
				method: "PUT",
				body: {
					type: "project",
					id: "p_search_fixture",
					title: "Search Fixture Project",
					lastVisitedAt: 1_700_000_000_000,
				},
			}),
			recentTabInvalidMissingLastVisitedAt: await sampleJSON(
				baseUrl,
				"/api/user-preferences/recent-tabs",
				token,
				{
					method: "PUT",
					body: {
						type: "project",
						id: "p_search_fixture",
						title: "Search Fixture Project",
					},
				},
			),
			favorites: await sampleJSON(baseUrl, "/api/favorites", token),
			favoriteCreateValid: await sampleJSON(baseUrl, "/api/favorites", token, {
				method: "POST",
				body: { path: favoritePath, label: "Favorite One" },
			}),
			favoriteCreateDuplicate: await sampleJSON(baseUrl, "/api/favorites", token, {
				method: "POST",
				body: { path: favoritePath, label: "Duplicate" },
			}),
			favoriteCreateInvalidMissingPath: await sampleJSON(baseUrl, "/api/favorites", token, {
				method: "POST",
				body: {},
			}),
			favoriteCreateInvalidNullLabel: await sampleJSON(baseUrl, "/api/favorites", token, {
				method: "POST",
				body: { path: "/contract/null-label", label: null },
			}),
			favoriteUpdateMissing: await sampleJSON(baseUrl, "/api/favorites/missing", token, {
				method: "PATCH",
				body: { path: "/contract/missing" },
			}),
			favoriteDeleteMissing: await sampleJSON(baseUrl, "/api/favorites/missing", token, {
				method: "DELETE",
				body: {},
			}),
			favoriteReorderInvalidMissing: await sampleJSON(baseUrl, "/api/favorites/reorder", token, {
				method: "PUT",
				body: {},
			}),
			favoriteReorderInvalidEmpty: await sampleJSON(baseUrl, "/api/favorites/reorder", token, {
				method: "PUT",
				body: { ids: [] },
			}),
			fsShortcuts: await sampleJSON(baseUrl, "/api/fs/shortcuts", token),
			fsBrowseDefault: await sampleJSON(baseUrl, "/api/fs/browse", token),
			fsBrowseFixture: await sampleJSON(
				baseUrl,
				`/api/fs/browse?path=${encodeURIComponent(fsFixtureDir)}`,
				token,
			),
			fsBrowseFixtureShowHidden: await sampleJSON(
				baseUrl,
				`/api/fs/browse?path=${encodeURIComponent(fsFixtureDir)}&showHidden=1`,
				token,
			),
			fsBrowseMissing: await sampleJSON(
				baseUrl,
				`/api/fs/browse?path=${encodeURIComponent(fsMissingDir)}`,
				token,
			),
			fsMkdirInvalidMissing: await sampleJSON(baseUrl, "/api/fs/mkdir", token, {
				method: "POST",
				body: {},
			}),
			fsMkdirInvalidPathOnly: await sampleJSON(baseUrl, "/api/fs/mkdir", token, {
				method: "POST",
				body: { path: join(fsFixtureDir, "path-only") },
			}),
			fsMkdirInvalidSeparator: await sampleJSON(baseUrl, "/api/fs/mkdir", token, {
				method: "POST",
				body: { parent: fsFixtureDir, name: "nested/child" },
			}),
			fsMkdirValid: await sampleJSON(baseUrl, "/api/fs/mkdir", token, {
				method: "POST",
				body: { parent: fsFixtureDir, name: fsMkdirName },
			}),
			fsRevealUnsupportedOrMissing: await sampleJSON(baseUrl, "/api/fs/reveal", token, {
				method: "POST",
				body: { path: process.platform === "linux" ? fsFixtureDir : fsMissingDir },
			}),
			uploadNarratorImage: await sampleJSON(
				baseUrl,
				"/api/uploads/n_upload_fixture/img1234",
				token,
			),
			uploadAvatarImage: await sampleJSON(
				baseUrl,
				"/api/uploads/avatars/u_upload_fixture/avatar1234",
				token,
			),
			uploadMissing: await sampleJSON(baseUrl, "/api/uploads/n_upload_fixture/missing", token),
			shareMissing: await sampleJSON(baseUrl, "/api/shares/missing"),
			sharePreviewMissing: await sampleJSON(baseUrl, "/api/shares/missing/preview"),
			notificationDingtalkInvalidMissing: await sampleJSON(
				baseUrl,
				"/api/notifications/test-dingtalk",
				token,
				{ method: "POST", body: {} },
			),
			notificationFeishuInvalidMissing: await sampleJSON(
				baseUrl,
				"/api/notifications/test-feishu",
				token,
				{ method: "POST", body: {} },
			),
			notificationSoundUploadMissingFile: await sampleMultipart(
				baseUrl,
				"/api/notification-sounds",
				token,
				(formData) => {
					formData.append("note", "missing file");
				},
			),
			notificationSoundUploadBadMime: await sampleMultipart(
				baseUrl,
				"/api/notification-sounds",
				token,
				(formData) => {
					formData.append(
						"file",
						new Blob(["ID3"], { type: "application/octet-stream" }),
						"tone.bin",
					);
				},
			),
			notificationSoundUploadValid,
			notificationSoundGetUploaded: await sampleJSON(baseUrl, notificationSoundIdPath, token),
			notificationSoundDeleteUploaded: await sampleJSON(baseUrl, notificationSoundIdPath, token, {
				method: "DELETE",
			}),
			notificationSoundDeleteMissing: await sampleJSON(
				baseUrl,
				"/api/notification-sounds/missing",
				token,
				{ method: "DELETE" },
			),
			volumeSnapshots: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/volume-snapshots",
				token,
			),
			volumeSnapshotsByService: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/volume-snapshots?serviceName=db",
				token,
			),
			volumeSnapshotDetail: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/vs_contract_fixture",
				token,
			),
			volumeSnapshotUpdateName: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/vs_contract_fixture",
				token,
				{ method: "PATCH", body: { name: "Contract DB Snapshot" } },
			),
			volumeSnapshotApplications: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/vs_contract_fixture/applications",
				token,
			),
			volumeSnapshotMissing: await sampleJSON(baseUrl, "/api/volume-snapshots/missing", token),
			volumeSnapshotUpdateMissing: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/missing",
				token,
				{ method: "PATCH", body: { name: "Missing snapshot" } },
			),
			volumeSnapshotDeleteMissing: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/missing",
				token,
				{ method: "DELETE" },
			),
			volumeSnapshotApplyMissing: await sampleJSON(
				baseUrl,
				"/api/volume-snapshots/missing/apply",
				token,
				{ method: "POST", body: { targetChapterId: "c_graph_target" } },
			),
			browserSessionsEmpty: await sampleJSON(
				baseUrl,
				"/api/narrators/n_search_fixture/browser-sessions",
				token,
			),
			rulerSegmentSummary: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/ruler/segment?from=commit-alpha&detail=summary",
				token,
			),
			rulerSegmentFull: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/ruler/segment?from=commit-alpha",
				token,
			),
			graphPositionsInvalidMissingAxis: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/graph/positions",
				token,
				{
					method: "PATCH",
					body: { positions: [{ chapterId: "c_search_fixture", crossOffset: 88 }] },
				},
			),
			graphPositionsValid: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/graph/positions",
				token,
				{
					method: "PATCH",
					body: {
						positions: [
							{
								chapterId: "c_search_fixture",
								anchorCommitSha: "commit-alpha",
								axisOffset: 77,
								crossOffset: 88,
								panelExpanded: true,
								panelWidth: 333,
								panelHeight: 222,
							},
						],
					},
				},
			),
			rulerPositionsInvalidNegativeCross: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/ruler/positions",
				token,
				{
					method: "PATCH",
					body: {
						positions: [
							{
								chapterId: "c_graph_target",
								anchorCommitSha: "commit-alpha",
								axisOffset: 44,
								crossOffset: -1,
							},
						],
					},
				},
			),
			rulerPositionsValid: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/ruler/positions",
				token,
				{
					method: "PATCH",
					body: {
						positions: [
							{
								chapterId: "c_graph_target",
								anchorCommitSha: "commit-alpha",
								axisOffset: 44,
								crossOffset: 55,
								width: 444,
								height: 333,
							},
						],
					},
				},
			),
			rulerSegmentAfterPositions: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/ruler/segment?from=commit-alpha",
				token,
			),
			projectGraph: await sampleJSON(baseUrl, "/api/projects/p_search_fixture/graph", token),
			chapterCreateFixtureStateBefore: chapterCreateFixtureState(home),
			chapterCreateFixture: await createChapterCreateFixture(baseUrl, token, home),
			chapterCreateFixtureStateAfter: chapterCreateFixtureState(home),
			projectGraphAfterChapterCreate: await sampleJSON(
				baseUrl,
				"/api/projects/p_chapter_create_fixture/graph",
				token,
			),
			chapterDormantWakeFixtureStateBefore: chapterDormantWakeFixtureState(home),
			chapterDormantWakeDormant: dormantWakeResponseSample(
				await sampleJSON(baseUrl, "/api/chapters/c_dormant_wake_fixture/dormant", token, {
					method: "POST",
				}),
				home,
			),
			chapterDormantWakeFixtureStateAfterDormant: chapterDormantWakeFixtureState(home),
			chapterDormantWakeWake: dormantWakeResponseSample(
				await sampleJSON(baseUrl, "/api/chapters/c_dormant_wake_fixture/wake", token, {
					method: "POST",
				}),
				home,
			),
			chapterDormantWakeFixtureStateAfterWake: chapterDormantWakeFixtureState(home),
			projectCreateFixtureStateBefore: projectCreateFixtureState(home),
			projectCreateFixture: await sampleJSON(baseUrl, "/api/projects", token, {
				method: "POST",
				body: {
					name: "Project Create Fixture",
					description: "Project create TS-Go DB/Git/FS diff fixture",
					repoMode: "existing",
					gitPath: projectCreateFixtureRepoPath(home),
					flowMode: "classic",
				},
			}),
			projectCreateFixtureStateAfter: projectCreateFixtureState(home),
			projectsAfterProjectCreate: await sampleJSON(baseUrl, "/api/projects", token),
			projectRealCleanupWarningStateBefore: projectRealCleanupWarningFixtureState(home),
			projectRealCleanupWarning: await sampleJSON(
				baseUrl,
				"/api/projects/p_real_cleanup_fixture",
				token,
				{ method: "DELETE" },
			),
			projectRealCleanupWarningStateAfter: projectRealCleanupWarningFixtureState(home),
			projectDeleteFixtureStateBefore: projectDeleteFixtureState(home),
			projectDeleteFixture: await sampleJSON(baseUrl, "/api/projects/p_delete_fixture", token, {
				method: "DELETE",
			}),
			projectDeleteFixtureGetAfter: await sampleJSON(
				baseUrl,
				"/api/projects/p_delete_fixture",
				token,
			),
			projectsAfterProjectDelete: await sampleJSON(baseUrl, "/api/projects", token),
			projectDeleteFixtureStateAfter: projectDeleteFixtureState(home),
			chapterDeleteFixture: await sampleJSON(baseUrl, "/api/chapters/c_delete_fixture", token, {
				method: "DELETE",
			}),
			chapterDeleteFixtureGetAfter: await sampleJSON(
				baseUrl,
				"/api/chapters/c_delete_fixture",
				token,
			),
			projectGraphAfterChapterDelete: await sampleJSON(
				baseUrl,
				"/api/projects/p_search_fixture/graph",
				token,
			),
			chapterEdgesMissingScope: await sampleJSON(baseUrl, "/api/chapter-edges", token),
			chapterEdgesInvalidTypeReview: await sampleJSON(
				baseUrl,
				"/api/chapter-edges?projectId=p_search_fixture&type=review",
				token,
			),
			chapterEdgesProject: await sampleJSON(
				baseUrl,
				"/api/chapter-edges?projectId=p_search_fixture",
				token,
			),
			chapterEdgesDependency: await sampleJSON(
				baseUrl,
				"/api/chapter-edges?projectId=p_search_fixture&type=dependency",
				token,
			),
			chapterEdgesByChapter: await sampleJSON(
				baseUrl,
				"/api/chapter-edges?chapterId=c_search_fixture",
				token,
			),
			dependencyEdgeCreateLifecycle,
			chapterEdgesAfterLifecycleCreate,
			projectGraphAfterDependencyCreate,
			dependencyEdgeDeleteLifecycle,
			chapterEdgesAfterLifecycleDelete,
			projectGraphAfterDependencyDelete,
			chapterEdgeDeleteMissing: await sampleJSON(baseUrl, "/api/chapter-edges/missing", token, {
				method: "DELETE",
				body: {},
			}),
			search: await sampleJSON(
				baseUrl,
				"/api/search?q=contract&entities=chapters,messages,narrators",
				token,
			),
			searchCJK: await sampleJSON(
				baseUrl,
				`/api/search?q=${encodeURIComponent("叙事分叉")}&entities=chapters,messages,narrators`,
				token,
			),
			searchShortChapter: await sampleJSON(baseUrl, "/api/search?q=Al&entities=chapters", token),
			searchLargeTopK: await sampleJSON(
				baseUrl,
				"/api/search?q=auroraanchor&entities=chapters,messages,narrators&limit=6",
				token,
			),
			searchLargeTieBreak: await sampleJSON(
				baseUrl,
				"/api/search?q=chrononeedle&entities=chapters&limit=2",
				token,
			),
			routines: await sampleJSON(baseUrl, "/api/routines", token),
			projectRoutinesInitial,
			projectRoutineInvalidAction,
			projectRoutineMissingRoutine,
			projectRoutineMissingProject,
			projectRoutineEnableTerminal,
			projectRoutinesAfterEnable,
			projectRoutineDisableTerminal,
			projectRoutinesAfterDisable,
			projectRoutineResetTerminal,
			projectRoutinesAfterReset,
			routinesGlobalPrompt: await sampleJSON(baseUrl, "/api/routines/global-prompt", token),
			skillsGlobal: await sampleJSON(baseUrl, "/api/skills/global", token),
			globalSkillCreateValid,
			globalSkillDetailCreated,
			globalSkillCreateDuplicate,
			globalSkillToggleInvalidMissingEnabled,
			globalSkillToggleDisable,
			globalSkillToggleEnable,
			globalSkillUpdateValid,
			globalSkillOldDetailAfterRename,
			globalSkillDetailUpdated,
			globalSkillListAfterUpdate,
			globalSkillDeleteValid,
			globalSkillDetailAfterDelete,
			globalSkillCreateInvalidMissingDescription,
			globalSkillUpdateMissing,
			globalSkillDeleteMissing,
			globalSkillToggleMissing,
			skillsProject: await sampleJSON(baseUrl, "/api/skills?projectId=p_search_fixture", token),
			skillProjectDetail: await sampleJSON(
				baseUrl,
				"/api/skills/project-contract?projectId=p_search_fixture",
				token,
			),
			mcpServers,
			mcpTools,
			mcpCreateInvalidArgs,
			mcpCreateInvalidToolPermissions,
			mcpTestInvalidBody,
			mcpCreateDisabled,
			mcpTestStdioValid,
			mcpCreateStdioEnabled,
			mcpServersAfterStdioCreate,
			mcpToolsAfterStdioCreate,
			mcpDeleteStdioValid,
			customSubagents: await sampleJSON(baseUrl, "/api/custom-subagents", token),
			customSubagentCreateValid,
			customSubagentDetailCreated,
			customSubagentUpdateValid,
			customSubagentOldDetailAfterRename,
			customSubagentDetailUpdated,
			customSubagentListAfterUpdate,
			customSubagentDeleteValid,
			customSubagentDetailAfterDelete,
			customSubagentMissing: await sampleJSON(baseUrl, "/api/custom-subagents/missing", token),
			customSubagentCreateInvalid: await sampleJSON(baseUrl, "/api/custom-subagents", token, {
				method: "POST",
				body: {},
			}),
			customSubagentDeleteMissing: await sampleJSON(
				baseUrl,
				"/api/custom-subagents/missing",
				token,
				{ method: "DELETE", body: {} },
			),
			workspaces: await sampleJSON(baseUrl, "/api/workspaces", token),
			workspaceCreateValid,
			workspaceDetailCreated,
			workspaceUpdateValid,
			workspaceDetailUpdated,
			workspaceListAfterUpdate,
			workspaceDeleteValid,
			workspaceDetailAfterDelete,
			workspaceMissing: await sampleJSON(baseUrl, "/api/workspaces/missing", token),
			workspaceCreateInvalidShortTree: await sampleJSON(baseUrl, "/api/workspaces", token, {
				method: "POST",
				body: { tree: "x" },
			}),
			workspaceUpdateMissing: await sampleJSON(baseUrl, "/api/workspaces/missing", token, {
				method: "PATCH",
				body: { title: "Missing workspace" },
			}),
			workspaceDeleteMissing: await sampleJSON(baseUrl, "/api/workspaces/missing", token, {
				method: "DELETE",
				body: {},
			}),
			hooks: await sampleJSON(baseUrl, "/api/hooks", token),
			hooksAll: await sampleJSON(baseUrl, "/api/hooks/all", token),
			hookCreateValid,
			hookDetailCreated,
			hookUpdateValid,
			hookDetailUpdated,
			hookListAfterUpdate,
			hookAllAfterUpdate,
			hookUpdateClearActiveURLInvalid,
			hookUpdateMissingInvalidBody,
			hookDeleteValid,
			hookDetailAfterDelete,
			hookMissing: await sampleJSON(baseUrl, "/api/hooks/missing", token),
			hookDeleteMissing: await sampleJSON(baseUrl, "/api/hooks/missing", token, {
				method: "DELETE",
				body: {},
			}),
			userHooks: await sampleJSON(baseUrl, "/api/hooks", userToken),
			benchmarkSuites: await sampleJSON(baseUrl, "/api/benchmarks/suites", token),
			benchmarkRuns: await sampleJSON(baseUrl, "/api/benchmarks/runs", token),
			benchmarkMissingResults: await sampleJSON(
				baseUrl,
				"/api/benchmarks/runs/missing/results",
				token,
			),
			benchmarkMissingSummary: await sampleJSON(
				baseUrl,
				"/api/benchmarks/runs/missing/summary",
				token,
			),
			benchmarkCompareMissingParam: await sampleJSON(baseUrl, "/api/benchmarks/compare", token),
			userBenchmarkSuites: await sampleJSON(baseUrl, "/api/benchmarks/suites", userToken),
			userBenchmarkCompare: await sampleJSON(baseUrl, "/api/benchmarks/compare", userToken),
			clineStatus,
			clineBalance,
			clineRecommendedModelsRefresh,
			clineRecommendedModelsCached,
			clineModels,
			clineMissingProviderModels,
			clineMissingProviderRefresh,
			clinePoolSearch,
			clinePoolCount,
			clineEnabledModelsInvalid,
			clineEnabledModelsValid,
			clineStatusAfterEnabledModels,
			clineSettingsAfterEnabledModels,
			clineEnabledModelsResetBeforeAuth,
			clineAuthCancel,
			clineAuthCallbackInvalid,
			clineAuthCallbackValid,
			clineStatusAfterAuthCallback,
			clineSettingsAfterAuthCallback,
			clineUserInfoRefreshValid,
			clineBalanceAfterUserInfoRefresh,
			clineStatusAfterUserInfoRefresh,
			clineAuthLogout,
			clineEnabledModelsResetBeforeBrowserAuth,
			clineBrowserAuthStart,
			clineBrowserCallback,
			clineStatusAfterBrowserCallback,
			clineSettingsAfterBrowserCallback,
			clineBrowserAuthLogout,
			clineAuthCallbackExpired,
			clineBalanceAfterExpiredAuthRefresh,
			clineStatusAfterExpiredAuthRefresh,
			clineAuthLogoutAfterExpiredRefresh,
			clineAuthCallbackInvalidRefresh,
			clineBalanceAfterInvalidRefresh,
			clineStatusAfterInvalidRefresh,
			clineProviderRefreshValid,
			clineProviderModelsAfterRefresh,
			clineModelsAfterProviderRefresh,
			codexStatus: await sampleJSON(baseUrl, "/api/codex/status", token),
			codexDefaultReasoningEffort: await sampleJSON(
				baseUrl,
				"/api/codex/default-reasoning-effort",
				token,
			),
			codexUseWebSocketMissing: await sampleJSON(baseUrl, "/api/codex/use-websocket", token, {
				method: "POST",
				body: {},
			}),
			codexUseWebSocketInvalid: await sampleJSON(baseUrl, "/api/codex/use-websocket", token, {
				method: "POST",
				body: { useWebSocket: "yes" },
			}),
			codexLoadBalancingInvalid: await sampleJSON(
				baseUrl,
				"/api/codex/load-balancing-mode",
				token,
				{ method: "POST", body: { mode: "tiered_sticky" } },
			),
			codexTierOrderInvalid: await sampleJSON(baseUrl, "/api/codex/tier-order", token, {
				method: "POST",
				body: { tierOrder: ["team", "invalid"] },
			}),
			codexTierOrderValid: await sampleJSON(baseUrl, "/api/codex/tier-order", token, {
				method: "POST",
				body: { tierOrder: ["team", "pro", "team"] },
			}),
			codexImportMissing: await sampleJSON(baseUrl, "/api/codex/import", token, {
				method: "POST",
				body: {},
			}),
			codexImportInvalidNoToken: await sampleJSON(baseUrl, "/api/codex/import", token, {
				method: "POST",
				body: { credentials: [{ email: "codex-no-token@example.com" }] },
			}),
			codexCredentialDisableMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing/disable",
				token,
				{ method: "POST", body: {} },
			),
			codexCredentialEnableMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing/enable",
				token,
				{ method: "POST", body: {} },
			),
			codexCredentialResetMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing/reset",
				token,
				{ method: "POST", body: {} },
			),
			codexCredentialUpdateMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing",
				token,
				{ method: "PATCH", body: { displayName: "Missing" } },
			),
			codexCredentialUpdateEmptyBody: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/codex-contract",
				token,
				{ method: "PATCH" },
			),
			codexCredentialUpdateInvalidJSON: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/codex-contract",
				token,
				{ method: "PATCH", rawBody: "{" },
			),
			codexCredentialUpdateMissingInvalidJSON: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing",
				token,
				{ method: "PATCH", rawBody: "{" },
			),
			codexCredentialDeleteMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing",
				token,
				{ method: "DELETE" },
			),
			codexCredentialRefreshMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing/refresh",
				token,
				{ method: "POST", body: {} },
			),
			codexCredentialUsageMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/missing/usage",
				token,
				{ method: "POST", body: {} },
			),
			codexCredentialBatchDeleteMissing: await sampleJSON(
				baseUrl,
				"/api/codex/credentials/batch",
				token,
				{ method: "DELETE", body: { ids: ["missing"] } },
			),
			userOpenaiProviderRefreshValid: await sampleJSON(
				baseUrl,
				"/api/openai/providers/openai-secret-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userOpenaiGlobalRefresh: await sampleJSON(baseUrl, "/api/openai/models/refresh", userToken, {
				method: "POST",
				body: {},
			}),
			userOpenaiProviderRefreshNoKey: await sampleJSON(
				baseUrl,
				"/api/openai/providers/openai-no-key-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userOpenaiModels: await sampleJSON(baseUrl, "/api/openai/models", userToken),
			userOpenaiMissingRefresh: await sampleJSON(
				baseUrl,
				"/api/openai/providers/missing/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userAnthropicProviderRefreshValid: await sampleJSON(
				baseUrl,
				"/api/anthropic/providers/anthropic-secret-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userAnthropicGlobalRefresh: await sampleJSON(
				baseUrl,
				"/api/anthropic/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userAnthropicProviderRefreshNoKey: await sampleJSON(
				baseUrl,
				"/api/anthropic/providers/anthropic-no-key-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userAnthropicMissingRefresh: await sampleJSON(
				baseUrl,
				"/api/anthropic/providers/missing/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNkpProviderRefreshValid: await sampleJSON(
				baseUrl,
				"/api/nkp/providers/nkp-secret-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNkpGlobalRefresh: await sampleJSON(baseUrl, "/api/nkp/models/refresh", userToken, {
				method: "POST",
				body: {},
			}),
			userNkpProviderRefreshNoKey: await sampleJSON(
				baseUrl,
				"/api/nkp/providers/nkp-no-key-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNkpModels: await sampleJSON(baseUrl, "/api/nkp/models", userToken),
			userNkpQuota: await sampleJSON(
				baseUrl,
				"/api/nkp/providers/nkp-secret-fixture/quota",
				userToken,
			),
			userNkpQuotas: await sampleJSON(baseUrl, "/api/nkp/quotas", userToken),
			settingsAfterNkpQuota: await sampleJSON(baseUrl, "/api/settings", token),
			userNkpMissingRefresh: await sampleJSON(
				baseUrl,
				"/api/nkp/providers/missing/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugProviderRefreshValid: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-secret-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugProviderRefreshNoKey: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-login-fixture/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugGlobalRefresh: await sampleJSON(baseUrl, "/api/nug/models/refresh", userToken, {
				method: "POST",
				body: {},
			}),
			userNugModels: await sampleJSON(baseUrl, "/api/nug/models", userToken),
			userNugQuota: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-secret-fixture/quota",
				userToken,
			),
			userNugQuotas: await sampleJSON(baseUrl, "/api/nug/quotas", userToken),
			userNugChannelsHealth: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-secret-fixture/channels/health",
				userToken,
			),
			userNugUsage: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-secret-fixture/usage?limit=2&offset=0",
				userToken,
			),
			userNugUsageSummary: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-secret-fixture/usage/summary?range=30days",
				userToken,
			),
			userNugLoginValid: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-login-fixture/login",
				userToken,
				{ method: "POST", body: { username: "alice", password: "secret" } },
			),
			userNugApiKeyValid: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-login-fixture/api-key",
				userToken,
				{ method: "POST", body: { sessionToken: "manual-session", name: "manual-contract" } },
			),
			userNugLoginMissingFields: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-login-fixture/login",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugApiKeyMissingSession: await sampleJSON(
				baseUrl,
				"/api/nug/providers/nug-login-fixture/api-key",
				userToken,
				{ method: "POST", body: {} },
			),
			settingsAfterNugLogin: await sampleJSON(baseUrl, "/api/settings", token),
			...(await sampleNugOAuthFlow(baseUrl, userToken, token)),
			userNugMissingRefresh: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/models/refresh",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugLoginMissing: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/login",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugApiKeyMissing: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/api-key",
				userToken,
				{ method: "POST", body: {} },
			),
			userNugOAuthStartMissing: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/oauth/start",
				userToken,
			),
			userCodexQuotaOverview: await sampleJSON(baseUrl, "/api/codex/quota-overview", userToken),
			userCodexStatus: await sampleJSON(baseUrl, "/api/codex/status", userToken),
			userClineStatus: await sampleJSON(baseUrl, "/api/cline/status", userToken),
			userKiroStatus: await sampleJSON(baseUrl, "/api/kiro/status", userToken),
			nkpMissingQuota: await sampleJSON(baseUrl, "/api/nkp/providers/missing/quota", token),
			nugMissingQuota: await sampleJSON(baseUrl, "/api/nug/providers/missing/quota", token),
			nugMissingUsage: await sampleJSON(baseUrl, "/api/nug/providers/missing/usage", token),
			nugMissingUsageSummary: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/usage/summary?range=30days",
				token,
			),
			nugMissingChannelsHealth: await sampleJSON(
				baseUrl,
				"/api/nug/providers/missing/channels/health",
				token,
			),
			runtimeCached: await sampleJSON(baseUrl, "/api/runtime/cached", token),
			runtimeScan: await sampleJSON(baseUrl, "/api/runtime/scan", token),
			runtimeCleanupTerminals: await sampleJSON(baseUrl, "/api/runtime/cleanup", token, {
				method: "POST",
				body: { target: "terminals", execute: true },
			}),
			userRuntimeCached: await sampleJSON(baseUrl, "/api/runtime/cached", userToken),
			userRuntimeScan: await sampleJSON(baseUrl, "/api/runtime/scan", userToken),
			userRuntimeCleanup: await sampleJSON(baseUrl, "/api/runtime/cleanup", userToken, {
				method: "POST",
				body: { target: "terminals", execute: true },
			}),
			storageCached: await sampleJSON(baseUrl, "/api/storage/cached", token),
			storageVacuum: await sampleJSON(baseUrl, "/api/storage/database/vacuum", token, {
				method: "POST",
				body: {},
			}),
			userStorageCached: await sampleJSON(baseUrl, "/api/storage/cached", userToken),
			userStorageCleanup: await sampleJSON(baseUrl, "/api/storage/cleanup", userToken, {
				method: "POST",
				body: { target: "uploads" },
			}),
			userStorageVacuum: await sampleJSON(baseUrl, "/api/storage/database/vacuum", userToken, {
				method: "POST",
				body: {},
			}),
			updateApply: await sampleJSON(baseUrl, "/api/update/apply", token, {
				method: "POST",
				body: {},
			}),
			updateCleanup: await sampleJSON(baseUrl, "/api/update/cleanup", token, {
				method: "POST",
				body: {},
			}),
			updateDirectory: await sampleJSON(baseUrl, "/api/update/directory", token),
			updateStatus: await sampleJSON(baseUrl, "/api/update/status", token),
			updateVersion: await sampleJSON(baseUrl, "/api/update/version", token),
			updateCheck: await sampleJSON(baseUrl, "/api/update/check", token),
			updateCheckTrusted,
			userUpdateCheckTrusted,
			userUpdateCheck: await sampleJSON(baseUrl, "/api/update/check", userToken),
			userUpdateVersion: await sampleJSON(baseUrl, "/api/update/version", userToken),
			userUpdateDirectory: await sampleJSON(baseUrl, "/api/update/directory", userToken),
			userUpdateStatus: await sampleJSON(baseUrl, "/api/update/status", userToken),
			userUpdateCleanup: await sampleJSON(baseUrl, "/api/update/cleanup", userToken, {
				method: "POST",
				body: {},
			}),
			userUpdateApply: await sampleJSON(baseUrl, "/api/update/apply", userToken, {
				method: "POST",
				body: {},
			}),
		},
		sse: {
			updateDownload: await sampleSSE(baseUrl, "/api/update/download", token),
			updateDownloadTrustedError,
			userUpdateDownload: await sampleSSE(baseUrl, "/api/update/download", userToken),
			storageScan: await sampleSSE(baseUrl, "/api/storage/scan", token, { method: "GET" }),
			userStorageScan: await sampleSSE(baseUrl, "/api/storage/scan", userToken, {
				method: "GET",
			}),
		},
		ws: {
			narrator: await sampleWS(baseUrl, "/ws/narrator", token),
			terminal: await sampleWS(baseUrl, "/ws/terminal", token),
			unauthNarrator: await sampleWS(baseUrl, "/ws/narrator"),
			unauthTerminal: await sampleWS(baseUrl, "/ws/terminal"),
		},
	};
}

function shapeOf(value: unknown): unknown {
	if (Array.isArray(value)) return "array";
	if (!value || typeof value !== "object") return typeof value;
	return Object.fromEntries(
		Object.entries(value)
			.sort(([a], [b]) => a.localeCompare(b))
			.map(([key, child]) => [key, Array.isArray(child) ? "array" : typeof child]),
	);
}

function shapeCovers(expected: unknown, actual: unknown): boolean {
	const expectedShape = shapeOf(expected);
	const actualShape = shapeOf(actual);
	if (
		!expectedShape ||
		typeof expectedShape !== "object" ||
		Array.isArray(expectedShape) ||
		!actualShape ||
		typeof actualShape !== "object" ||
		Array.isArray(actualShape)
	) {
		return expectedShape === actualShape;
	}
	for (const [key, expectedKind] of Object.entries(expectedShape)) {
		if ((actualShape as Record<string, unknown>)[key] !== expectedKind) return false;
	}
	return true;
}

function recordBody(value: unknown): Record<string, unknown> {
	return value && typeof value === "object" && !Array.isArray(value)
		? (value as Record<string, unknown>)
		: {};
}

function expectJSONStatus(
	label: string,
	sample: BackendSample,
	name: string,
	expected: number,
	diffs: string[],
): void {
	const status = sample.json[name]?.status;
	if (status !== expected)
		diffs.push(`semantic.${label}.json.${name}.status expected ${expected} got ${status}`);
}

function expectJSONValue(
	label: string,
	sample: BackendSample,
	name: string,
	key: string,
	expected: unknown,
	diffs: string[],
): void {
	const actual = recordBody(sample.json[name]?.body)[key];
	if (actual !== expected) {
		diffs.push(
			`semantic.${label}.json.${name}.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(actual)}`,
		);
	}
}

function expectJSONStringLength(
	label: string,
	sample: BackendSample,
	name: string,
	key: string,
	expectedLength: number,
	diffs: string[],
): void {
	const actual = recordBody(sample.json[name]?.body)[key];
	if (typeof actual !== "string" || actual.length !== expectedLength) {
		diffs.push(
			`semantic.${label}.json.${name}.${key} expected string length ${expectedLength} got ${JSON.stringify(actual)}`,
		);
	}
}

function expectJSONArray(
	label: string,
	sample: BackendSample,
	name: string,
	key: string,
	diffs: string[],
): void {
	const actual = recordBody(sample.json[name]?.body)[key];
	if (!Array.isArray(actual)) {
		diffs.push(
			`semantic.${label}.json.${name}.${key} expected array got ${JSON.stringify(actual)}`,
		);
	}
}

function sampleJSONArray(sample: BackendSample, name: string, key?: string): unknown[] {
	if (key === undefined) {
		const body = sample.json[name]?.body;
		return Array.isArray(body) ? body : [];
	}
	const value = recordBody(sample.json[name]?.body)[key];
	return Array.isArray(value) ? value : [];
}

function expectArrayObject(
	label: string,
	sample: BackendSample,
	name: string,
	key: string | undefined,
	matchKey: string,
	matchValue: unknown,
	diffs: string[],
): Record<string, unknown> | undefined {
	const item = sampleJSONArray(sample, name, key).find((raw): raw is Record<string, unknown> => {
		return !!raw && typeof raw === "object" && !Array.isArray(raw) && raw[matchKey] === matchValue;
	});
	if (!item) {
		diffs.push(
			`semantic.${label}.json.${name}.${key ?? "body"}.missingObject ${matchKey}=${JSON.stringify(matchValue)}`,
		);
	}
	return item;
}

function expectProviderNoKeyRefresh(
	label: string,
	sample: BackendSample,
	name: string,
	expectedError: string,
	diffs: string[],
): void {
	expectJSONStatus(label, sample, name, 500, diffs);
	expectJSONValue(label, sample, name, "error", expectedError, diffs);
}

function expectProviderGlobalRefresh(
	label: string,
	sample: BackendSample,
	name: string,
	secretProviderId: string,
	noKeyProviderId: string,
	noKeyError: string,
	modelIdKey: string,
	expectedModelIds: string,
	diffs: string[],
): void {
	expectJSONStatus(label, sample, name, 200, diffs);
	expectJSONValue(label, sample, name, "fromCache", false, diffs);
	const results = sampleJSONArray(sample, name, "results");
	if (results.length !== 2) {
		diffs.push(
			`semantic.${label}.json.${name}.results expected 2 provider results got ${results.length}`,
		);
	}
	const success = expectArrayObject(
		label,
		sample,
		name,
		"results",
		"providerId",
		secretProviderId,
		diffs,
	);
	if (success && success.count !== 2) {
		diffs.push(
			`semantic.${label}.json.${name}.${secretProviderId}.count expected 2 got ${JSON.stringify(success.count)}`,
		);
	}
	const missingKey = expectArrayObject(
		label,
		sample,
		name,
		"results",
		"providerId",
		noKeyProviderId,
		diffs,
	);
	if (missingKey && (missingKey.count !== 0 || missingKey.error !== noKeyError)) {
		diffs.push(
			`semantic.${label}.json.${name}.${noKeyProviderId} expected count=0 error=${noKeyError} got ${JSON.stringify(missingKey)}`,
		);
	}
	const ids = sampleJSONArray(sample, name, "models")
		.map((model) => recordBody(model)[modelIdKey])
		.join(",");
	if (ids !== expectedModelIds) {
		diffs.push(`semantic.${label}.json.${name}.models expected ${expectedModelIds} got ${ids}`);
	}
}

function valueAtPath(value: unknown, path: string[]): unknown {
	let current = value;
	for (const key of path) {
		if (!current || typeof current !== "object" || Array.isArray(current)) return undefined;
		current = (current as Record<string, unknown>)[key];
	}
	return current;
}

function expectJSONPathValue(
	label: string,
	sample: BackendSample,
	name: string,
	path: string[],
	expected: unknown,
	diffs: string[],
): void {
	const actual = valueAtPath(sample.json[name]?.body, path);
	if (actual !== expected) {
		diffs.push(
			`semantic.${label}.json.${name}.${path.join(".")} expected ${JSON.stringify(expected)} got ${JSON.stringify(actual)}`,
		);
	}
}

function findSettingsProvider(
	sample: BackendSample,
	name: string,
	field: string,
	id: string,
): Record<string, unknown> | undefined {
	const providers = recordBody(sample.json[name]?.body)[field];
	if (!Array.isArray(providers)) return undefined;
	return providers.find((raw): raw is Record<string, unknown> => {
		if (!raw || typeof raw !== "object" || Array.isArray(raw)) return false;
		return raw.id === id;
	});
}

function expectSettingsSecretMasked(
	label: string,
	sample: BackendSample,
	name: string,
	field: string,
	id: string,
	secretKey: string,
	rawSecret: string,
	diffs: string[],
): void {
	const provider = findSettingsProvider(sample, name, field, id);
	if (!provider) {
		diffs.push(`semantic.${label}.json.${name}.${field}.missingProvider ${id}`);
		return;
	}
	const actual = provider[secretKey];
	if (typeof actual !== "string" || actual === rawSecret || !actual.startsWith("********")) {
		diffs.push(
			`semantic.${label}.json.${name}.${field}.${id}.${secretKey} expected masked secret got ${JSON.stringify(actual)}`,
		);
	}
}

function expectSettingsAuthSecretHidden(
	label: string,
	sample: BackendSample,
	name: string,
	diffs: string[],
): void {
	const auth = recordBody(sample.json[name]?.body).auth;
	if (!auth || typeof auth !== "object" || Array.isArray(auth)) return;
	const jwtSecret = (auth as Record<string, unknown>).jwtSecret;
	if (typeof jwtSecret === "string" && jwtSecret !== "") {
		diffs.push(`semantic.${label}.json.${name}.auth.jwtSecret leaked string`);
	}
}

function expectSettingsCustomApiQuotaContract(
	label: string,
	sample: BackendSample,
	name: string,
	diffs: string[],
): void {
	const quotas = recordBody(recordBody(sample.json[name]?.body).customApiQuotas);
	const quota = recordBody(quotas["openai-secret-fixture"]);
	const keys = Object.keys(quota).sort().join(",");
	if (
		quota.quotaBalance !== "$42.50" ||
		quota.detailedQuotaBalance !== "42.50 credits\nreset soon" ||
		quota.fetchedAt !== 1_700_000_000_000 ||
		keys !== "detailedQuotaBalance,fetchedAt,quotaBalance"
	) {
		diffs.push(
			`semantic.${label}.json.${name}.customApiQuotas.openai-secret-fixture expected full quota cache payload got ${JSON.stringify(quota)}`,
		);
	}
}

function expectSettingsSecretContracts(
	label: string,
	sample: BackendSample,
	diffs: string[],
): void {
	for (const name of ["settings", "userSettings", "settingsMaskedProviderPatch"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectSettingsAuthSecretHidden(label, sample, name, diffs);
		if (name !== "settingsMaskedProviderPatch") {
			expectSettingsCustomApiQuotaContract(label, sample, name, diffs);
		}

		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"openaiProviders",
			"openai-secret-fixture",
			"apiKey",
			"sk-openai-secret",
			diffs,
		);
		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"anthropicProviders",
			"anthropic-secret-fixture",
			"apiKey",
			"anthropic-key",
			diffs,
		);
		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"nkpProviders",
			"nkp-secret-fixture",
			"apiKey",
			"nkp-key",
			diffs,
		);
		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"nugProviders",
			"nug-secret-fixture",
			"apiKey",
			"sk-nug-secret",
			diffs,
		);
		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"nugProviders",
			"nug-secret-fixture",
			"oauthClientSecret",
			"nug-oauth-secret",
			diffs,
		);
		expectSettingsSecretMasked(
			label,
			sample,
			name,
			"nugProviders",
			"nug-oauth-fixture",
			"oauthClientSecret",
			"nug-oauth-client-secret",
			diffs,
		);
	}
}

function searchResultTypes(sample: BackendSample, name: string): Set<string> {
	const body = recordBody(sample.json[name]?.body);
	const results = Array.isArray(body.results) ? body.results : [];
	const types = new Set<string>();
	for (const raw of results) {
		if (!raw || typeof raw !== "object" || Array.isArray(raw)) continue;
		const resultType = (raw as Record<string, unknown>).type;
		if (typeof resultType === "string") types.add(resultType);
	}
	return types;
}

function expectSearchTypes(
	label: string,
	sample: BackendSample,
	name: string,
	expectedTypes: string[],
	diffs: string[],
): void {
	const types = searchResultTypes(sample, name);
	for (const expected of expectedTypes) {
		if (!types.has(expected)) {
			diffs.push(`semantic.${label}.json.${name}.missingSearchType ${expected}`);
		}
	}
}

type SearchFieldExpectation = {
	key: string;
	type?: "string" | "number";
	equals?: unknown;
	includes?: string;
};

function searchResultRecords(sample: BackendSample, name: string): Record<string, unknown>[] {
	const body = recordBody(sample.json[name]?.body);
	const results = Array.isArray(body.results) ? body.results : [];
	return results.filter((raw): raw is Record<string, unknown> => {
		return Boolean(raw && typeof raw === "object" && !Array.isArray(raw));
	});
}

function searchResultKeys(sample: BackendSample, name: string): string[] {
	return searchResultRecords(sample, name).map((result) => `${result.type}:${result.id}`);
}

function findSearchResult(
	sample: BackendSample,
	name: string,
	type: string,
	id: string,
): Record<string, unknown> | undefined {
	return searchResultRecords(sample, name).find(
		(result) => result.type === type && result.id === id,
	);
}

function expectSearchResultContract(
	label: string,
	sample: BackendSample,
	name: string,
	type: string,
	id: string,
	fields: SearchFieldExpectation[],
	diffs: string[],
): void {
	const result = findSearchResult(sample, name, type, id);
	if (!result) {
		diffs.push(`semantic.${label}.json.${name}.missingSearchResult ${type}:${id}`);
		return;
	}
	for (const field of fields) {
		const actual = result[field.key];
		if (field.type && typeof actual !== field.type) {
			diffs.push(
				`semantic.${label}.json.${name}.${type}:${id}.${field.key} expected ${field.type} got ${JSON.stringify(actual)}`,
			);
			continue;
		}
		if ("equals" in field && actual !== field.equals) {
			diffs.push(
				`semantic.${label}.json.${name}.${type}:${id}.${field.key} expected ${JSON.stringify(field.equals)} got ${JSON.stringify(actual)}`,
			);
		}
		if (field.includes && (typeof actual !== "string" || !actual.includes(field.includes))) {
			diffs.push(
				`semantic.${label}.json.${name}.${type}:${id}.${field.key} expected to include ${JSON.stringify(field.includes)} got ${JSON.stringify(actual)}`,
			);
		}
	}
}

function expectSearchLargeFixtureContracts(
	label: string,
	sample: BackendSample,
	diffs: string[],
): void {
	expectJSONStatus(label, sample, "searchLargeTopK", 200, diffs);
	expectJSONStatus(label, sample, "searchLargeTieBreak", 200, diffs);
	const largeKeys = searchResultKeys(sample, "searchLargeTopK");
	const requiredKeys = [
		"chapter:c_search_large_title",
		"chapter:c_search_large_description",
		"narrator:n_search_large_primary",
		"narrator:n_search_large_secondary",
		"message:m_search_large_primary",
		"message:m_search_large_secondary",
	];
	for (const key of requiredKeys) {
		if (!largeKeys.includes(key)) {
			diffs.push(`semantic.${label}.json.searchLargeTopK.missingSearchResult ${key}`);
		}
	}
	if (largeKeys.length !== requiredKeys.length) {
		diffs.push(
			`semantic.${label}.json.searchLargeTopK.results length expected ${requiredKeys.length} got ${largeKeys.length}`,
		);
	}
	const typeCounts = new Map<string, number>();
	for (const result of searchResultRecords(sample, "searchLargeTopK")) {
		const resultType = typeof result.type === "string" ? result.type : "unknown";
		typeCounts.set(resultType, (typeCounts.get(resultType) ?? 0) + 1);
		if (typeof result.matchScore !== "number" || result.matchScore <= 0) {
			diffs.push(
				`semantic.${label}.json.searchLargeTopK.${resultType}:${result.id}.matchScore expected positive number got ${JSON.stringify(result.matchScore)}`,
			);
		}
		if (
			typeof result.snippet !== "string" ||
			!result.snippet.toLowerCase().includes("auroraanchor")
		) {
			diffs.push(
				`semantic.${label}.json.searchLargeTopK.${resultType}:${result.id}.snippet expected auroraanchor got ${JSON.stringify(result.snippet)}`,
			);
		}
	}
	for (const resultType of ["chapter", "message", "narrator"]) {
		if (typeCounts.get(resultType) !== 2) {
			diffs.push(
				`semantic.${label}.json.searchLargeTopK.${resultType} count expected 2 got ${typeCounts.get(resultType) ?? 0}`,
			);
		}
	}
	expectSearchResultContract(
		label,
		sample,
		"searchLargeTopK",
		"chapter",
		"c_search_large_description",
		[
			{ key: "snippet", type: "string", includes: "auroraanchor" },
			{ key: "matchField", equals: "description" },
		],
		diffs,
	);
	const tieKeys = searchResultKeys(sample, "searchLargeTieBreak");
	const expectedTieKeys = ["chapter:c_search_tie_recent", "chapter:c_search_tie_older"];
	for (const [index, expected] of expectedTieKeys.entries()) {
		if (tieKeys[index] !== expected) {
			diffs.push(
				`semantic.${label}.json.searchLargeTieBreak.order[${index}] expected ${expected} got ${tieKeys[index] ?? "<missing>"}`,
			);
		}
	}
	const metadata = recordBody(sample.json.searchLargeTopK?.body).searchMetadata;
	if (metadata && typeof metadata === "object" && !Array.isArray(metadata)) {
		const body = recordBody(metadata);
		if (body.degraded !== false || body.ftsReady !== true || body.shortQuery !== false) {
			diffs.push(
				`semantic.${label}.json.searchLargeTopK.searchMetadata expected ready non-degraded FTS got ${JSON.stringify(body)}`,
			);
		}
	}
}

function expectSearchFixtureContracts(label: string, sample: BackendSample, diffs: string[]): void {
	expectSearchLargeFixtureContracts(label, sample, diffs);
	expectSearchResultContract(
		label,
		sample,
		"searchCJK",
		"chapter",
		"c_search_fixture",
		[
			{ key: "title", type: "string", includes: "叙事分叉" },
			{ key: "snippet", type: "string" },
			{ key: "matchField", type: "string" },
			{ key: "matchScore", type: "number" },
			{ key: "status", equals: "active" },
			{ key: "role", equals: "branch" },
		],
		diffs,
	);
	expectSearchResultContract(
		label,
		sample,
		"searchCJK",
		"message",
		"m_search_fixture",
		[
			{ key: "snippet", type: "string", includes: "叙事分叉" },
			{ key: "matchField", equals: "message" },
			{ key: "matchScore", type: "number" },
			{ key: "messageRole", equals: "user" },
			{ key: "narratorId", equals: "n_search_fixture" },
			{ key: "chapterId", equals: "c_search_fixture" },
		],
		diffs,
	);
	expectSearchResultContract(
		label,
		sample,
		"searchCJK",
		"narrator",
		"n_search_fixture",
		[
			{ key: "title", type: "string", includes: "叙事分叉" },
			{ key: "snippet", type: "string", includes: "叙事分叉" },
			{ key: "matchField", equals: "title" },
			{ key: "matchScore", type: "number" },
			{ key: "status", equals: "idle" },
			{ key: "chapterId", equals: "c_search_fixture" },
		],
		diffs,
	);
	expectSearchResultContract(
		label,
		sample,
		"searchShortChapter",
		"chapter",
		"c_search_fixture",
		[
			{ key: "title", type: "string", includes: "Alpha" },
			{ key: "snippet", type: "string" },
			{ key: "matchField", equals: "title" },
			{ key: "matchScore", type: "number" },
		],
		diffs,
	);
}

function expectGitStatusSummaryContract(
	label: string,
	sample: BackendSample,
	name: string,
	diffs: string[],
): void {
	expectJSONStatus(label, sample, name, 200, diffs);
	const body = recordBody(sample.json[name]?.body);
	for (const [key, expectedType] of Object.entries({
		hasChanges: "boolean",
		staged: "number",
		unstaged: "number",
		untracked: "number",
		totalFiles: "number",
		headSha: "string",
		branch: "string",
		linesAdded: "number",
		linesRemoved: "number",
	})) {
		if (typeof body[key] !== expectedType) {
			diffs.push(
				`semantic.${label}.json.${name}.${key} expected ${expectedType} got ${JSON.stringify(body[key])}`,
			);
		}
	}
	if (!Array.isArray(body.files)) {
		diffs.push(`semantic.${label}.json.${name}.files expected array`);
	}
}

function expectGitPanelFixtureContracts(
	label: string,
	sample: BackendSample,
	diffs: string[],
): void {
	expectJSONStatus(label, sample, "gitAICommitMessageEmpty", 200, diffs);
	expectJSONValue(label, sample, "gitAICommitMessageEmpty", "message", "", diffs);
	for (const name of [
		"gitStatusDirty",
		"gitStageAll",
		"gitUnstageAll",
		"gitDiscardTracked",
		"gitResetHard",
		"gitStageAllBeforeCommit",
	]) {
		expectGitStatusSummaryContract(label, sample, name, diffs);
	}
	const dirtyStatus = recordBody(sample.json.gitStatusDirty?.body);
	if (dirtyStatus.hasChanges !== true || Number(dirtyStatus.totalFiles) < 2) {
		diffs.push(
			`semantic.${label}.json.gitStatusDirty expected dirty fixture with at least 2 changed files got ${JSON.stringify(dirtyStatus)}`,
		);
	}
	expectArrayObject(label, sample, "gitStatusDirty", "files", "path", "tracked.txt", diffs);
	expectArrayObject(label, sample, "gitStatusDirty", "files", "path", "notes.txt", diffs);
	for (const name of ["gitDiffTracked", "gitDiffUntracked"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectJSONValue(label, sample, name, "truncated", false, diffs);
		if (typeof recordBody(sample.json[name]?.body).diff !== "string") {
			diffs.push(`semantic.${label}.json.${name}.diff expected string`);
		}
	}
	expectJSONStatus(label, sample, "gitLog", 200, diffs);
	if (sampleJSONArray(sample, "gitLog").length === 0) {
		diffs.push(`semantic.${label}.json.gitLog expected at least one commit`);
	}
	for (const name of ["gitStashListEmpty", "gitStashListAfterPush"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		if (!Array.isArray(sample.json[name]?.body)) {
			diffs.push(`semantic.${label}.json.${name} expected array`);
		}
	}
	if (sampleJSONArray(sample, "gitStashListAfterPush").length === 0) {
		diffs.push(`semantic.${label}.json.gitStashListAfterPush expected one stashed item`);
	}
	for (const name of ["gitStashPush", "gitStashDrop", "gitStashPop"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectJSONValue(label, sample, name, "hasConflicts", false, diffs);
		const status = recordBody(recordBody(sample.json[name]?.body).status);
		if (typeof status.hasChanges !== "boolean" || !Array.isArray(status.files)) {
			diffs.push(`semantic.${label}.json.${name}.status expected Git status summary`);
		}
	}
	expectJSONStatus(label, sample, "gitCommit", 200, diffs);
	if (typeof recordBody(sample.json.gitCommit?.body).commitSha !== "string") {
		diffs.push(`semantic.${label}.json.gitCommit.commitSha expected string`);
	}
	const commitStatus = recordBody(recordBody(sample.json.gitCommit?.body).status);
	if (commitStatus.hasChanges !== false || !Array.isArray(commitStatus.files)) {
		diffs.push(`semantic.${label}.json.gitCommit.status expected clean Git status summary`);
	}
}

function appendSemanticDiffs(label: string, sample: BackendSample, diffs: string[]): void {
	expectSettingsSecretContracts(label, sample, diffs);
	expectGitPanelFixtureContracts(label, sample, diffs);
	expectJSONValue(label, sample, "settingsContextThresholds", "pruneStart", 95, diffs);
	expectJSONValue(label, sample, "settingsContextThresholds", "compactStart", 99, diffs);
	expectJSONStatus(label, sample, "settingsTestModelMissing", 400, diffs);
	expectJSONStatus(label, sample, "settingsRetryRuleInvalid", 400, diffs);
	expectJSONStatus(label, sample, "settingsRetryRuleValid", 201, diffs);
	expectJSONValue(label, sample, "settingsRetryRuleValid", "enabled", true, diffs);
	expectJSONValue(label, sample, "settingsRetryRuleValid", "domain", "retry.example.test", diffs);
	expectJSONValue(label, sample, "settingsRetryRuleValid", "statusCode", 503, diffs);
	expectJSONStringLength(label, sample, "settingsRetryRuleValid", "id", 8, diffs);
	expectJSONValue(label, sample, "userPreferences", "autoLoadOlderMessages", true, diffs);
	expectJSONValue(label, sample, "userPreferences", "language", "en", diffs);
	expectJSONArray(label, sample, "userPreferences", "recentTabs", diffs);
	if (!Array.isArray(sample.json.recentTabUpsert?.body)) {
		diffs.push(`semantic.${label}.json.recentTabUpsert expected array`);
	}
	expectJSONStatus(label, sample, "recentTabInvalidMissingLastVisitedAt", 400, diffs);
	expectJSONStatus(label, sample, "favorites", 200, diffs);
	if (!Array.isArray(sample.json.favorites?.body)) {
		diffs.push(`semantic.${label}.json.favorites expected array`);
	}
	expectJSONStatus(label, sample, "favoriteCreateValid", 201, diffs);
	expectJSONStringLength(label, sample, "favoriteCreateValid", "id", 21, diffs);
	expectJSONValue(label, sample, "favoriteCreateValid", "label", "Favorite One", diffs);
	for (const name of [
		"favoriteCreateDuplicate",
		"favoriteCreateInvalidMissingPath",
		"favoriteCreateInvalidNullLabel",
		"favoriteReorderInvalidMissing",
		"favoriteReorderInvalidEmpty",
	]) {
		expectJSONStatus(label, sample, name, 400, diffs);
	}
	for (const name of ["favoriteUpdateMissing", "favoriteDeleteMissing"]) {
		expectJSONStatus(label, sample, name, 404, diffs);
	}
	expectJSONStatus(label, sample, "fsShortcuts", 200, diffs);
	expectJSONArray(label, sample, "fsShortcuts", "shortcuts", diffs);
	expectJSONArray(label, sample, "fsShortcuts", "drives", diffs);
	expectJSONStatus(label, sample, "fsBrowseDefault", 200, diffs);
	expectJSONArray(label, sample, "fsBrowseDefault", "entries", diffs);
	expectJSONArray(label, sample, "fsBrowseDefault", "drives", diffs);
	expectJSONStatus(label, sample, "fsBrowseFixture", 200, diffs);
	expectJSONArray(label, sample, "fsBrowseFixture", "entries", diffs);
	expectArrayObject(label, sample, "fsBrowseFixture", "entries", "name", "visible", diffs);
	if (
		sampleJSONArray(sample, "fsBrowseFixture", "entries").some(
			(entry) => recordBody(entry).name === ".hidden",
		)
	) {
		diffs.push(`semantic.${label}.json.fsBrowseFixture.hiddenEntryVisibleByDefault`);
	}
	expectJSONStatus(label, sample, "fsBrowseFixtureShowHidden", 200, diffs);
	expectArrayObject(
		label,
		sample,
		"fsBrowseFixtureShowHidden",
		"entries",
		"name",
		".hidden",
		diffs,
	);
	for (const name of [
		"fsBrowseMissing",
		"fsMkdirInvalidMissing",
		"fsMkdirInvalidPathOnly",
		"fsMkdirInvalidSeparator",
		"fsRevealUnsupportedOrMissing",
	]) {
		expectJSONStatus(label, sample, name, 400, diffs);
	}
	expectJSONStatus(label, sample, "fsMkdirValid", 200, diffs);
	if (typeof recordBody(sample.json.fsMkdirValid?.body).path !== "string") {
		diffs.push(`semantic.${label}.json.fsMkdirValid.path expected string`);
	}
	expectJSONStatus(label, sample, "uploadNarratorImage", 200, diffs);
	expectJSONStatus(label, sample, "uploadAvatarImage", 200, diffs);
	expectJSONStatus(label, sample, "uploadMissing", 404, diffs);
	expectJSONStatus(label, sample, "shareMissing", 404, diffs);
	expectJSONStatus(label, sample, "sharePreviewMissing", 404, diffs);
	for (const name of [
		"notificationDingtalkInvalidMissing",
		"notificationFeishuInvalidMissing",
		"notificationSoundUploadMissingFile",
		"notificationSoundUploadBadMime",
	]) {
		expectJSONStatus(label, sample, name, 400, diffs);
	}
	expectJSONStatus(label, sample, "notificationSoundUploadValid", 200, diffs);
	const soundUpload = recordBody(sample.json.notificationSoundUploadValid?.body);
	if (
		typeof soundUpload.id !== "string" ||
		soundUpload.id.length !== 8 ||
		typeof soundUpload.filename !== "string" ||
		soundUpload.mediaType !== "audio/mpeg"
	) {
		diffs.push(
			`semantic.${label}.json.notificationSoundUploadValid unexpected body ${JSON.stringify(soundUpload)}`,
		);
	}
	expectJSONStatus(label, sample, "notificationSoundGetUploaded", 200, diffs);
	expectJSONStatus(label, sample, "notificationSoundDeleteUploaded", 200, diffs);
	expectJSONStatus(label, sample, "notificationSoundDeleteMissing", 404, diffs);
	for (const name of ["volumeSnapshots", "volumeSnapshotsByService"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectArrayObject(label, sample, name, undefined, "id", "vs_contract_fixture", diffs);
	}
	expectJSONStatus(label, sample, "volumeSnapshotDetail", 200, diffs);
	const snapshotDetail = recordBody(sample.json.volumeSnapshotDetail?.body);
	for (const [key, expected] of Object.entries({
		id: "vs_contract_fixture",
		projectId: "p_search_fixture",
		serviceName: "db",
		containerPath: "/var/lib/postgresql/data",
	})) {
		if (snapshotDetail[key] !== expected) {
			diffs.push(
				`semantic.${label}.json.volumeSnapshotDetail.${key} expected ${expected} got ${JSON.stringify(snapshotDetail[key])}`,
			);
		}
	}
	expectJSONStatus(label, sample, "volumeSnapshotUpdateName", 200, diffs);
	if (recordBody(sample.json.volumeSnapshotUpdateName?.body).name !== "Contract DB Snapshot") {
		diffs.push(`semantic.${label}.json.volumeSnapshotUpdateName.name expected fixture name`);
	}
	expectJSONStatus(label, sample, "volumeSnapshotApplications", 200, diffs);
	expectArrayObject(
		label,
		sample,
		"volumeSnapshotApplications",
		undefined,
		"id",
		"vsa_contract_fixture",
		diffs,
	);
	for (const name of [
		"volumeSnapshotMissing",
		"volumeSnapshotUpdateMissing",
		"volumeSnapshotDeleteMissing",
		"volumeSnapshotApplyMissing",
	]) {
		expectJSONStatus(label, sample, name, 404, diffs);
	}
	expectJSONStatus(label, sample, "browserSessionsEmpty", 200, diffs);
	if (!Array.isArray(sample.json.browserSessionsEmpty?.body)) {
		diffs.push(`semantic.${label}.json.browserSessionsEmpty expected array`);
	}
	for (const name of ["rulerSegmentSummary", "rulerSegmentFull"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		const chapter = expectArrayObject(
			label,
			sample,
			name,
			"chapters",
			"id",
			"c_search_fixture",
			diffs,
		);
		if (chapter && recordBody(chapter).narratorStatus !== "unread") {
			diffs.push(
				`semantic.${label}.json.${name}.c_search_fixture.narratorStatus expected unread got ${JSON.stringify(recordBody(chapter).narratorStatus)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "graphPositionsInvalidMissingAxis", 400, diffs);
	expectJSONStatus(label, sample, "graphPositionsValid", 200, diffs);
	if (recordBody(sample.json.graphPositionsValid?.body).ok !== true) {
		diffs.push(`semantic.${label}.json.graphPositionsValid.ok expected true`);
	}
	expectJSONStatus(label, sample, "rulerPositionsInvalidNegativeCross", 400, diffs);
	expectJSONStatus(label, sample, "rulerPositionsValid", 200, diffs);
	if (recordBody(sample.json.rulerPositionsValid?.body).success !== true) {
		diffs.push(`semantic.${label}.json.rulerPositionsValid.success expected true`);
	}
	expectJSONStatus(label, sample, "rulerSegmentAfterPositions", 200, diffs);
	const graphPositionChapter = expectArrayObject(
		label,
		sample,
		"rulerSegmentAfterPositions",
		"chapters",
		"id",
		"c_search_fixture",
		diffs,
	);
	const rulerPositionChapter = expectArrayObject(
		label,
		sample,
		"rulerSegmentAfterPositions",
		"chapters",
		"id",
		"c_graph_target",
		diffs,
	);
	if (graphPositionChapter) {
		const chapter = recordBody(graphPositionChapter);
		for (const [key, expected] of Object.entries({ axisOffset: 77, crossOffset: 88 })) {
			if (chapter[key] !== expected) {
				diffs.push(
					`semantic.${label}.json.rulerSegmentAfterPositions.c_search_fixture.${key} expected ${expected} got ${JSON.stringify(chapter[key])}`,
				);
			}
		}
	}
	if (rulerPositionChapter) {
		const chapter = recordBody(rulerPositionChapter);
		for (const [key, expected] of Object.entries({
			axisOffset: 44,
			crossOffset: 55,
			panelWidth: 444,
			panelHeight: 333,
		})) {
			if (chapter[key] !== expected) {
				diffs.push(
					`semantic.${label}.json.rulerSegmentAfterPositions.c_graph_target.${key} expected ${expected} got ${JSON.stringify(chapter[key])}`,
				);
			}
		}
	}
	expectJSONStatus(label, sample, "projectGraph", 200, diffs);
	for (const key of ["nodes", "edges", "explorationGroups", "openedTerminals"]) {
		expectJSONArray(label, sample, "projectGraph", key, diffs);
	}
	const graphNode = expectArrayObject(
		label,
		sample,
		"projectGraph",
		"nodes",
		"id",
		"c_search_fixture",
		diffs,
	);
	if (graphNode) {
		const data = recordBody(graphNode.data);
		if (data.title !== "叙事分叉 Alpha Chapter" || data.role !== "branch") {
			diffs.push(`semantic.${label}.json.projectGraph.node.c_search_fixture unexpected data`);
		}
	}
	const graphEdge = expectArrayObject(
		label,
		sample,
		"projectGraph",
		"edges",
		"id",
		"e_graph_dependency",
		diffs,
	);
	if (graphEdge) {
		for (const [key, expected] of Object.entries({
			source: "c_search_fixture",
			target: "c_graph_target",
			type: "dependency",
		})) {
			if (graphEdge[key] !== expected) {
				diffs.push(
					`semantic.${label}.json.projectGraph.edge.e_graph_dependency.${key} expected ${expected} got ${JSON.stringify(graphEdge[key])}`,
				);
			}
		}
	}
	expectArrayObject(
		label,
		sample,
		"projectGraph",
		"explorationGroups",
		"id",
		"eg_graph_fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "chapterCreateFixtureStateBefore", 200, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateBefore", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateBefore", "chapterRows", 0, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateBefore", "repoExists", true, diffs);
	expectJSONStatus(label, sample, "chapterCreateFixture", 201, diffs);
	expectJSONValue(label, sample, "chapterCreateFixture", "idIsString", true, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterCreateFixture",
		"projectId",
		"p_chapter_create_fixture",
		diffs,
	);
	expectJSONValue(label, sample, "chapterCreateFixture", "title", "Chapter Create Fixture", diffs);
	expectJSONValue(label, sample, "chapterCreateFixture", "status", "active", diffs);
	expectJSONValue(label, sample, "chapterCreateFixture", "role", "branch", diffs);
	expectJSONValue(label, sample, "chapterCreateFixture", "branchHasExpectedPrefix", true, diffs);
	expectJSONValue(label, sample, "chapterCreateFixture", "worktreePathInFixtureRepo", true, diffs);
	expectJSONStatus(label, sample, "chapterCreateFixtureStateAfter", 200, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "chapterRows", 1, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "narratorRows", 1, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "commitRows", 2, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "manualCommitRows", 2, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "branchExists", true, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterCreateFixtureStateAfter",
		"branchHasExpectedPrefix",
		true,
		diffs,
	);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "worktreeExists", true, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterCreateFixtureStateAfter",
		"worktreePathInRepo",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterCreateFixtureStateAfter",
		"worktreeHasTrackedFiles",
		true,
		diffs,
	);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "baseBranch", "main", diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "status", "active", diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "role", "branch", diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "isRoot", 0, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "headMatchesRepo", true, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "commitCount", 2, diffs);
	expectJSONValue(label, sample, "chapterCreateFixtureStateAfter", "narratorCwdNull", true, diffs);
	expectJSONStatus(label, sample, "projectGraphAfterChapterCreate", 200, diffs);
	const createdGraphNode = sampleJSONArray(sample, "projectGraphAfterChapterCreate", "nodes").find(
		(node) => recordBody(recordBody(node).data).title === "Chapter Create Fixture",
	);
	if (!createdGraphNode) {
		diffs.push(`semantic.${label}.json.projectGraphAfterChapterCreate.createdNodeMissing`);
	} else if (recordBody(recordBody(createdGraphNode).data).role !== "branch") {
		diffs.push(`semantic.${label}.json.projectGraphAfterChapterCreate.createdNodeUnexpectedRole`);
	}
	expectJSONStatus(label, sample, "chapterDormantWakeFixtureStateBefore", 200, diffs);
	expectJSONValue(label, sample, "chapterDormantWakeFixtureStateBefore", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "chapterDormantWakeFixtureStateBefore", "chapterRows", 1, diffs);
	expectJSONValue(label, sample, "chapterDormantWakeFixtureStateBefore", "status", "active", diffs);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateBefore",
		"worktreeExists",
		true,
		diffs,
	);
	expectJSONValue(label, sample, "chapterDormantWakeFixtureStateBefore", "commitRows", 0, diffs);
	expectJSONStatus(label, sample, "chapterDormantWakeDormant", 200, diffs);
	expectJSONValue(label, sample, "chapterDormantWakeDormant", "ok", true, diffs);
	expectJSONStatus(label, sample, "chapterDormantWakeFixtureStateAfterDormant", 200, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterDormant",
		"status",
		"dormant",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterDormant",
		"worktreePathNull",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterDormant",
		"knownWorktreeExists",
		false,
		diffs,
	);
	expectJSONStatus(label, sample, "chapterDormantWakeWake", 200, diffs);
	expectJSONValue(label, sample, "chapterDormantWakeWake", "ok", true, diffs);
	expectJSONStatus(label, sample, "chapterDormantWakeFixtureStateAfterWake", 200, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterWake",
		"status",
		"active",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterWake",
		"worktreeExists",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterWake",
		"headMatchesBranch",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterWake",
		"commitCount",
		2,
		diffs,
	);
	expectJSONValue(label, sample, "chapterDormantWakeFixtureStateAfterWake", "commitRows", 2, diffs);
	expectJSONValue(
		label,
		sample,
		"chapterDormantWakeFixtureStateAfterWake",
		"manualCommitRows",
		2,
		diffs,
	);
	expectJSONStatus(label, sample, "projectCreateFixtureStateBefore", 200, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateBefore", "projectRows", 0, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateBefore", "rootChapterRows", 0, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateBefore", "repoExists", true, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateBefore", "backupDbExists", false, diffs);
	expectJSONStatus(label, sample, "projectCreateFixture", 201, diffs);
	expectJSONStatus(label, sample, "projectCreateFixtureStateAfter", 200, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateAfter", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateAfter", "rootChapterRows", 1, diffs);
	expectJSONValue(label, sample, "projectCreateFixtureStateAfter", "rootCommitRows", 2, diffs);
	expectJSONValue(
		label,
		sample,
		"projectCreateFixtureStateAfter",
		"manualRootCommitRows",
		2,
		diffs,
	);
	expectJSONValue(label, sample, "projectCreateFixtureStateAfter", "backupDbExists", true, diffs);
	expectJSONValue(
		label,
		sample,
		"projectCreateFixtureStateAfter",
		"gitignoreHasNarrafork",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"projectCreateFixtureStateAfter",
		"gitignoreHasWorktrees",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"projectCreateFixtureStateAfter",
		"gitPathMatchesFixture",
		true,
		diffs,
	);
	expectJSONValue(label, sample, "projectCreateFixtureStateAfter", "defaultBranch", "main", diffs);
	expectJSONStatus(label, sample, "projectsAfterProjectCreate", 200, diffs);
	if (
		!sampleJSONArray(sample, "projectsAfterProjectCreate", undefined).some(
			(project) => recordBody(project).name === "Project Create Fixture",
		)
	) {
		diffs.push(`semantic.${label}.json.projectsAfterProjectCreate.createdProjectMissing`);
	}
	expectJSONStatus(label, sample, "projectRealCleanupWarningStateBefore", 200, diffs);
	expectJSONValue(label, sample, "projectRealCleanupWarningStateBefore", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "projectRealCleanupWarningStateBefore", "chapterRows", 2, diffs);
	expectJSONValue(
		label,
		sample,
		"projectRealCleanupWarningStateBefore",
		"rootRepoExists",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"projectRealCleanupWarningStateBefore",
		"staleWorktreeExists",
		true,
		diffs,
	);
	expectJSONStatus(label, sample, "projectRealCleanupWarning", 200, diffs);
	expectJSONValue(label, sample, "projectRealCleanupWarning", "ok", true, diffs);
	if (label === "go") {
		const cleanupError = expectArrayObject(
			label,
			sample,
			"projectRealCleanupWarning",
			"cleanupErrors",
			"scope",
			"worktree",
			diffs,
		);
		if (cleanupError && cleanupError.id !== "c_real_cleanup_branch_fixture") {
			diffs.push(
				`semantic.${label}.json.projectRealCleanupWarning.cleanupErrors.worktree.id expected c_real_cleanup_branch_fixture got ${JSON.stringify(cleanupError.id)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "projectRealCleanupWarningStateAfter", 200, diffs);
	expectJSONValue(label, sample, "projectRealCleanupWarningStateAfter", "projectRows", 0, diffs);
	expectJSONValue(label, sample, "projectRealCleanupWarningStateAfter", "chapterRows", 0, diffs);
	expectJSONValue(
		label,
		sample,
		"projectRealCleanupWarningStateAfter",
		"rootRepoExists",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"projectRealCleanupWarningStateAfter",
		"staleWorktreeExists",
		true,
		diffs,
	);
	expectJSONStatus(label, sample, "projectDeleteFixtureStateBefore", 200, diffs);
	expectJSONValue(label, sample, "projectDeleteFixtureStateBefore", "projectRows", 1, diffs);
	expectJSONValue(label, sample, "projectDeleteFixtureStateBefore", "chapterRows", 2, diffs);
	expectJSONValue(
		label,
		sample,
		"projectDeleteFixtureStateBefore",
		"recentTabsContainDeletedProject",
		true,
		diffs,
	);
	expectJSONValue(label, sample, "projectDeleteFixtureStateBefore", "rootRepoExists", true, diffs);
	expectJSONValue(
		label,
		sample,
		"projectDeleteFixtureStateBefore",
		"branchWorktreeExists",
		true,
		diffs,
	);
	expectJSONStatus(label, sample, "projectDeleteFixture", 200, diffs);
	expectJSONValue(label, sample, "projectDeleteFixture", "ok", true, diffs);
	expectJSONStatus(label, sample, "projectDeleteFixtureGetAfter", 404, diffs);
	expectJSONStatus(label, sample, "projectsAfterProjectDelete", 200, diffs);
	if (
		sampleJSONArray(sample, "projectsAfterProjectDelete", undefined).some(
			(project) => recordBody(project).id === "p_delete_fixture",
		)
	) {
		diffs.push(`semantic.${label}.json.projectsAfterProjectDelete.deletedProjectStillPresent`);
	}
	expectJSONStatus(label, sample, "projectDeleteFixtureStateAfter", 200, diffs);
	expectJSONValue(label, sample, "projectDeleteFixtureStateAfter", "projectRows", 0, diffs);
	expectJSONValue(label, sample, "projectDeleteFixtureStateAfter", "chapterRows", 0, diffs);
	expectJSONValue(
		label,
		sample,
		"projectDeleteFixtureStateAfter",
		"recentTabsContainDeletedProject",
		false,
		diffs,
	);
	expectJSONValue(label, sample, "projectDeleteFixtureStateAfter", "rootRepoExists", true, diffs);
	expectJSONValue(
		label,
		sample,
		"projectDeleteFixtureStateAfter",
		"branchWorktreeExists",
		false,
		diffs,
	);
	expectArrayObject(label, sample, "projectGraph", "nodes", "id", "c_delete_fixture", diffs);
	expectJSONStatus(label, sample, "chapterDeleteFixture", 200, diffs);
	expectJSONValue(label, sample, "chapterDeleteFixture", "ok", true, diffs);
	expectJSONStatus(label, sample, "chapterDeleteFixtureGetAfter", 404, diffs);
	expectJSONStatus(label, sample, "projectGraphAfterChapterDelete", 200, diffs);
	for (const key of ["nodes", "edges", "explorationGroups", "openedTerminals"]) {
		expectJSONArray(label, sample, "projectGraphAfterChapterDelete", key, diffs);
	}
	if (
		sampleJSONArray(sample, "projectGraphAfterChapterDelete", "nodes").some(
			(node) => recordBody(node).id === "c_delete_fixture",
		)
	) {
		diffs.push(`semantic.${label}.json.projectGraphAfterChapterDelete.deletedNodeStillPresent`);
	}
	for (const name of ["chapterEdgesProject", "chapterEdgesDependency", "chapterEdgesByChapter"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectArrayObject(label, sample, name, undefined, "id", "e_graph_dependency", diffs);
	}
	expectJSONStatus(label, sample, "dependencyEdgeCreateLifecycle", 201, diffs);
	const lifecycleEdge = recordBody(sample.json.dependencyEdgeCreateLifecycle?.body);
	const lifecycleEdgeId = lifecycleEdge.id;
	if (typeof lifecycleEdgeId !== "string" || lifecycleEdgeId.length !== 21) {
		diffs.push(
			`semantic.${label}.json.dependencyEdgeCreateLifecycle.id expected 21-char string got ${JSON.stringify(lifecycleEdgeId)}`,
		);
	}
	for (const [key, expected] of Object.entries({
		projectId: "p_search_fixture",
		sourceId: "c_dep_lifecycle_source",
		targetId: "c_dep_lifecycle_target",
		type: "dependency",
	})) {
		if (lifecycleEdge[key] !== expected) {
			diffs.push(
				`semantic.${label}.json.dependencyEdgeCreateLifecycle.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(lifecycleEdge[key])}`,
			);
		}
	}
	const lifecycleMetadata = recordBody(lifecycleEdge.metadata);
	if (
		lifecycleMetadata.description !== "Contract lifecycle dependency" ||
		lifecycleMetadata.lastSyncedCommit !== null
	) {
		diffs.push(
			`semantic.${label}.json.dependencyEdgeCreateLifecycle.metadata unexpected ${JSON.stringify(lifecycleMetadata)}`,
		);
	}
	expectJSONStatus(label, sample, "chapterEdgesAfterLifecycleCreate", 200, diffs);
	if (typeof lifecycleEdgeId === "string") {
		expectArrayObject(
			label,
			sample,
			"chapterEdgesAfterLifecycleCreate",
			undefined,
			"id",
			lifecycleEdgeId,
			diffs,
		);
	}
	expectJSONStatus(label, sample, "projectGraphAfterDependencyCreate", 200, diffs);
	if (typeof lifecycleEdgeId === "string") {
		const graphLifecycleEdge = expectArrayObject(
			label,
			sample,
			"projectGraphAfterDependencyCreate",
			"edges",
			"id",
			lifecycleEdgeId,
			diffs,
		);
		if (graphLifecycleEdge) {
			for (const [key, expected] of Object.entries({
				source: "c_dep_lifecycle_source",
				target: "c_dep_lifecycle_target",
				type: "dependency",
			})) {
				if (graphLifecycleEdge[key] !== expected) {
					diffs.push(
						`semantic.${label}.json.projectGraphAfterDependencyCreate.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(graphLifecycleEdge[key])}`,
					);
				}
			}
		}
	}
	expectJSONStatus(label, sample, "dependencyEdgeDeleteLifecycle", 200, diffs);
	expectJSONValue(label, sample, "dependencyEdgeDeleteLifecycle", "ok", true, diffs);
	expectJSONStatus(label, sample, "chapterEdgesAfterLifecycleDelete", 200, diffs);
	if (
		typeof lifecycleEdgeId === "string" &&
		sampleJSONArray(sample, "chapterEdgesAfterLifecycleDelete").some(
			(edge) => recordBody(edge).id === lifecycleEdgeId,
		)
	) {
		diffs.push(`semantic.${label}.json.chapterEdgesAfterLifecycleDelete.deletedEdgeStillPresent`);
	}
	expectJSONStatus(label, sample, "projectGraphAfterDependencyDelete", 200, diffs);
	if (
		typeof lifecycleEdgeId === "string" &&
		sampleJSONArray(sample, "projectGraphAfterDependencyDelete", "edges").some(
			(edge) => recordBody(edge).id === lifecycleEdgeId,
		)
	) {
		diffs.push(`semantic.${label}.json.projectGraphAfterDependencyDelete.deletedEdgeStillPresent`);
	}
	expectJSONStatus(label, sample, "chapterEdgesMissingScope", 400, diffs);
	expectJSONStatus(label, sample, "chapterEdgesInvalidTypeReview", 400, diffs);
	expectJSONStatus(label, sample, "chapterEdgeDeleteMissing", 404, diffs);
	expectSearchTypes(label, sample, "searchCJK", ["chapter", "message", "narrator"], diffs);
	expectSearchTypes(label, sample, "searchShortChapter", ["chapter"], diffs);
	expectSearchTypes(label, sample, "searchLargeTopK", ["chapter", "message", "narrator"], diffs);
	expectSearchTypes(label, sample, "searchLargeTieBreak", ["chapter"], diffs);
	expectSearchFixtureContracts(label, sample, diffs);
	expectJSONStatus(label, sample, "routines", 200, diffs);
	expectJSONArray(label, sample, "routines", "routines", diffs);
	const terminalRoutine = expectArrayObject(
		label,
		sample,
		"routines",
		"routines",
		"id",
		"terminal",
		diffs,
	);
	if (terminalRoutine && terminalRoutine.name !== "Terminal") {
		diffs.push(
			`semantic.${label}.json.routines.terminal.name expected Terminal got ${JSON.stringify(terminalRoutine.name)}`,
		);
	}
	const expectProjectRoutineState = (
		name: string,
		enabled: boolean,
		override: "global" | "enabled" | "disabled",
	) => {
		expectJSONStatus(label, sample, name, 200, diffs);
		const routine = expectArrayObject(label, sample, name, "routines", "id", "terminal", diffs);
		if (!routine) return;
		for (const [key, expected] of Object.entries({
			enabled,
			override,
			globalEnabled: false,
		})) {
			if (routine[key] !== expected) {
				diffs.push(
					`semantic.${label}.json.${name}.terminal.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(routine[key])}`,
				);
			}
		}
	};
	expectProjectRoutineState("projectRoutinesInitial", false, "global");
	expectJSONStatus(label, sample, "projectRoutineInvalidAction", 400, diffs);
	expectJSONStatus(label, sample, "projectRoutineMissingRoutine", 404, diffs);
	expectJSONStatus(label, sample, "projectRoutineMissingProject", 404, diffs);
	expectJSONStatus(label, sample, "projectRoutineEnableTerminal", 200, diffs);
	expectJSONValue(label, sample, "projectRoutineEnableTerminal", "ok", true, diffs);
	expectProjectRoutineState("projectRoutinesAfterEnable", true, "enabled");
	expectJSONStatus(label, sample, "projectRoutineDisableTerminal", 200, diffs);
	expectJSONValue(label, sample, "projectRoutineDisableTerminal", "ok", true, diffs);
	expectProjectRoutineState("projectRoutinesAfterDisable", false, "disabled");
	expectJSONStatus(label, sample, "projectRoutineResetTerminal", 200, diffs);
	expectJSONValue(label, sample, "projectRoutineResetTerminal", "ok", true, diffs);
	expectProjectRoutineState("projectRoutinesAfterReset", false, "global");
	expectJSONStatus(label, sample, "routinesGlobalPrompt", 200, diffs);
	expectJSONArray(label, sample, "routinesGlobalPrompt", "candidates", diffs);
	for (const name of ["skillsGlobal", "skillsProject"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		if (!Array.isArray(sample.json[name]?.body)) {
			diffs.push(`semantic.${label}.json.${name} expected array`);
		}
	}
	expectArrayObject(label, sample, "skillsGlobal", undefined, "name", "global-contract", diffs);
	expectJSONStatus(label, sample, "globalSkillCreateValid", 201, diffs);
	expectJSONValue(label, sample, "globalSkillCreateValid", "name", "Contract Skill", diffs);
	expectJSONValue(
		label,
		sample,
		"globalSkillCreateValid",
		"description",
		"Helps contract parity",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"globalSkillDetailCreated",
		"content",
		"Initial global skill content.",
		diffs,
	);
	expectJSONStatus(label, sample, "globalSkillCreateDuplicate", 400, diffs);
	expectJSONStatus(label, sample, "globalSkillToggleInvalidMissingEnabled", 400, diffs);
	expectJSONStatus(label, sample, "globalSkillToggleDisable", 200, diffs);
	expectJSONValue(label, sample, "globalSkillToggleDisable", "disabled", true, diffs);
	expectJSONStatus(label, sample, "globalSkillToggleEnable", 200, diffs);
	expectJSONValue(label, sample, "globalSkillToggleEnable", "disabled", false, diffs);
	expectJSONStatus(label, sample, "globalSkillUpdateValid", 200, diffs);
	expectJSONValue(label, sample, "globalSkillUpdateValid", "name", "Contract Skill Updated", diffs);
	expectJSONValue(
		label,
		sample,
		"globalSkillUpdateValid",
		"description",
		"Updated contract parity skill",
		diffs,
	);
	expectJSONStatus(label, sample, "globalSkillOldDetailAfterRename", 404, diffs);
	expectJSONStatus(label, sample, "globalSkillDetailUpdated", 200, diffs);
	expectJSONValue(
		label,
		sample,
		"globalSkillDetailUpdated",
		"content",
		"Updated global skill content.",
		diffs,
	);
	expectJSONStatus(label, sample, "globalSkillListAfterUpdate", 200, diffs);
	expectArrayObject(
		label,
		sample,
		"globalSkillListAfterUpdate",
		undefined,
		"name",
		"Contract Skill Updated",
		diffs,
	);
	expectJSONStatus(label, sample, "globalSkillDeleteValid", 200, diffs);
	expectJSONValue(label, sample, "globalSkillDeleteValid", "ok", true, diffs);
	expectJSONStatus(label, sample, "globalSkillDetailAfterDelete", 404, diffs);
	expectJSONStatus(label, sample, "globalSkillCreateInvalidMissingDescription", 400, diffs);
	expectJSONStatus(label, sample, "globalSkillUpdateMissing", 404, diffs);
	expectJSONStatus(label, sample, "globalSkillDeleteMissing", 404, diffs);
	expectJSONStatus(label, sample, "globalSkillToggleMissing", 404, diffs);
	expectArrayObject(label, sample, "skillsProject", undefined, "name", "project-contract", diffs);
	expectJSONStatus(label, sample, "skillProjectDetail", 200, diffs);
	expectJSONValue(label, sample, "skillProjectDetail", "name", "project-contract", diffs);
	expectJSONStatus(label, sample, "mcpServers", 200, diffs);
	expectJSONArray(label, sample, "mcpServers", "servers", diffs);
	expectJSONStatus(label, sample, "mcpTools", 200, diffs);
	expectJSONArray(label, sample, "mcpTools", "tools", diffs);
	for (const name of [
		"mcpCreateInvalidArgs",
		"mcpCreateInvalidToolPermissions",
		"mcpTestInvalidBody",
	]) {
		expectJSONStatus(label, sample, name, 400, diffs);
	}
	expectJSONStatus(label, sample, "mcpCreateDisabled", 201, diffs);
	expectJSONStringLength(label, sample, "mcpCreateDisabled", "id", 8, diffs);
	expectJSONValue(label, sample, "mcpCreateDisabled", "name", "contract-tools", diffs);
	expectJSONValue(label, sample, "mcpCreateDisabled", "enabled", false, diffs);
	expectJSONValue(label, sample, "mcpCreateDisabled", "defaultBehavior", "readWrite", diffs);
	expectJSONStatus(label, sample, "mcpTestStdioValid", 200, diffs);
	expectJSONValue(label, sample, "mcpTestStdioValid", "ok", true, diffs);
	expectArrayObject(label, sample, "mcpTestStdioValid", "tools", "name", "read_file", diffs);
	expectJSONStatus(label, sample, "mcpCreateStdioEnabled", 201, diffs);
	expectJSONStringLength(label, sample, "mcpCreateStdioEnabled", "id", 8, diffs);
	expectJSONValue(label, sample, "mcpCreateStdioEnabled", "name", "contract-mcp-live", diffs);
	expectJSONValue(label, sample, "mcpCreateStdioEnabled", "enabled", true, diffs);
	expectJSONValue(label, sample, "mcpCreateStdioEnabled", "defaultBehavior", "readWrite", diffs);
	const mcpStdioId = recordBody(sample.json.mcpCreateStdioEnabled?.body).id;
	if (typeof mcpStdioId !== "string" || mcpStdioId.length !== 8) {
		diffs.push(
			`semantic.${label}.json.mcpCreateStdioEnabled.id expected 8-char string got ${JSON.stringify(mcpStdioId)}`,
		);
	}
	expectJSONStatus(label, sample, "mcpServersAfterStdioCreate", 200, diffs);
	if (typeof mcpStdioId === "string") {
		const server = expectArrayObject(
			label,
			sample,
			"mcpServersAfterStdioCreate",
			"servers",
			"id",
			mcpStdioId,
			diffs,
		);
		if (server) {
			for (const [key, expected] of Object.entries({
				name: "contract-mcp-live",
				status: "connected",
				enabled: true,
			})) {
				if (server[key] !== expected) {
					diffs.push(
						`semantic.${label}.json.mcpServersAfterStdioCreate.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(server[key])}`,
					);
				}
			}
			const tools = Array.isArray(server.tools) ? server.tools : [];
			if (!tools.some((tool) => recordBody(tool).name === "read_file")) {
				diffs.push(`semantic.${label}.json.mcpServersAfterStdioCreate.tools missing read_file`);
			}
		}
	}
	expectJSONStatus(label, sample, "mcpToolsAfterStdioCreate", 200, diffs);
	const mcpTool = expectArrayObject(
		label,
		sample,
		"mcpToolsAfterStdioCreate",
		"tools",
		"name",
		"read_file",
		diffs,
	);
	if (mcpTool) {
		for (const [key, expected] of Object.entries({
			serverName: "contract-mcp-live",
			source: "external",
		})) {
			if (mcpTool[key] !== expected) {
				diffs.push(
					`semantic.${label}.json.mcpToolsAfterStdioCreate.read_file.${key} expected ${JSON.stringify(expected)} got ${JSON.stringify(mcpTool[key])}`,
				);
			}
		}
		if (typeof mcpStdioId === "string" && mcpTool.serverId !== mcpStdioId) {
			diffs.push(
				`semantic.${label}.json.mcpToolsAfterStdioCreate.read_file.serverId expected ${mcpStdioId} got ${JSON.stringify(mcpTool.serverId)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "mcpDeleteStdioValid", 200, diffs);
	expectJSONValue(label, sample, "mcpDeleteStdioValid", "ok", true, diffs);
	expectJSONStatus(label, sample, "customSubagentCreateValid", 201, diffs);
	for (const [name, expectedName] of [
		["customSubagentCreateValid", "Contract Helper"],
		["customSubagentDetailCreated", "Contract Helper"],
		["customSubagentUpdateValid", "Contract Audit"],
		["customSubagentDetailUpdated", "Contract Audit"],
	] as const) {
		expectJSONStatus(label, sample, name, name === "customSubagentCreateValid" ? 201 : 200, diffs);
		expectJSONValue(label, sample, name, "name", expectedName, diffs);
	}
	expectJSONValue(
		label,
		sample,
		"customSubagentCreateValid",
		"description",
		"Helps contract parity",
		diffs,
	);
	expectJSONValue(label, sample, "customSubagentCreateValid", "toolAccess", "custom", diffs);
	expectJSONArray(label, sample, "customSubagentCreateValid", "customTools", diffs);
	if (
		sampleJSONArray(sample, "customSubagentCreateValid", "customTools").join(",") !== "Read,Grep"
	) {
		diffs.push(`semantic.${label}.json.customSubagentCreateValid.customTools unexpected`);
	}
	expectJSONValue(
		label,
		sample,
		"customSubagentCreateValid",
		"defaultModel",
		"claude-sonnet-4.5",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"customSubagentCreateValid",
		"prompt",
		"Initial custom subagent prompt.",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"customSubagentDetailUpdated",
		"description",
		"Audits contract parity",
		diffs,
	);
	expectJSONValue(label, sample, "customSubagentDetailUpdated", "toolAccess", "general", diffs);
	expectJSONValue(
		label,
		sample,
		"customSubagentDetailUpdated",
		"defaultModel",
		"claude-haiku-4.5",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"customSubagentDetailUpdated",
		"prompt",
		"Updated custom subagent prompt.",
		diffs,
	);
	if (sampleJSONArray(sample, "customSubagentDetailUpdated", "customTools").length !== 0) {
		diffs.push(`semantic.${label}.json.customSubagentDetailUpdated.customTools expected empty`);
	}
	expectJSONStatus(label, sample, "customSubagentOldDetailAfterRename", 404, diffs);
	expectJSONStatus(label, sample, "customSubagentListAfterUpdate", 200, diffs);
	expectArrayObject(
		label,
		sample,
		"customSubagentListAfterUpdate",
		undefined,
		"name",
		"Contract Audit",
		diffs,
	);
	expectJSONStatus(label, sample, "customSubagentDeleteValid", 200, diffs);
	expectJSONValue(label, sample, "customSubagentDeleteValid", "ok", true, diffs);
	expectJSONStatus(label, sample, "customSubagentDetailAfterDelete", 404, diffs);
	for (const name of ["customSubagents", "workspaces", "hooks", "hooksAll"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		if (!Array.isArray(sample.json[name]?.body)) {
			diffs.push(`semantic.${label}.json.${name} expected array`);
		}
	}
	expectJSONStatus(label, sample, "workspaceCreateValid", 201, diffs);
	expectJSONStringLength(label, sample, "workspaceCreateValid", "id", 21, diffs);
	expectJSONValue(label, sample, "workspaceCreateValid", "title", "Contract Workspace", diffs);
	expectJSONValue(
		label,
		sample,
		"workspaceCreateValid",
		"tree",
		JSON.stringify({ type: "leaf", narratorId: "n_search_fixture" }),
		diffs,
	);
	for (const name of ["workspaceDetailCreated", "workspaceDetailUpdated"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
	}
	expectJSONStatus(label, sample, "workspaceUpdateValid", 200, diffs);
	expectJSONValue(label, sample, "workspaceUpdateValid", "ok", true, diffs);
	expectJSONValue(
		label,
		sample,
		"workspaceDetailUpdated",
		"title",
		"Contract Workspace Updated",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"workspaceDetailUpdated",
		"tree",
		JSON.stringify({ type: "leaf", narratorId: "n_search_fixture", presentation: "updated" }),
		diffs,
	);
	expectJSONStatus(label, sample, "workspaceListAfterUpdate", 200, diffs);
	expectArrayObject(
		label,
		sample,
		"workspaceListAfterUpdate",
		undefined,
		"title",
		"Contract Workspace Updated",
		diffs,
	);
	expectJSONStatus(label, sample, "workspaceDeleteValid", 200, diffs);
	expectJSONValue(label, sample, "workspaceDeleteValid", "ok", true, diffs);
	expectJSONStatus(label, sample, "workspaceDetailAfterDelete", 404, diffs);
	expectJSONStatus(label, sample, "hookCreateValid", 201, diffs);
	expectJSONValue(label, sample, "hookCreateValid", "event", "PreToolUse", diffs);
	expectJSONValue(label, sample, "hookCreateValid", "matcher", "Read", diffs);
	expectJSONValue(label, sample, "hookCreateValid", "type", "command", diffs);
	expectJSONValue(label, sample, "hookCreateValid", "command", "true", diffs);
	expectJSONValue(label, sample, "hookCreateValid", "timeout", 5, diffs);
	expectJSONValue(label, sample, "hookCreateValid", "enabled", true, diffs);
	expectJSONValue(label, sample, "hookCreateValid", "sortOrder", 2, diffs);
	const hookId = recordBody(sample.json.hookCreateValid?.body).id;
	if (typeof hookId !== "string" || hookId.length === 0) {
		diffs.push(`semantic.${label}.json.hookCreateValid.id expected non-empty string`);
	}
	expectJSONStatus(label, sample, "hookDetailCreated", 200, diffs);
	expectJSONStatus(label, sample, "hookUpdateValid", 200, diffs);
	expectJSONValue(label, sample, "hookUpdateValid", "matcher", "", diffs);
	expectJSONValue(label, sample, "hookUpdateValid", "type", "http", diffs);
	expectJSONValue(label, sample, "hookUpdateValid", "url", "https://example.com/hook", diffs);
	expectJSONValue(label, sample, "hookUpdateValid", "timeout", 10, diffs);
	expectJSONValue(label, sample, "hookUpdateValid", "enabled", false, diffs);
	const hookHeaders = recordBody(recordBody(sample.json.hookUpdateValid?.body).headers);
	if (hookHeaders["X-Test"] !== "yes") {
		diffs.push(
			`semantic.${label}.json.hookUpdateValid.headers.X-Test expected yes got ${JSON.stringify(hookHeaders["X-Test"])}`,
		);
	}
	expectJSONStatus(label, sample, "hookDetailUpdated", 200, diffs);
	expectJSONStatus(label, sample, "hookListAfterUpdate", 200, diffs);
	expectJSONStatus(label, sample, "hookAllAfterUpdate", 200, diffs);
	if (typeof hookId === "string") {
		expectArrayObject(label, sample, "hookListAfterUpdate", undefined, "id", hookId, diffs);
		expectArrayObject(label, sample, "hookAllAfterUpdate", undefined, "id", hookId, diffs);
	}
	expectJSONStatus(label, sample, "hookUpdateClearActiveURLInvalid", 400, diffs);
	expectJSONStatus(label, sample, "hookUpdateMissingInvalidBody", 404, diffs);
	expectJSONStatus(label, sample, "hookDeleteValid", 200, diffs);
	expectJSONValue(label, sample, "hookDeleteValid", "ok", true, diffs);
	expectJSONStatus(label, sample, "hookDetailAfterDelete", 404, diffs);
	for (const name of [
		"customSubagentMissing",
		"customSubagentDeleteMissing",
		"workspaceMissing",
		"workspaceUpdateMissing",
		"workspaceDeleteMissing",
		"hookMissing",
		"hookDeleteMissing",
	]) {
		expectJSONStatus(label, sample, name, 404, diffs);
	}
	for (const name of ["customSubagentCreateInvalid", "workspaceCreateInvalidShortTree"]) {
		expectJSONStatus(label, sample, name, 400, diffs);
	}
	expectJSONStatus(label, sample, "userHooks", 403, diffs);
	for (const name of [
		"unauthProjects",
		"unauthSettings",
		"unauthStorageCached",
		"unauthRuntimeScan",
		"unauthBenchmarkSuites",
	]) {
		expectJSONStatus(label, sample, name, 401, diffs);
	}
	expectJSONStatus(label, sample, "benchmarkCompareMissingParam", 400, diffs);
	expectJSONStatus(label, sample, "benchmarkMissingSummary", 404, diffs);
	if (!Array.isArray(sample.json.benchmarkMissingResults?.body)) {
		diffs.push(`semantic.${label}.json.benchmarkMissingResults expected array`);
	}
	expectJSONStatus(label, sample, "userBenchmarkSuites", 403, diffs);
	expectJSONStatus(label, sample, "userBenchmarkCompare", 403, diffs);
	expectJSONValue(label, sample, "clineStatus", "authenticated", false, diffs);
	expectJSONStatus(label, sample, "clineBalance", 422, diffs);
	for (const name of ["clineRecommendedModelsRefresh", "clineRecommendedModelsCached"]) {
		expectJSONStatus(label, sample, name, 200, diffs);
		const recommended = recordBody(sample.json[name].body).recommended;
		const free = recordBody(sample.json[name].body).free;
		if (!Array.isArray(recommended) || !Array.isArray(free)) {
			diffs.push(
				`semantic.${label}.json.${name} expected recommended/free arrays got ${JSON.stringify(sample.json[name].body)}`,
			);
			continue;
		}
		const recommendedIds = recommended.map((model) => recordBody(model).id).join(",");
		const freeIds = free.map((model) => recordBody(model).id).join(",");
		if (recommendedIds !== "cline/recommended-fixture") {
			diffs.push(
				`semantic.${label}.json.${name}.recommended expected fixture id got ${recommendedIds}`,
			);
		}
		if (freeIds !== "cline/free-fixture,cline/free-live-fixture") {
			diffs.push(`semantic.${label}.json.${name}.free expected fixture ids got ${freeIds}`);
		}
		const recommendedModel = recordBody(recommended[0]);
		if (
			recommendedModel.description !== "Fixture recommended model" ||
			!Array.isArray(recommendedModel.tags) ||
			recommendedModel.tags.join(",") !== "BEST"
		) {
			diffs.push(
				`semantic.${label}.json.${name}.recommended[0] expected description/tags got ${JSON.stringify(recommendedModel)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "clineModels", 200, diffs);
	expectJSONValue(label, sample, "clineModels", "fromCache", true, diffs);
	expectJSONArray(label, sample, "clineModels", "models", diffs);
	const clineModels = sampleJSONArray(sample, "clineModels", "models");
	const clineModelIds = clineModels.map((model) => recordBody(model).id).sort();
	if (clineModelIds.join(",") !== "cline/free-fixture,cline/paid-fixture") {
		diffs.push(
			`semantic.${label}.json.clineModels.models expected cached fixture ids got ${clineModelIds.join(",")}`,
		);
	}
	expectJSONStatus(label, sample, "clineMissingProviderModels", 200, diffs);
	expectJSONValue(label, sample, "clineMissingProviderModels", "fromCache", true, diffs);
	expectJSONArray(label, sample, "clineMissingProviderModels", "models", diffs);
	expectJSONStatus(label, sample, "clineMissingProviderRefresh", 404, diffs);
	expectJSONValue(
		label,
		sample,
		"clineMissingProviderRefresh",
		"error",
		`Provider "missing" not found`,
		diffs,
	);
	expectJSONStatus(label, sample, "clinePoolSearch", 200, diffs);
	expectJSONArray(label, sample, "clinePoolSearch", "models", diffs);
	expectJSONValue(label, sample, "clinePoolSearch", "total", 2, diffs);
	const clinePoolIds = sampleJSONArray(sample, "clinePoolSearch", "models")
		.map((model) => recordBody(model).id)
		.sort();
	if (clinePoolIds.join(",") !== "cline/free-fixture,cline/paid-fixture") {
		diffs.push(
			`semantic.${label}.json.clinePoolSearch.models expected fixture ids got ${clinePoolIds.join(",")}`,
		);
	}
	expectJSONStatus(label, sample, "clinePoolCount", 200, diffs);
	expectJSONValue(label, sample, "clinePoolCount", "count", 2, diffs);
	expectJSONStatus(label, sample, "clineEnabledModelsInvalid", 400, diffs);
	expectJSONValue(
		label,
		sample,
		"clineEnabledModelsInvalid",
		"error",
		"models must be an array",
		diffs,
	);
	expectJSONStatus(label, sample, "clineEnabledModelsValid", 200, diffs);
	expectJSONValue(label, sample, "clineEnabledModelsValid", "ok", true, diffs);
	expectJSONValue(label, sample, "clineEnabledModelsValid", "count", 1, diffs);
	const clineContextWindows = recordBody(
		sample.json.clineEnabledModelsValid.body,
	).modelContextWindows;
	if (!clineContextWindows || typeof clineContextWindows !== "object") {
		diffs.push(
			`semantic.${label}.json.clineEnabledModelsValid.modelContextWindows expected object got ${JSON.stringify(clineContextWindows)}`,
		);
	} else if (recordBody(clineContextWindows)["clinecache:cline/free-fixture"] !== 123456) {
		diffs.push(
			`semantic.${label}.json.clineEnabledModelsValid.modelContextWindows expected cached context window got ${JSON.stringify(clineContextWindows)}`,
		);
	}
	expectJSONStatus(label, sample, "clineStatusAfterEnabledModels", 200, diffs);
	expectJSONValue(label, sample, "clineStatusAfterEnabledModels", "totalModels", 2, diffs);
	expectJSONStatus(label, sample, "clineSettingsAfterEnabledModels", 200, diffs);
	const clineSettingsProviders = recordBody(
		sample.json.clineSettingsAfterEnabledModels.body,
	).clineProviders;
	if (!Array.isArray(clineSettingsProviders) || clineSettingsProviders.length === 0) {
		diffs.push(
			`semantic.${label}.json.clineSettingsAfterEnabledModels.clineProviders expected non-empty array got ${JSON.stringify(clineSettingsProviders)}`,
		);
	} else {
		const firstClineProvider = recordBody(clineSettingsProviders[0]);
		const enabledModels = firstClineProvider.enabledModels;
		if (!Array.isArray(enabledModels) || enabledModels.join(",") !== "cline/free-fixture") {
			diffs.push(
				`semantic.${label}.json.clineSettingsAfterEnabledModels.enabledModels expected cline/free-fixture got ${JSON.stringify(enabledModels)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "clineEnabledModelsResetBeforeAuth", 200, diffs);
	expectJSONValue(label, sample, "clineEnabledModelsResetBeforeAuth", "ok", true, diffs);
	expectJSONValue(label, sample, "clineEnabledModelsResetBeforeAuth", "count", 0, diffs);
	expectJSONStatus(label, sample, "clineAuthCancel", 200, diffs);

	expectJSONValue(label, sample, "clineAuthCancel", "ok", true, diffs);
	expectJSONStatus(label, sample, "clineAuthCallbackInvalid", 400, diffs);
	expectJSONValue(
		label,
		sample,
		"clineAuthCallbackInvalid",
		"error",
		"callbackUrl is required",
		diffs,
	);
	expectJSONStatus(label, sample, "clineAuthCallbackValid", 200, diffs);
	expectJSONValue(label, sample, "clineAuthCallbackValid", "ok", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineAuthCallbackValid",
		"email",
		"cline-alice@example.com",
		diffs,
	);
	expectJSONValue(label, sample, "clineAuthCallbackValid", "displayName", "Cline Alice", diffs);
	expectJSONStatus(label, sample, "clineStatusAfterAuthCallback", 200, diffs);
	expectJSONValue(label, sample, "clineStatusAfterAuthCallback", "authenticated", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterAuthCallback",
		"email",
		"cline-updated@example.com",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterAuthCallback",
		"displayName",
		"Cline Updated User",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterAuthCallback",
		"userId",
		"cline-user-fixture",
		diffs,
	);
	const clineAuthStatusProviders = recordBody(
		sample.json.clineStatusAfterAuthCallback.body,
	).providers;
	if (!Array.isArray(clineAuthStatusProviders) || clineAuthStatusProviders.length === 0) {
		diffs.push(
			`semantic.${label}.json.clineStatusAfterAuthCallback.providers expected non-empty array got ${JSON.stringify(clineAuthStatusProviders)}`,
		);
	} else if (recordBody(clineAuthStatusProviders[0]).hasToken !== true) {
		diffs.push(
			`semantic.${label}.json.clineStatusAfterAuthCallback.providers[0].hasToken expected true got ${JSON.stringify(clineAuthStatusProviders[0])}`,
		);
	}
	expectJSONStatus(label, sample, "clineSettingsAfterAuthCallback", 200, diffs);
	const clineAuthProvider = findSettingsProvider(
		sample,
		"clineSettingsAfterAuthCallback",
		"clineProviders",
		"cline-cache-fixture",
	);
	if (!clineAuthProvider) {
		diffs.push(
			`semantic.${label}.json.clineSettingsAfterAuthCallback.clineProviders.missingProvider cline-cache-fixture`,
		);
	} else {
		const accessToken = clineAuthProvider.accessToken;
		if (
			typeof accessToken !== "string" ||
			accessToken === "cline-callback-access-token" ||
			!accessToken.startsWith("********")
		) {
			diffs.push(
				`semantic.${label}.json.clineSettingsAfterAuthCallback.accessToken expected masked callback token got ${JSON.stringify(accessToken)}`,
			);
		}
		const enabledModels = clineAuthProvider.enabledModels;
		if (!Array.isArray(enabledModels) || enabledModels.length === 0) {
			diffs.push(
				`semantic.${label}.json.clineSettingsAfterAuthCallback.enabledModels expected auto-added free models got ${JSON.stringify(enabledModels)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "clineUserInfoRefreshValid", 200, diffs);
	expectJSONValue(label, sample, "clineUserInfoRefreshValid", "authenticated", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineUserInfoRefreshValid",
		"email",
		"cline-updated@example.com",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineUserInfoRefreshValid",
		"displayName",
		"Cline Updated User",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineUserInfoRefreshValid",
		"userId",
		"cline-user-fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "clineBalanceAfterUserInfoRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineBalanceAfterUserInfoRefresh", "balance", 123456, diffs);
	expectJSONValue(
		label,
		sample,
		"clineBalanceAfterUserInfoRefresh",
		"userId",
		"cline-user-fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "clineStatusAfterUserInfoRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineStatusAfterUserInfoRefresh", "authenticated", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterUserInfoRefresh",
		"email",
		"cline-updated@example.com",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterUserInfoRefresh",
		"displayName",
		"Cline Updated User",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterUserInfoRefresh",
		"userId",
		"cline-user-fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "clineAuthLogout", 200, diffs);
	expectJSONValue(label, sample, "clineAuthLogout", "ok", true, diffs);
	expectJSONStatus(label, sample, "clineEnabledModelsResetBeforeBrowserAuth", 200, diffs);
	expectJSONValue(label, sample, "clineEnabledModelsResetBeforeBrowserAuth", "count", 0, diffs);
	expectJSONStatus(label, sample, "clineBrowserAuthStart", 200, diffs);
	const clineBrowserAuthorizeUrl = recordBody(sample.json.clineBrowserAuthStart.body).authorizeUrl;
	if (typeof clineBrowserAuthorizeUrl !== "string") {
		diffs.push(
			`semantic.${label}.json.clineBrowserAuthStart.authorizeUrl expected string got ${JSON.stringify(clineBrowserAuthorizeUrl)}`,
		);
	} else {
		const parsedAuthorizeUrl = new URL(clineBrowserAuthorizeUrl);
		if (
			parsedAuthorizeUrl.pathname !== "/cline/oauth/authorize" ||
			parsedAuthorizeUrl.searchParams.get("state") !== "cline-browser-fixture" ||
			parsedAuthorizeUrl.searchParams.get("callback_url") !== "http://localhost:19876/auth/callback"
		) {
			diffs.push(
				`semantic.${label}.json.clineBrowserAuthStart.authorizeUrl unexpected ${clineBrowserAuthorizeUrl}`,
			);
		}
	}
	expectJSONStatus(label, sample, "clineBrowserCallback", 200, diffs);
	if (
		typeof sample.json.clineBrowserCallback.body !== "string" ||
		!sample.json.clineBrowserCallback.body.includes("Authorization successful")
	) {
		diffs.push(
			`semantic.${label}.json.clineBrowserCallback.body expected success html got ${JSON.stringify(sample.json.clineBrowserCallback.body)}`,
		);
	}
	expectJSONStatus(label, sample, "clineStatusAfterBrowserCallback", 200, diffs);
	expectJSONValue(label, sample, "clineStatusAfterBrowserCallback", "authenticated", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterBrowserCallback",
		"email",
		"cline-updated@example.com",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterBrowserCallback",
		"displayName",
		"Cline Updated User",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterBrowserCallback",
		"userId",
		"cline-user-fixture",
		diffs,
	);
	const clineBrowserStatusProviders = recordBody(
		sample.json.clineStatusAfterBrowserCallback.body,
	).providers;
	if (!Array.isArray(clineBrowserStatusProviders) || clineBrowserStatusProviders.length === 0) {
		diffs.push(
			`semantic.${label}.json.clineStatusAfterBrowserCallback.providers expected non-empty array got ${JSON.stringify(clineBrowserStatusProviders)}`,
		);
	} else if (recordBody(clineBrowserStatusProviders[0]).hasToken !== true) {
		diffs.push(
			`semantic.${label}.json.clineStatusAfterBrowserCallback.providers[0].hasToken expected true got ${JSON.stringify(clineBrowserStatusProviders[0])}`,
		);
	}
	expectJSONStatus(label, sample, "clineSettingsAfterBrowserCallback", 200, diffs);
	const clineBrowserProvider = findSettingsProvider(
		sample,
		"clineSettingsAfterBrowserCallback",
		"clineProviders",
		"cline-cache-fixture",
	);
	if (!clineBrowserProvider) {
		diffs.push(
			`semantic.${label}.json.clineSettingsAfterBrowserCallback.clineProviders.missingProvider cline-cache-fixture`,
		);
	} else {
		const accessToken = clineBrowserProvider.accessToken;
		if (
			typeof accessToken !== "string" ||
			accessToken === "cline-browser-access-token" ||
			!accessToken.startsWith("********")
		) {
			diffs.push(
				`semantic.${label}.json.clineSettingsAfterBrowserCallback.accessToken expected masked browser token got ${JSON.stringify(accessToken)}`,
			);
		}
		const enabledModels = clineBrowserProvider.enabledModels;
		if (!Array.isArray(enabledModels) || enabledModels.length === 0) {
			diffs.push(
				`semantic.${label}.json.clineSettingsAfterBrowserCallback.enabledModels expected auto-added free models got ${JSON.stringify(enabledModels)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "clineBrowserAuthLogout", 200, diffs);
	expectJSONValue(label, sample, "clineBrowserAuthLogout", "ok", true, diffs);
	expectJSONStatus(label, sample, "clineAuthCallbackExpired", 200, diffs);
	expectJSONValue(label, sample, "clineAuthCallbackExpired", "ok", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineAuthCallbackExpired",
		"email",
		"cline-expired@example.com",
		diffs,
	);
	expectJSONStatus(label, sample, "clineBalanceAfterExpiredAuthRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineBalanceAfterExpiredAuthRefresh", "balance", 654321, diffs);
	expectJSONValue(
		label,
		sample,
		"clineBalanceAfterExpiredAuthRefresh",
		"userId",
		"cline-refresh-user-fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "clineStatusAfterExpiredAuthRefresh", 200, diffs);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterExpiredAuthRefresh",
		"authenticated",
		true,
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterExpiredAuthRefresh",
		"email",
		"cline-refresh-userinfo@example.com",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterExpiredAuthRefresh",
		"displayName",
		"Cline Refresh UserInfo",
		diffs,
	);
	expectJSONValue(
		label,
		sample,
		"clineStatusAfterExpiredAuthRefresh",
		"userId",
		"cline-refresh-user-fixture",
		diffs,
	);
	expectJSONStatus(label, sample, "clineAuthLogoutAfterExpiredRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineAuthLogoutAfterExpiredRefresh", "ok", true, diffs);
	expectJSONStatus(label, sample, "clineAuthCallbackInvalidRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineAuthCallbackInvalidRefresh", "ok", true, diffs);
	expectJSONValue(
		label,
		sample,
		"clineAuthCallbackInvalidRefresh",
		"email",
		"cline-invalid-refresh-expired@example.com",
		diffs,
	);
	expectJSONStatus(label, sample, "clineBalanceAfterInvalidRefresh", 422, diffs);
	expectJSONValue(
		label,
		sample,
		"clineBalanceAfterInvalidRefresh",
		"error",
		"Not authenticated or balance unavailable",
		diffs,
	);
	expectJSONStatus(label, sample, "clineStatusAfterInvalidRefresh", 200, diffs);
	expectJSONValue(label, sample, "clineStatusAfterInvalidRefresh", "authenticated", false, diffs);
	expectJSONStatus(label, sample, "clineProviderRefreshValid", 200, diffs);
	expectJSONValue(label, sample, "clineProviderRefreshValid", "count", 3, diffs);
	expectJSONValue(label, sample, "clineProviderRefreshValid", "fromCache", false, diffs);
	if ("models" in recordBody(sample.json.clineProviderRefreshValid.body)) {
		diffs.push(
			`semantic.${label}.json.clineProviderRefreshValid.models should be omitted for TS-compatible provider-specific Cline refresh`,
		);
	}
	for (const [name, expectedIds] of [
		["clineProviderModelsAfterRefresh", "cline/refresh-a,cline/refresh-a,cline/refresh-b"],
		["clineModelsAfterProviderRefresh", "cline/refresh-a,cline/refresh-b"],
	] as const) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectJSONValue(label, sample, name, "fromCache", true, diffs);
		const refreshedClineModels = sampleJSONArray(sample, name, "models");
		const ids = refreshedClineModels.map((model) => recordBody(model).id);
		if (ids.join(",") !== expectedIds) {
			diffs.push(
				`semantic.${label}.json.${name}.models expected Cline refresh ids ${expectedIds} got ${ids.join(",")}`,
			);
		}
		const firstModel = recordBody(refreshedClineModels[0]);
		if (
			firstModel.contextLength !== 100000 ||
			firstModel.promptPrice !== "0.000001" ||
			firstModel.completionPrice !== "0.000003"
		) {
			diffs.push(
				`semantic.${label}.json.${name}.models[0] expected normalized Cline refresh metadata got ${JSON.stringify(firstModel)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "codexStatus", 200, diffs);
	expectJSONArray(label, sample, "codexStatus", "entries", diffs);
	expectJSONValue(label, sample, "codexStatus", "useWebSocket", true, diffs);
	expectJSONValue(label, sample, "codexStatus", "currentId", "codex-contract", diffs);
	expectJSONValue(label, sample, "codexStatus", "total", 1, diffs);
	expectJSONValue(label, sample, "codexStatus", "availableTotal", 1, diffs);
	expectJSONValue(label, sample, "codexStatus", "unavailableTotal", 0, diffs);
	const codexStatusEntries = sampleJSONArray(sample, "codexStatus", "entries");
	if (
		codexStatusEntries.length !== 1 ||
		recordBody(codexStatusEntries[0]).id !== "codex-contract"
	) {
		diffs.push(`semantic.${label}.json.codexStatus.entries expected only codex-contract`);
	}
	expectJSONStatus(label, sample, "codexDefaultReasoningEffort", 200, diffs);
	expectJSONValue(label, sample, "codexDefaultReasoningEffort", "reasoningEffort", "medium", diffs);
	expectJSONStatus(label, sample, "codexUseWebSocketMissing", 200, diffs);
	expectJSONValue(label, sample, "codexUseWebSocketMissing", "ok", true, diffs);
	expectJSONValue(label, sample, "codexUseWebSocketMissing", "useWebSocket", true, diffs);
	expectJSONStatus(label, sample, "codexUseWebSocketInvalid", 400, diffs);
	expectJSONStatus(label, sample, "codexLoadBalancingInvalid", 400, diffs);
	expectJSONStatus(label, sample, "codexTierOrderInvalid", 400, diffs);
	expectJSONStatus(label, sample, "codexTierOrderValid", 200, diffs);
	expectJSONValue(label, sample, "codexTierOrderValid", "ok", true, diffs);
	expectJSONArray(label, sample, "codexTierOrderValid", "tierOrder", diffs);
	expectJSONStatus(label, sample, "codexImportMissing", 400, diffs);
	expectJSONValue(label, sample, "codexImportMissing", "error", "No credentials provided", diffs);
	expectJSONStatus(label, sample, "codexImportInvalidNoToken", 400, diffs);
	expectJSONValue(
		label,
		sample,
		"codexImportInvalidNoToken",
		"error",
		"No valid credentials: each credential must have a non-empty refreshToken or accessToken",
		diffs,
	);
	for (const name of [
		"codexCredentialDisableMissing",
		"codexCredentialEnableMissing",
		"codexCredentialResetMissing",
		"codexCredentialUpdateMissing",
		"codexCredentialDeleteMissing",
	] as const) {
		expectJSONStatus(label, sample, name, 404, diffs);
		expectJSONValue(label, sample, name, "error", "Credential not found: missing", diffs);
	}
	for (const name of [
		"codexCredentialUpdateEmptyBody",
		"codexCredentialUpdateInvalidJSON",
	] as const) {
		expectJSONStatus(label, sample, name, 200, diffs);
		expectJSONValue(label, sample, name, "ok", true, diffs);
	}
	expectJSONStatus(label, sample, "codexCredentialUpdateMissingInvalidJSON", 404, diffs);
	expectJSONValue(
		label,
		sample,
		"codexCredentialUpdateMissingInvalidJSON",
		"error",
		"Credential not found: missing",
		diffs,
	);
	for (const name of ["codexCredentialRefreshMissing", "codexCredentialUsageMissing"] as const) {
		expectJSONStatus(label, sample, name, 400, diffs);
		expectJSONValue(label, sample, name, "error", "Credential not found: missing", diffs);
	}
	expectJSONStatus(label, sample, "codexCredentialBatchDeleteMissing", 200, diffs);
	expectJSONArray(label, sample, "codexCredentialBatchDeleteMissing", "removed", diffs);
	expectJSONArray(label, sample, "codexCredentialBatchDeleteMissing", "notFound", diffs);
	if (
		sampleJSONArray(sample, "codexCredentialBatchDeleteMissing", "notFound").join(",") !== "missing"
	) {
		diffs.push(
			`semantic.${label}.json.codexCredentialBatchDeleteMissing.notFound expected missing`,
		);
	}
	expectJSONStatus(label, sample, "userOpenaiProviderRefreshValid", 200, diffs);
	expectJSONValue(label, sample, "userOpenaiProviderRefreshValid", "fromCache", false, diffs);
	const refreshedOpenAIModels = recordBody(sample.json.userOpenaiProviderRefreshValid.body).models;
	if (!Array.isArray(refreshedOpenAIModels) || refreshedOpenAIModels.length !== 2) {
		diffs.push(
			`semantic.${label}.json.userOpenaiProviderRefreshValid.models expected two unique models got ${JSON.stringify(refreshedOpenAIModels)}`,
		);
	} else {
		const ids = refreshedOpenAIModels.map((model) => recordBody(model).id);
		if (ids.join(",") !== "gpt-contract-a,gpt-contract-b") {
			diffs.push(
				`semantic.${label}.json.userOpenaiProviderRefreshValid.models expected sorted unique fixture ids got ${ids.join(",")}`,
			);
		}
	}
	expectProviderGlobalRefresh(
		label,
		sample,
		"userOpenaiGlobalRefresh",
		"openai-secret-fixture",
		"openai-no-key-fixture",
		`OpenAI API key not configured for provider "OpenAI No Key Fixture"`,
		"id",
		"gpt-contract-a,gpt-contract-b",
		diffs,
	);
	expectProviderNoKeyRefresh(
		label,
		sample,
		"userOpenaiProviderRefreshNoKey",
		`OpenAI API key not configured for provider "OpenAI No Key Fixture"`,
		diffs,
	);
	expectJSONStatus(label, sample, "userOpenaiModels", 200, diffs);
	expectJSONValue(label, sample, "userOpenaiModels", "fromCache", true, diffs);
	expectJSONStatus(label, sample, "userAnthropicProviderRefreshValid", 200, diffs);
	expectJSONValue(label, sample, "userAnthropicProviderRefreshValid", "fromCache", false, diffs);
	const refreshedAnthropicModels = recordBody(
		sample.json.userAnthropicProviderRefreshValid.body,
	).models;
	if (!Array.isArray(refreshedAnthropicModels) || refreshedAnthropicModels.length !== 2) {
		diffs.push(
			`semantic.${label}.json.userAnthropicProviderRefreshValid.models expected two unique models got ${JSON.stringify(refreshedAnthropicModels)}`,
		);
	} else {
		const ids = refreshedAnthropicModels.map((model) => recordBody(model).id);
		if (ids.join(",") !== "claude-contract-a,claude-contract-b") {
			diffs.push(
				`semantic.${label}.json.userAnthropicProviderRefreshValid.models expected sorted unique fixture ids got ${ids.join(",")}`,
			);
		}
	}
	const anthropicResolvedBaseUrl = recordBody(
		sample.json.userAnthropicProviderRefreshValid.body,
	).resolvedBaseUrl;
	if (
		typeof anthropicResolvedBaseUrl !== "string" ||
		!anthropicResolvedBaseUrl.endsWith("/anthropic/v1")
	) {
		diffs.push(
			`semantic.${label}.json.userAnthropicProviderRefreshValid.resolvedBaseUrl expected fixture /anthropic/v1 URL got ${JSON.stringify(anthropicResolvedBaseUrl)}`,
		);
	}
	expectProviderGlobalRefresh(
		label,
		sample,
		"userAnthropicGlobalRefresh",
		"anthropic-secret-fixture",
		"anthropic-no-key-fixture",
		`Anthropic API key not configured for provider "Anthropic No Key Fixture"`,
		"id",
		"claude-contract-a,claude-contract-b",
		diffs,
	);
	expectProviderNoKeyRefresh(
		label,
		sample,
		"userAnthropicProviderRefreshNoKey",
		`Anthropic API key not configured for provider "Anthropic No Key Fixture"`,
		diffs,
	);
	expectJSONStatus(label, sample, "userNkpProviderRefreshValid", 200, diffs);
	expectJSONValue(label, sample, "userNkpProviderRefreshValid", "fromCache", false, diffs);
	const refreshedNkpModels = recordBody(sample.json.userNkpProviderRefreshValid.body).models;
	if (!Array.isArray(refreshedNkpModels) || refreshedNkpModels.length !== 2) {
		diffs.push(
			`semantic.${label}.json.userNkpProviderRefreshValid.models expected two unique models got ${JSON.stringify(refreshedNkpModels)}`,
		);
	} else {
		const ids = refreshedNkpModels.map((model) => recordBody(model).model_id);
		if (ids.join(",") !== "nkp-contract-a,nkp-contract-b") {
			diffs.push(
				`semantic.${label}.json.userNkpProviderRefreshValid.models expected sorted unique fixture model_id values got ${ids.join(",")}`,
			);
		}
	}
	expectProviderGlobalRefresh(
		label,
		sample,
		"userNkpGlobalRefresh",
		"nkp-secret-fixture",
		"nkp-no-key-fixture",
		`NKP API key not configured for provider "NKP No Key Fixture"`,
		"model_id",
		"nkp-contract-a,nkp-contract-b",
		diffs,
	);
	expectProviderNoKeyRefresh(
		label,
		sample,
		"userNkpProviderRefreshNoKey",
		`NKP API key not configured for provider "NKP No Key Fixture"`,
		diffs,
	);
	expectJSONStatus(label, sample, "userNkpModels", 200, diffs);
	expectJSONValue(label, sample, "userNkpModels", "fromCache", true, diffs);
	expectJSONStatus(label, sample, "userNkpQuota", 200, diffs);
	expectJSONValue(label, sample, "userNkpQuota", "quotaBalance", 12.34, diffs);
	expectJSONValue(label, sample, "userNkpQuota", "quotaTotalGranted", 56.78, diffs);
	expectJSONStatus(label, sample, "userNkpQuotas", 200, diffs);
	const nkpQuotaCache = recordBody(sample.json.userNkpQuotas.body)["nkp-secret-fixture"];
	const nkpQuotaCacheBody = recordBody(nkpQuotaCache);
	const nkpQuotaKeys = Object.keys(nkpQuotaCacheBody).sort().join(",");
	if (
		nkpQuotaCacheBody.quotaBalance !== 12.34 ||
		nkpQuotaCacheBody.quotaTotalGranted !== 56.78 ||
		nkpQuotaKeys !== "quotaBalance,quotaTotalGranted"
	) {
		diffs.push(
			`semantic.${label}.json.userNkpQuotas.nkp-secret-fixture expected cached quota balance/granted-only payload got ${JSON.stringify(nkpQuotaCache)}`,
		);
	}
	expectJSONStatus(label, sample, "settingsAfterNkpQuota", 200, diffs);
	const settingsNkpQuotas = recordBody(
		recordBody(sample.json.settingsAfterNkpQuota.body).nkpQuotas,
	);
	const settingsNkpQuota = settingsNkpQuotas["nkp-secret-fixture"];
	const settingsNkpQuotaBody = recordBody(settingsNkpQuota);
	const settingsNkpQuotaKeys = Object.keys(settingsNkpQuotaBody).sort().join(",");
	if (
		settingsNkpQuotaBody.quotaBalance !== 12.34 ||
		settingsNkpQuotaBody.quotaTotalGranted !== 56.78 ||
		settingsNkpQuotaKeys !== "quotaBalance,quotaTotalGranted"
	) {
		diffs.push(
			`semantic.${label}.json.settingsAfterNkpQuota.nkpQuotas.nkp-secret-fixture expected balance/granted-only payload got ${JSON.stringify(settingsNkpQuota)}`,
		);
	}

	expectJSONStatus(label, sample, "userNugProviderRefreshValid", 200, diffs);

	expectJSONValue(label, sample, "userNugProviderRefreshValid", "fromCache", false, diffs);
	const refreshedNugModels = recordBody(sample.json.userNugProviderRefreshValid.body).models;
	if (!Array.isArray(refreshedNugModels) || refreshedNugModels.length !== 2) {
		diffs.push(
			`semantic.${label}.json.userNugProviderRefreshValid.models expected two unique models got ${JSON.stringify(refreshedNugModels)}`,
		);
	} else {
		const ids = refreshedNugModels.map((model) => recordBody(model).id);
		if (ids.join(",") !== "nug-contract-a,nug-contract-b") {
			diffs.push(
				`semantic.${label}.json.userNugProviderRefreshValid.models expected sorted unique fixture ids got ${ids.join(",")}`,
			);
		}
	}
	expectJSONStatus(label, sample, "userNugProviderRefreshNoKey", 500, diffs);
	expectJSONValue(
		label,
		sample,
		"userNugProviderRefreshNoKey",
		"error",
		`NUG API key not configured for provider "NUG Login Fixture"`,
		diffs,
	);
	expectJSONStatus(label, sample, "userNugGlobalRefresh", 200, diffs);
	expectJSONValue(label, sample, "userNugGlobalRefresh", "fromCache", false, diffs);
	const nugGlobalResults = sampleJSONArray(sample, "userNugGlobalRefresh", "results");
	if (nugGlobalResults.length !== 3) {
		diffs.push(
			`semantic.${label}.json.userNugGlobalRefresh.results expected 3 provider results got ${nugGlobalResults.length}`,
		);
	}
	const nugGlobalSecret = expectArrayObject(
		label,
		sample,
		"userNugGlobalRefresh",
		"results",
		"providerId",
		"nug-secret-fixture",
		diffs,
	);
	if (nugGlobalSecret && nugGlobalSecret.count !== 2) {
		diffs.push(
			`semantic.${label}.json.userNugGlobalRefresh.nug-secret-fixture.count expected 2 got ${JSON.stringify(nugGlobalSecret.count)}`,
		);
	}
	for (const [providerId, providerName] of [
		["nug-login-fixture", "NUG Login Fixture"],
		["nug-oauth-fixture", "NUG OAuth Fixture"],
	] as const) {
		const missingKeyResult = expectArrayObject(
			label,
			sample,
			"userNugGlobalRefresh",
			"results",
			"providerId",
			providerId,
			diffs,
		);
		const expectedError = `NUG API key not configured for provider "${providerName}"`;
		if (
			missingKeyResult &&
			(missingKeyResult.count !== 0 || missingKeyResult.error !== expectedError)
		) {
			diffs.push(
				`semantic.${label}.json.userNugGlobalRefresh.${providerId} expected count=0 error=${expectedError} got ${JSON.stringify(missingKeyResult)}`,
			);
		}
	}
	const nugGlobalModels = sampleJSONArray(sample, "userNugGlobalRefresh", "models");
	if (
		nugGlobalModels.map((model) => recordBody(model).id).join(",") !==
		"nug-contract-a,nug-contract-b"
	) {
		diffs.push(
			`semantic.${label}.json.userNugGlobalRefresh.models expected sorted unique fixture ids`,
		);
	}
	expectJSONStatus(label, sample, "userNugModels", 200, diffs);
	expectJSONValue(label, sample, "userNugModels", "fromCache", true, diffs);
	expectJSONStatus(label, sample, "userNugQuota", 200, diffs);
	expectJSONValue(label, sample, "userNugQuota", "balance", 12.5, diffs);
	expectJSONValue(label, sample, "userNugQuota", "totalGranted", 50, diffs);
	expectJSONValue(label, sample, "userNugQuota", "username", "nug-fixture", diffs);
	expectJSONStatus(label, sample, "userNugQuotas", 200, diffs);
	const cachedNugQuota = recordBody(
		recordBody(sample.json.userNugQuotas.body)["nug-secret-fixture"],
	);
	const cachedNugQuotaKeys = Object.keys(cachedNugQuota).sort().join(",");
	if (
		cachedNugQuota.balance !== 12.5 ||
		cachedNugQuota.totalGranted !== 50 ||
		cachedNugQuota.detailedQuotaBalance !== null ||
		cachedNugQuotaKeys !== "balance,detailedQuotaBalance,totalGranted"
	) {
		diffs.push(
			`semantic.${label}.json.userNugQuotas.nug-secret-fixture expected cached balance/totalGranted/null-detail payload got ${JSON.stringify(cachedNugQuota)}`,
		);
	}
	expectJSONStatus(label, sample, "userNugChannelsHealth", 200, diffs);
	const nugChannels = recordBody(sample.json.userNugChannelsHealth.body).channels;
	if (!Array.isArray(nugChannels) || recordBody(nugChannels[0]).channelType !== "kiro") {
		diffs.push(
			`semantic.${label}.json.userNugChannelsHealth.channels expected kiro channel got ${JSON.stringify(nugChannels)}`,
		);
	}
	expectJSONStatus(label, sample, "userNugUsage", 200, diffs);
	expectJSONValue(label, sample, "userNugUsage", "total", 1, diffs);
	const nugUsageEvents = recordBody(sample.json.userNugUsage.body).events;
	if (!Array.isArray(nugUsageEvents) || nugUsageEvents.length !== 1) {
		diffs.push(
			`semantic.${label}.json.userNugUsage.events expected one event got ${JSON.stringify(nugUsageEvents)}`,
		);
	} else {
		const event = recordBody(nugUsageEvents[0]);
		if (
			event.channelType !== "kiro" ||
			event.model !== "claude-nug-fixture" ||
			event.inputTokens !== 11 ||
			event.cacheReadInputTokens !== 3 ||
			event.quotaCost !== 6.7
		) {
			diffs.push(
				`semantic.${label}.json.userNugUsage.events[0] expected normalized fixture event got ${JSON.stringify(event)}`,
			);
		}
	}
	expectJSONStatus(label, sample, "userNugUsageSummary", 200, diffs);
	expectJSONValue(label, sample, "userNugUsageSummary", "requestCount", 3, diffs);
	expectJSONValue(label, sample, "userNugUsageSummary", "totalQuotaCost", 6.7, diffs);
	expectJSONValue(label, sample, "userNugUsageSummary", "totalCacheReadTokens", 7, diffs);
	expectJSONStatus(label, sample, "userNugLoginValid", 200, diffs);
	expectJSONValue(label, sample, "userNugLoginValid", "apiKey", "nug-generated-key", diffs);
	expectJSONPathValue(label, sample, "userNugLoginValid", ["user", "username"], "alice", diffs);
	expectJSONStatus(label, sample, "userNugApiKeyValid", 200, diffs);
	expectJSONValue(label, sample, "userNugApiKeyValid", "apiKey", "nug-generated-key", diffs);
	expectJSONStatus(label, sample, "userNugLoginMissingFields", 400, diffs);
	expectJSONValue(
		label,
		sample,
		"userNugLoginMissingFields",
		"error",
		"Missing username or password",
		diffs,
	);
	expectJSONStatus(label, sample, "userNugApiKeyMissingSession", 400, diffs);
	expectJSONValue(
		label,
		sample,
		"userNugApiKeyMissingSession",
		"error",
		"Missing sessionToken",
		diffs,
	);
	const nugLoginProvider = findSettingsProvider(
		sample,
		"settingsAfterNugLogin",
		"nugProviders",
		"nug-login-fixture",
	);
	if (!nugLoginProvider) {
		diffs.push(
			`semantic.${label}.json.settingsAfterNugLogin.nugProviders.missingProvider nug-login-fixture`,
		);
	} else if (
		nugLoginProvider.apiKey ||
		nugLoginProvider.nugUsername ||
		nugLoginProvider.nugUserId
	) {
		diffs.push(
			`semantic.${label}.json.settingsAfterNugLogin.nug-login-fixture expected no server-side credential persistence got ${JSON.stringify(nugLoginProvider)}`,
		);
	}
	expectJSONStatus(label, sample, "userNugOAuthStartValid", 200, diffs);
	const nugAuthorizeUrl = recordBody(sample.json.userNugOAuthStartValid.body).authorizeUrl;
	const nugOAuthState = recordBody(sample.json.userNugOAuthStartValid.body).state;
	if (typeof nugAuthorizeUrl !== "string" || typeof nugOAuthState !== "string") {
		diffs.push(
			`semantic.${label}.json.userNugOAuthStartValid expected authorizeUrl/state got ${JSON.stringify(sample.json.userNugOAuthStartValid.body)}`,
		);
	} else {
		const parsedAuthorizeUrl = new URL(nugAuthorizeUrl);
		if (
			parsedAuthorizeUrl.pathname !== "/nug/oauth/authorize" ||
			parsedAuthorizeUrl.searchParams.get("client_id") !== "nug-oauth-client" ||
			parsedAuthorizeUrl.searchParams.get("state") !== nugOAuthState ||
			!parsedAuthorizeUrl.searchParams.get("redirect_uri")?.endsWith("/api/nug/oauth/callback")
		) {
			diffs.push(
				`semantic.${label}.json.userNugOAuthStartValid.authorizeUrl unexpected ${nugAuthorizeUrl}`,
			);
		}
	}
	expectJSONStatus(label, sample, "nugOAuthCallbackValid", 302, diffs);
	expectJSONValue(
		label,
		sample,
		"nugOAuthCallbackValid",
		"location",
		"/settings/providers?oauth_success=nug-oauth-fixture",
		diffs,
	);
	const nugOAuthProvider = findSettingsProvider(
		sample,
		"settingsAfterNugOAuth",
		"nugProviders",
		"nug-oauth-fixture",
	);
	if (!nugOAuthProvider) {
		diffs.push(
			`semantic.${label}.json.settingsAfterNugOAuth.nugProviders.missingProvider nug-oauth-fixture`,
		);
	} else {
		const apiKey = nugOAuthProvider.apiKey;
		if (
			typeof apiKey !== "string" ||
			apiKey === "nug-oauth-api-key" ||
			!apiKey.startsWith("********")
		) {
			diffs.push(
				`semantic.${label}.json.settingsAfterNugOAuth.nug-oauth-fixture.apiKey expected masked OAuth key got ${JSON.stringify(apiKey)}`,
			);
		}
		if (
			nugOAuthProvider.oauthDeviceId !== "nug-oauth-device" ||
			nugOAuthProvider.nugUsername !== "oauth-alice" ||
			nugOAuthProvider.nugUserId !== "nug-oauth-user-1"
		) {
			diffs.push(
				`semantic.${label}.json.settingsAfterNugOAuth.nug-oauth-fixture expected persisted OAuth metadata got ${JSON.stringify(nugOAuthProvider)}`,
			);
		}
	}
	for (const name of [
		"userOpenaiMissingRefresh",
		"userAnthropicMissingRefresh",
		"userNkpMissingRefresh",
		"userNugMissingRefresh",
		"userNugLoginMissing",
		"userNugApiKeyMissing",
		"userNugOAuthStartMissing",
	]) {
		expectJSONStatus(label, sample, name, 404, diffs);
		expectJSONValue(label, sample, name, "error", `Provider "missing" not found`, diffs);
	}
	expectJSONStatus(label, sample, "userCodexQuotaOverview", 200, diffs);
	for (const name of ["userCodexStatus", "userClineStatus", "userKiroStatus"]) {
		expectJSONStatus(label, sample, name, 403, diffs);
	}
	expectJSONValue(
		label,
		sample,
		"clineBalance",
		"error",
		"Not authenticated or balance unavailable",
		diffs,
	);
	expectJSONStatus(label, sample, "nkpMissingQuota", 404, diffs);
	expectJSONValue(label, sample, "nkpMissingQuota", "error", `Provider "missing" not found`, diffs);
	for (const name of [
		"nugMissingQuota",
		"nugMissingUsage",
		"nugMissingUsageSummary",
		"nugMissingChannelsHealth",
	]) {
		expectJSONStatus(label, sample, name, 404, diffs);
		expectJSONValue(label, sample, name, "error", `Provider "missing" not found`, diffs);
	}
	expectJSONValue(label, sample, "runtimeCached", "cached", false, diffs);
	expectJSONValue(label, sample, "runtimeCleanupTerminals", "ok", true, diffs);
	for (const name of ["userRuntimeCached", "userRuntimeScan", "userRuntimeCleanup"]) {
		expectJSONStatus(label, sample, name, 403, diffs);
	}
	if (label === "Go") {
		expectJSONArray(label, sample, "runtimeCleanupTerminals", "errors", diffs);
		const explicitGoHealthCapabilities: Array<[string[], unknown]> = [
			[["chapters", "split", "supported"], true],
			[["chapters", "containers", "routeContract", "setup"], "GET /api/chapters/container-setup"],
			[
				["chapters", "containers", "routeContract", "podmanStatus"],
				"GET /api/chapters/podman/status",
			],
			[
				["chapters", "containers", "routeContract", "podmanInstall"],
				"POST /api/chapters/podman/install",
			],
			[
				["chapters", "containers", "routeContract", "composeInfo"],
				"GET /api/chapters/{id}/compose-info",
			],
			[["chapters", "containers", "routeContract", "list"], "GET /api/chapters/{id}/containers"],
			[
				["chapters", "containers", "routeContract", "start"],
				"POST /api/chapters/{id}/containers/start",
			],
			[
				["chapters", "containers", "routeContract", "stop"],
				"POST /api/chapters/{id}/containers/stop",
			],
			[
				["chapters", "containers", "routeContract", "pause"],
				"POST /api/chapters/{id}/containers/pause",
			],
			[
				["chapters", "containers", "routeContract", "unpause"],
				"POST /api/chapters/{id}/containers/unpause",
			],
			[
				["chapters", "containers", "routeContract", "logs"],
				"GET /api/chapters/{id}/containers/logs",
			],
			[
				["chapters", "containers", "routeContract", "remove"],
				"POST /api/chapters/{id}/containers/remove",
			],
			[["terminal", "supported"], true],
			[["vnet", "supported"], true],
			[["vnet", "ws"], true],
			[["vnet", "peerCleanup"], true],
			[["update", "download", "supported"], true],
			[["update", "download", "sse"], true],
			[["update", "download", "sha512"], true],
			[["gateway", "webhook", "supported"], true],
			[["mcp", "transports", "stdio", "supported"], true],
			[["settings", "storage", "supported"], true],
			[["settings", "patch", "supported"], true],
			[["settings", "secretMasking"], true],
			[["settings", "providerModelAugmentation"], true],
			[["settings", "tlsGeneration"], true],
			[["settings", "retryRules"], true],
			[["uploads", "serveNarratorImages", "supported"], true],
			[["uploads", "serveAvatars", "supported"], true],
			[["uploads", "cleanupPreservesMessageImageRefs", "supported"], true],
			[["shares", "create", "supported"], true],
			[["shares", "publicDownload", "supported"], true],
			[["shares", "preview", "supported"], true],
			[["storage", "database", "preview"], true],
			[["benchmark", "containerExecution", "supported"], true],
			[["narrator", "toolInventory", "capabilityContract"], true],
			[["content", "projectRoutines", "supported"], true],
			[["content", "projectSkills", "supported"], true],
		];
		for (const [path, expected] of explicitGoHealthCapabilities) {
			expectJSONPathValue(label, sample, "health", ["capabilities", ...path], expected, diffs);
		}
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "routes", "splitAtCommit"],
			true,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "mode"],
			"split-at-commit",
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "partials", "compressedAISummary", "supported"],
			false,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "partials", "compressedAISummary", "fallback"],
			true,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "partials", "compressedAISummary", "mode"],
			"bounded-ref-copy-with-deterministic-context-note",
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "chapters", "split", "partials", "compressedAISummary", "reason"],
			"Go split compressed context copies bounded message refs and records a deterministic context note instead of generating a TypeScript-equivalent model summary.",
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "storage", "cleanup", "uploads", "preservesMessageImageRefs"],
			true,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "storage", "cleanup", "worktrees", "supported"],
			false,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "storage", "cleanup", "worktrees", "alternative"],
			"runtime.cleanup.worktrees",
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "storage", "cleanup", "containers", "supported"],
			false,
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "storage", "cleanup", "containers", "alternative"],
			"runtime.cleanup.containers",
			diffs,
		);
		expectJSONPathValue(
			label,
			sample,
			"health",
			["capabilities", "runtime", "cleanup", "worktrees", "supported"],
			true,
			diffs,
		);
	}
	expectJSONValue(label, sample, "storageCached", "cached", false, diffs);
	expectJSONStatus(label, sample, "storageVacuum", 409, diffs);
	expectJSONValue(label, sample, "storageVacuum", "vacuumRan", false, diffs);
	for (const name of ["userStorageCached", "userStorageCleanup", "userStorageVacuum"]) {
		expectJSONStatus(label, sample, name, 403, diffs);
	}
	expectJSONValue(label, sample, "updateApply", "success", false, diffs);
	expectJSONValue(label, sample, "updateCleanup", "success", true, diffs);
	expectJSONValue(label, sample, "updateStatus", "ready", false, diffs);
	expectJSONValue(label, sample, "updateCheck", "updateAvailable", false, diffs);
	expectJSONStatus(label, sample, "updateCheckTrusted", 200, diffs);
	expectJSONValue(label, sample, "updateCheckTrusted", "updateAvailable", true, diffs);
	expectJSONValue(label, sample, "updateCheckTrusted", "latestVersion", contractNextVersion, diffs);
	expectJSONPathValue(
		label,
		sample,
		"updateCheckTrusted",
		["releaseInfo", "version"],
		contractNextVersion,
		diffs,
	);
	expectJSONPathValue(
		label,
		sample,
		"updateCheckTrusted",
		["releaseInfo", "path"],
		"narrafork-contract-update.bin",
		diffs,
	);
	expectJSONStatus(label, sample, "userUpdateCheckTrusted", 200, diffs);
	expectJSONValue(label, sample, "userUpdateCheckTrusted", "updateAvailable", true, diffs);
	expectJSONStatus(label, sample, "userUpdateCheck", 200, diffs);
	expectJSONStatus(label, sample, "userUpdateVersion", 200, diffs);
	for (const name of [
		"userUpdateDirectory",
		"userUpdateStatus",
		"userUpdateCleanup",
		"userUpdateApply",
	]) {
		expectJSONStatus(label, sample, name, 403, diffs);
	}
	if (!sample.sse.storageScan?.events.includes("complete")) {
		diffs.push(`semantic.${label}.sse.storageScan.missingComplete`);
	}
	if (sample.sse.userStorageScan?.status !== 403) {
		diffs.push(
			`semantic.${label}.sse.userStorageScan.status expected 403 got ${sample.sse.userStorageScan?.status}`,
		);
	}
	expectJSONStatus(label, sample, "updateCheck", 200, diffs);
	if (sample.sse.updateDownload?.status !== 404) {
		diffs.push(
			`semantic.${label}.sse.updateDownload.status expected 404 got ${sample.sse.updateDownload?.status}`,
		);
	}
	if (sample.sse.updateDownloadTrustedError?.status !== 200) {
		diffs.push(
			`semantic.${label}.sse.updateDownloadTrustedError.status expected 200 got ${sample.sse.updateDownloadTrustedError?.status}`,
		);
	}
	if (sample.sse.updateDownloadTrustedError?.terminal !== "false") {
		diffs.push(
			`semantic.${label}.sse.updateDownloadTrustedError.terminal expected false got ${sample.sse.updateDownloadTrustedError?.terminal}`,
		);
	}
	for (const event of ["progress", "error"]) {
		if (!sample.sse.updateDownloadTrustedError?.events.includes(event)) {
			diffs.push(`semantic.${label}.sse.updateDownloadTrustedError.missingEvent ${event}`);
		}
	}
	if (sample.sse.userUpdateDownload?.status !== 403) {
		diffs.push(
			`semantic.${label}.sse.userUpdateDownload.status expected 403 got ${sample.sse.userUpdateDownload?.status}`,
		);
	}
	for (const name of ["unauthNarrator", "unauthTerminal"]) {
		if (sample.ws[name]?.opened !== false) {
			diffs.push(
				`semantic.${label}.ws.${name}.opened expected false got ${sample.ws[name]?.opened}`,
			);
		}
	}
}

function expectSearchOrderDiffClean(
	ts: BackendSample,
	go: BackendSample,
	name: string,
	diffs: string[],
): void {
	const tsKeys = searchResultKeys(ts, name);
	const goKeys = searchResultKeys(go, name);
	if (tsKeys.join(",") !== goKeys.join(",")) {
		diffs.push(`json.${name}.resultOrder TS=${tsKeys.join(",")} Go=${goKeys.join(",")}`);
	}
}

function summarize(sample: BackendSample): unknown {
	return {
		json: Object.fromEntries(
			Object.entries(sample.json).map(([key, value]) => [
				key,
				{
					status: value.status,
					contentType: value.contentType,
					kind: value.kind,
					keys: value.keys,
					shape: shapeOf(value.body),
				},
			]),
		),
		sse: Object.fromEntries(
			Object.entries(sample.sse).map(([key, value]) => [
				key,
				{
					status: value.status,
					contentType: value.contentType,
					events: value.events,
					terminal: value.terminal,
					bodyPreview: value.bodyPreview.slice(0, 240),
				},
			]),
		),
		ws: sample.ws,
	};
}

function diffSamples(ts: BackendSample, go: BackendSample): string[] {
	const diffs: string[] = [];
	appendSemanticDiffs("TS", ts, diffs);
	appendSemanticDiffs("Go", go, diffs);
	expectSearchOrderDiffClean(ts, go, "searchLargeTopK", diffs);
	expectSearchOrderDiffClean(ts, go, "searchLargeTieBreak", diffs);
	for (const key of Object.keys(ts.json)) {
		const a = ts.json[key];
		const b = go.json[key];
		if (a.status !== b.status) diffs.push(`json.${key}.status TS=${a.status} Go=${b.status}`);
		for (const required of a.keys) {
			if (!b.keys.includes(required)) diffs.push(`json.${key}.missingGoKey ${required}`);
		}
		if (!shapeCovers(a.body, b.body)) {
			diffs.push(`json.${key}.shape differs`);
		}
	}
	for (const key of Object.keys(ts.sse)) {
		const a = ts.sse[key];
		const b = go.sse[key];
		if (a.status !== b.status) diffs.push(`sse.${key}.status TS=${a.status} Go=${b.status}`);
		if (a.events.join(",") !== b.events.join(",")) {
			diffs.push(`sse.${key}.events TS=${a.events.join(",")} Go=${b.events.join(",")}`);
		}
		if (a.terminal !== b.terminal)
			diffs.push(`sse.${key}.terminal TS=${a.terminal} Go=${b.terminal}`);
	}
	for (const key of Object.keys(ts.ws)) {
		const a = ts.ws[key];
		const b = go.ws[key];
		if (a.opened !== b.opened) diffs.push(`ws.${key}.opened TS=${a.opened} Go=${b.opened}`);
		if ((a.firstMessageType ?? "") !== (b.firstMessageType ?? "")) {
			diffs.push(
				`ws.${key}.firstMessageType TS=${a.firstMessageType ?? ""} Go=${b.firstMessageType ?? ""}`,
			);
		}
	}
	for (const name of ["clineSettingsAfterAuthCallback", "clineSettingsAfterBrowserCallback"]) {
		const tsClineAuthProvider = findSettingsProvider(
			ts,
			name,
			"clineProviders",
			"cline-cache-fixture",
		);
		const goClineAuthProvider = findSettingsProvider(
			go,
			name,
			"clineProviders",
			"cline-cache-fixture",
		);
		const tsClineAuthEnabled = Array.isArray(tsClineAuthProvider?.enabledModels)
			? tsClineAuthProvider.enabledModels.join(",")
			: "";
		const goClineAuthEnabled = Array.isArray(goClineAuthProvider?.enabledModels)
			? goClineAuthProvider.enabledModels.join(",")
			: "";
		if (tsClineAuthEnabled !== goClineAuthEnabled) {
			diffs.push(`json.${name}.enabledModels TS=${tsClineAuthEnabled} Go=${goClineAuthEnabled}`);
		}
	}
	for (const name of ["clineProviderModelsAfterRefresh", "clineModelsAfterProviderRefresh"]) {
		const tsIds = sampleJSONArray(ts, name, "models")
			.map((model) => recordBody(model).id)
			.join(",");
		const goIds = sampleJSONArray(go, name, "models")
			.map((model) => recordBody(model).id)
			.join(",");
		if (tsIds !== goIds) {
			diffs.push(`json.${name}.models.ids TS=${tsIds} Go=${goIds}`);
		}
	}
	return diffs;
}

async function main() {
	const home = mkdtempSync(join(tmpdir(), "narrafork-ts-go-contract-"));
	const tsPort = await freePort();
	const goPort = await freePort();
	const tsUrl = `http://127.0.0.1:${tsPort}`;
	const goUrl = `http://127.0.0.1:${goPort}`;
	let tsProc: Bun.Subprocess | undefined;
	let goProc: Bun.Subprocess | undefined;
	const updateFixture = startUpdateFixtureServer();
	const providerFixture = startProviderModelFixtureServer();
	const openAIBaseUrl = `${providerFixture.url}/openai/v1`;
	const anthropicBaseUrl = `${providerFixture.url}/anthropic`;
	const nkpBaseUrl = `${providerFixture.url}/nkp`;
	const nugBaseUrl = `${providerFixture.url}/nug`;
	try {
		seedPrestartProviderFixtures(home);
		const tsFetchPreload = writeTsFetchPreload(home);
		tsProc = spawnTS(home, tsPort, tsFetchPreload, providerFixture.url);
		await waitHealth(tsUrl, "TypeScript backend");
		const token = await registerAdmin(tsUrl);
		const userToken = await registerUser(tsUrl, "regular");
		await seedSettingsSecrets(
			tsUrl,
			token,
			openAIBaseUrl,
			anthropicBaseUrl,
			nkpBaseUrl,
			nugBaseUrl,
		);
		seedSearchFixture(home);
		resetProjectCreateFixture(home);
		resetChapterCreateFixture(home);
		resetChapterDormantWakeFixture(home);
		seedProjectRealCleanupWarningFixture(home);
		seedProjectDeleteFixture(home);
		const ts = await collect(
			tsUrl,
			token,
			userToken,
			home,
			updateFixture.url,
			openAIBaseUrl,
			anthropicBaseUrl,
			nkpBaseUrl,
			nugBaseUrl,
		);
		await stopProcess(tsProc);
		tsProc = undefined;
		seedClineModelCacheFixture(home);
		seedChapterDeleteFixture(home);
		resetProjectCreateFixture(home);
		resetChapterCreateFixture(home);
		resetChapterDormantWakeFixture(home);
		seedProjectRealCleanupWarningFixture(home);
		seedProjectDeleteFixture(home);

		const goBinary = await buildGoBinary(home);
		goProc = spawnGo(home, goPort, goBinary, providerFixture.url);
		try {
			await waitHealth(goUrl, "Go backend");
		} catch (err) {
			await stopProcess(goProc);
			const logs = await processLogs(goProc);
			throw new Error(
				`${err instanceof Error ? err.message : String(err)}\nGo stdout:\n${logs.stdout}\nGo stderr:\n${logs.stderr}`,
			);
		}
		const go = await collect(
			goUrl,
			token,
			userToken,
			home,
			updateFixture.url,
			openAIBaseUrl,
			anthropicBaseUrl,
			nkpBaseUrl,
			nugBaseUrl,
		);
		const diffs = diffSamples(ts, go);
		console.log(
			JSON.stringify(
				{ ok: true, tsUrl, goUrl, diffs, ts: summarize(ts), go: summarize(go) },
				null,
				2,
			),
		);
	} finally {
		if (tsProc) await stopProcess(tsProc);
		if (goProc) await stopProcess(goProc);
		updateFixture.stop();
		providerFixture.stop();
		if (process.env.NARRAFORK_KEEP_CONTRACT_HOME !== "1") {
			rmSync(home, { recursive: true, force: true });
		}
	}
}

await main();
