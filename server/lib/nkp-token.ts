import { createHash } from "node:crypto";

/**
 * Build a request token for NKP chat requests.
 * Encodes timestamp and a short hash of the API key into a base64 token.
 */
export function buildNkpRequestToken(apiKey: string): string {
	const ts = Math.floor(Date.now() / 1000);
	const hash = createHash("md5").update(`${apiKey}${ts}`).digest("hex").slice(0, 16);
	return Buffer.from(`${ts}:${hash}`).toString("base64");
}
