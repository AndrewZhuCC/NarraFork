import { expect, test } from "bun:test";
import { fileURLToPath } from "node:url";

// Module mocks live in a child process: no DB or real device connection is opened.
test("device rollback removes still go out after the request is aborted", async () => {
	const portPath = fileURLToPath(new URL("./device-port.ts", import.meta.url));
	const connectionPath = fileURLToPath(new URL("../device-connection-service.ts", import.meta.url));
	const transferPath = fileURLToPath(new URL("../device-transfer-service.ts", import.meta.url));
	const settingsPath = fileURLToPath(new URL("../../lib/settings/index.ts", import.meta.url));
	const source = `
import { mock } from "bun:test";
import assert from "node:assert/strict";
const calls = [];
mock.module(${JSON.stringify(settingsPath)}, () => ({ settings: { devices: { maxRpcBytes: 65536 } } }));
mock.module(${JSON.stringify(transferPath)}, () => ({
 downloadFile: async () => {}, uploadFile: async () => {}, statRemote: async () => ({ exists: false }),
}));
mock.module(${JSON.stringify(connectionPath)}, () => ({
 isDeviceOnline: () => true,
 getConnectedDeviceHello: () => ({ platform: { os: "linux" }, defaultCwd: "/home/u" }),
 sendRpc: async (id, method, params, opts) => {
  calls.push({ method, aborted: opts?.signal?.aborted === true, hasSignal: !!opts?.signal, timeoutMs: opts?.timeoutMs });
  // Mirrors the real sendRpc: an aborted signal rejects immediately.
  if (opts?.signal?.aborted) throw new Error("aborted");
  return {};
 },
}));
const { createDeviceSessionFsPort } = await import(${JSON.stringify(portPath)});
const controller = new AbortController();
const port = createDeviceSessionFsPort("dev", controller.signal);
controller.abort();
await assert.rejects(port.write("/r/a.jsonl", new Uint8Array([1])), /aborted/);
await port.remove("/r/a.jsonl");
const remove = calls.find((c) => c.method === "fs.remove");
assert.ok(remove, "remove was sent");
assert.equal(remove.hasSignal, false);
assert.ok(remove.timeoutMs > 0, "remove is still bounded by a timeout");
console.log("OK");
`;
	const proc = Bun.spawn([process.execPath, "--eval", source], {
		stdout: "pipe",
		stderr: "pipe",
		env: { ...process.env, NARRAFORK_ALLOW_MULTIPLE: "1" },
	});
	const timeout = setTimeout(() => proc.kill(), 10_000);
	// Bounded like every other child-process read in this repo.
	const collect = async (stream: ReadableStream<Uint8Array>) => {
		const chunks: Uint8Array[] = [];
		let size = 0;
		for await (const chunk of stream) {
			size += chunk.byteLength;
			if (size > 65_536) {
				proc.kill();
				throw new Error("test output limit");
			}
			chunks.push(chunk);
		}
		return Buffer.concat(chunks).toString("utf8");
	};
	const [stdout, stderr, code] = await Promise.all([
		collect(proc.stdout),
		collect(proc.stderr),
		proc.exited,
	]).finally(() => clearTimeout(timeout));
	expect({ code, stdout: stdout.trim(), stderr: code === 0 ? "" : stderr }).toEqual({
		code: 0,
		stdout: "OK",
		stderr: "",
	});
});
