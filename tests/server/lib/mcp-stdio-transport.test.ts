import { describe, expect, it } from "bun:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { createTransport } from "../../../server/lib/mcp/transports";

// A real stdio peer: each flood writes exactly 8 MiB, one chunk at a time,
// respecting backpressure. Replies cannot be sent until stderr has drained.
const serverSource = `
const readline = require("node:readline");
const chunk = "x".repeat(64 * 1024);
function flood(remaining, done) {
	if (remaining === 0) return done();
	process.stderr.write(chunk, () => flood(remaining - 1, done));
}
readline.createInterface({ input: process.stdin }).on("line", (line) => {
	const message = JSON.parse(line);
	if (message.id === undefined) return;
	let result;
	switch (message.method) {
		case "initialize":
			result = {
				protocolVersion: message.params.protocolVersion,
				capabilities: { tools: {} },
				serverInfo: { name: "stderr-fixture", version: "1" },
			};
			break;
		case "tools/list":
			result = { tools: [{ name: "probe", inputSchema: { type: "object" } }] };
			break;
		case "tools/call":
			result = { content: [{ type: "text", text: "ok" }] };
			break;
		default:
			result = {};
	}
	const reply = () => process.stdout.write(
		JSON.stringify({ jsonrpc: "2.0", id: message.id, result }) + "\\n",
	);
	if (message.method === floodMethod) flood(128, reply);
	else reply();
});
`;

const requestOptions = { timeout: 2_000 };

describe("stdio MCP stderr backpressure", () => {
	for (const floodMethod of ["initialize", "tools/call"]) {
		it(`stays responsive after heavy stderr output during ${floodMethod}`, async () => {
			const transport = createTransport({
				id: "stderr-fixture",
				name: "stderr-fixture",
				transport: "stdio",
				command: process.execPath,
				args: ["-e", `const floodMethod = ${JSON.stringify(floodMethod)};\n${serverSource}`],
				enabled: true,
			});
			const client = new Client({ name: "stderr-test", version: "1" }, { capabilities: {} });
			try {
				await client.connect(transport, requestOptions);
				expect((await client.listTools(undefined, requestOptions)).tools).toHaveLength(1);
				for (let i = 0; i < 2; i++) {
					const result = await client.callTool(
						{ name: "probe", arguments: {} },
						undefined,
						requestOptions,
					);
					expect(result.content).toEqual([{ type: "text", text: "ok" }]);
				}
				// Protocol traffic is still intact after the logs, not just the first reply.
				await client.ping(requestOptions);
				expect((await client.listTools(undefined, requestOptions)).tools[0]?.name).toBe("probe");
			} finally {
				// Close even when initialize rejects; only this test's child is affected.
				await transport.close();
			}
		}, 15_000);
	}
});
