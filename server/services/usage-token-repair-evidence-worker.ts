import { parentPort } from "node:worker_threads";
import type { TokenRepairProtocolOverride } from "@shared/usage-token-repair";
import { analyzeTokenRepairEvidence } from "../lib/usage-token-repair-evidence";
import type { RepairRequestRow } from "./usage-token-repair-store";

if (!parentPort) throw new Error("Token repair evidence worker requires a parent");
parentPort.on(
	"message",
	async (input: {
		id: number;
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
			parentPort?.postMessage({ id: input.id, result });
		} catch {
			parentPort?.postMessage({
				id: input.id,
				error: "Historical request evidence could not be read safely",
			});
		}
	},
);
parentPort.postMessage({ ready: "token-repair-evidence-v1" });
