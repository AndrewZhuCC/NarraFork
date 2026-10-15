import { parentPort } from "node:worker_threads";
import type { TokenRepairProtocolOverride } from "@shared/usage-token-repair";
import { analyzeTokenRepairEvidence } from "../lib/usage-token-repair-evidence";
import type { RepairRequestRow } from "./usage-token-repair-store";

if (!parentPort) throw new Error("Token repair evidence worker requires a parent");
parentPort.once(
	"message",
	async (input: {
		row: RepairRequestRow;
		rawDumpJson: string | null;
		dumpDirectory: string;
		protocolHint?: TokenRepairProtocolOverride["protocol"];
	}) => {
		try {
			const result = await analyzeTokenRepairEvidence(input.row, input.rawDumpJson, {
				dumpDirectory: input.dumpDirectory,
				protocolHint: input.protocolHint,
			});
			parentPort?.postMessage({ result });
		} catch {
			parentPort?.postMessage({ error: "Historical request evidence could not be read safely" });
		}
	},
);
parentPort.postMessage({ ready: "token-repair-evidence-v1" });
