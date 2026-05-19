import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repoRoot = resolve(import.meta.dir, "..", "..");
const goRoot = resolve(repoRoot, "go_backend");

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

function envFor(home: string, port: number): Record<string, string> {
	return {
		...process.env,
		HOME: home,
		NARRAFORK_ALLOW_MULTIPLE: "1",
		NARRAFORK_DISABLE_OPEN_BROWSER: "1",
		HOST: "127.0.0.1",
		PORT: String(port),
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

function spawnTS(home: string, port: number): Bun.Subprocess {
	return Bun.spawn(["bun", "server/main.ts", `--port=${port}`, "--host=127.0.0.1"], {
		cwd: repoRoot,
		env: envFor(home, port),
		stdout: "pipe",
		stderr: "pipe",
	});
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

function spawnGo(home: string, port: number, binPath: string): Bun.Subprocess {
	return Bun.spawn([binPath, "-port", String(port), "-host", "127.0.0.1"], {
		cwd: goRoot,
		env: envFor(home, port),
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

async function registerAdmin(baseUrl: string): Promise<string> {
	const res = await fetch(`${baseUrl}/api/auth/register`, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ username: "admin", password: "password123", language: "en" }),
	});
	if (!res.ok) throw new Error(`register failed: ${res.status} ${await res.text()}`);
	const body = (await res.json()) as { token?: string };
	if (!body.token) throw new Error("register response did not include token");
	return body.token;
}

function bodyKind(body: unknown): string {
	if (Array.isArray(body)) return "array";
	if (body && typeof body === "object") return "object";
	return typeof body;
}

async function sampleJSON(baseUrl: string, path: string, token?: string): Promise<JsonSample> {
	const res = await fetch(`${baseUrl}${path}`, {
		headers: token ? { Authorization: `Bearer ${token}` } : undefined,
	});
	const contentType = res.headers.get("content-type") ?? "";
	let body: unknown = null;
	try {
		body = await res.json();
	} catch {
		body = await res.text();
	}
	return {
		status: res.status,
		contentType,
		keys: body && typeof body === "object" && !Array.isArray(body) ? Object.keys(body).sort() : [],
		kind: bodyKind(body),
		body,
	};
}

async function sampleSSE(baseUrl: string, path: string, token: string): Promise<SseSample> {
	const res = await fetch(`${baseUrl}${path}`, {
		method: "POST",
		headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
		body: JSON.stringify({}),
	});
	const contentType = res.headers.get("content-type") ?? "";
	const text = await res.text();
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

async function sampleWS(baseUrl: string, path: string, token: string): Promise<WsSample> {
	const wsUrl = `${baseUrl.replace(/^http/, "ws")}${path}?token=${encodeURIComponent(token)}`;
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

async function collect(baseUrl: string, token: string): Promise<BackendSample> {
	return {
		json: {
			health: await sampleJSON(baseUrl, "/api/health"),
			authStatus: await sampleJSON(baseUrl, "/api/auth/status"),
			projects: await sampleJSON(baseUrl, "/api/projects", token),
			settings: await sampleJSON(baseUrl, "/api/settings", token),
			search: await sampleJSON(
				baseUrl,
				"/api/search?q=contract&entities=chapters,messages,narrators",
				token,
			),
			updateCheck: await sampleJSON(baseUrl, "/api/update/check", token),
		},
		sse: {
			updateDownload: await sampleSSE(baseUrl, "/api/update/download", token),
		},
		ws: {
			narrator: await sampleWS(baseUrl, "/ws/narrator", token),
			terminal: await sampleWS(baseUrl, "/ws/terminal", token),
		},
	};
}

function shapeOf(value: unknown): unknown {
	if (Array.isArray(value)) return "array";
	if (!value || typeof value !== "object") return typeof value;
	return Object.fromEntries(
		Object.entries(value).map(([key, child]) => [
			key,
			Array.isArray(child) ? "array" : typeof child,
		]),
	);
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
	for (const key of Object.keys(ts.json)) {
		const a = ts.json[key];
		const b = go.json[key];
		if (a.status !== b.status) diffs.push(`json.${key}.status TS=${a.status} Go=${b.status}`);
		for (const required of a.keys) {
			if (!b.keys.includes(required)) diffs.push(`json.${key}.missingGoKey ${required}`);
		}
		if (JSON.stringify(shapeOf(a.body)) !== JSON.stringify(shapeOf(b.body))) {
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
	try {
		tsProc = spawnTS(home, tsPort);
		await waitHealth(tsUrl, "TypeScript backend");
		const token = await registerAdmin(tsUrl);
		const ts = await collect(tsUrl, token);
		await stopProcess(tsProc);
		tsProc = undefined;

		const goBinary = await buildGoBinary(home);
		goProc = spawnGo(home, goPort, goBinary);
		try {
			await waitHealth(goUrl, "Go backend");
		} catch (err) {
			await stopProcess(goProc);
			const logs = await processLogs(goProc);
			throw new Error(
				`${err instanceof Error ? err.message : String(err)}\nGo stdout:\n${logs.stdout}\nGo stderr:\n${logs.stderr}`,
			);
		}
		const go = await collect(goUrl, token);
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
		if (process.env.NARRAFORK_KEEP_CONTRACT_HOME !== "1") {
			rmSync(home, { recursive: true, force: true });
		}
	}
}

await main();
