import { parentPort } from "node:worker_threads";
import type { TokenRepairProtocolOverride } from "@shared/usage-token-repair";
import { TokenRepairRunner } from "./usage-token-repair-runner";
import { createTokenRepairStore, type TokenRepairStoreConfig } from "./usage-token-repair-store";

if (!parentPort) throw new Error("Token repair worker requires a parent");
let runner: TokenRepairRunner | undefined;
let initialized = false;
parentPort.on(
	"message",
	async (request: {
		id: number;
		op: string;
		config?: TokenRepairStoreConfig;
		directory?: string;
		jobId?: string;
		userId?: string;
		includeReviewed?: boolean;
		protocolOverrides?: TokenRepairProtocolOverride[];
		sourceJobId?: string;
		cursor?: string;
		candidateIds?: string[];
	}) => {
		try {
			let value: unknown;
			if (request.op === "init") {
				if (initialized || !request.config || !request.directory)
					throw new Error("Invalid initialization");
				initialized = true;
				const store = createTokenRepairStore(request.config);
				runner = new TokenRepairRunner(store, request.directory);
				value = true;
			} else {
				if (!runner) throw new Error("Token repair worker is not initialized");
				switch (request.op) {
					case "probe":
						value = await runner.store.probe();
						break;
					case "latest":
						value = await runner.store.latestJob();
						break;
					case "get":
						value = await runner.store.getJob(request.jobId ?? "");
						break;
					case "list":
						value = await runner.store.listCandidates(request.jobId ?? "", request.cursor);
						break;
					case "start":
						value = await runner.start(
							request.userId ?? "",
							request.includeReviewed === true,
							request.protocolOverrides,
							request.sourceJobId,
						);
						break;
					case "cancel":
						value = await runner.cancel(request.jobId ?? "");
						break;
					case "apply":
						value = await runner.apply(
							request.jobId ?? "",
							request.candidateIds ?? [],
							request.userId ?? "",
						);
						break;
					case "close":
						await runner.close();
						value = true;
						break;
					default:
						throw new Error("Unknown token repair operation");
				}
			}
			parentPort?.postMessage({ id: request.id, value });
		} catch {
			// Do not disclose PostgreSQL URLs, SQL, dump paths or raw evidence in API errors.
			parentPort?.postMessage({
				id: request.id,
				error:
					"Token repair operation could not complete. Check job status and retry only after reviewing committed results.",
			});
		}
	},
);
parentPort.postMessage({ ready: "token-repair-v1" });
