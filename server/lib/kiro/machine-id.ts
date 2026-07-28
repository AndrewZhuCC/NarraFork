// Machine ID generation — reverse-engineered from kiro CLI chat

import { createHash } from "node:crypto";
import type { KiroConfig, KiroCredentials } from "./types";

function normalizeMachineId(machineId: string): string | null {
	const trimmed = machineId.trim();

	// Already 64-char hex
	if (trimmed.length === 64 && /^[0-9a-fA-F]+$/.test(trimmed)) {
		return trimmed;
	}

	// UUID format — strip dashes, repeat to 64 chars
	const withoutDashes = trimmed.split("-").join("");
	if (withoutDashes.length === 32 && /^[0-9a-fA-F]+$/.test(withoutDashes)) {
		return `${withoutDashes}${withoutDashes}`;
	}

	return null;
}

function sha256Hex(input: string): string {
	return createHash("sha256").update(input).digest("hex");
}

/**
 * Generate machineId from credentials and config.
 * Priority: cred.machineId > config.machineId > SHA256("KotlinNativeAPI/" + secret)
 * where secret is the refreshToken, or the static API key for api_key credentials.
 */
export function generateMachineId(creds: KiroCredentials, config: KiroConfig): string | null {
	if (creds.machineId) {
		const normalized = normalizeMachineId(creds.machineId);
		if (normalized) return normalized;
	}

	if (config.machineId) {
		const normalized = normalizeMachineId(config.machineId);
		if (normalized) return normalized;
	}

	const secret = creds.refreshToken || creds.apiKey;
	if (secret) {
		return sha256Hex(`KotlinNativeAPI/${secret}`);
	}

	return null;
}
