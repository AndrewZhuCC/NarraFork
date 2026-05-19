#!/usr/bin/env bun

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { initKiro, kiroChatRaw, kiroSnapshot, type KiroChatRequest } from "../../server/lib/kiro-adapter";

function readStdin(): string {
	return readFileSync(0, "utf8");
}

function writeEvent(event: { eventType: string; data: Record<string, unknown> }): void {
	process.stdout.write(`${JSON.stringify(event)}\n`);
}

function sanitizedKiroDiagnostic(): string | undefined {
	if (process.env.NARRAFORK_KIRO_BRIDGE_DIAGNOSTICS !== "1") return undefined;
	const snapshot = kiroSnapshot();
	if (!snapshot) return "Kiro snapshot unavailable";
	return JSON.stringify({
		total: snapshot.total,
		available: snapshot.available,
		entries: snapshot.entries.map((entry) => ({
			id: entry.id,
			disabled: entry.disabled,
			disabledReason: entry.disabledReason,
			failureCount: entry.failureCount,
			expiresAt: entry.expiresAt,
			subscriptionTitle: entry.subscriptionTitle,
			hasProfileArn: entry.hasProfileArn,
			availableModels: entry.availableModels,
		})),
	});
}

async function main(): Promise<void> {
	const input = readStdin().trim();
	if (!input) {
		throw new Error("Kiro bridge received empty stdin");
	}
	const request = JSON.parse(input) as KiroChatRequest;
	const credentialsPath = process.env.NARRAFORK_KIRO_CREDENTIALS_PATH;
	const configPath = process.env.NARRAFORK_KIRO_CONFIG_PATH;
	const defaultModel = process.env.NARRAFORK_KIRO_DEFAULT_MODEL;
	if (credentialsPath && configPath) {
		initKiro({
			credentialsPath: resolve(credentialsPath),
			configPath: resolve(configPath),
			...(defaultModel ? { defaultModel: defaultModel as never } : {}),
		});
	}
	for await (const event of kiroChatRaw(request)) {
		writeEvent(event);
	}
}

main().catch((error) => {
	process.stderr.write(`${error instanceof Error ? error.stack || error.message : String(error)}\n`);
	const diagnostic = sanitizedKiroDiagnostic();
	if (diagnostic) {
		process.stderr.write(`Kiro bridge diagnostic: ${diagnostic}\n`);
	}
	process.exit(1);
});
