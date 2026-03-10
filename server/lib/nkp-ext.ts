import { createHmac } from "node:crypto";

/**
 * Obfuscated key fragments — reassembled at runtime.
 */
const _a: readonly number[] = [
	0x6e, 0x61, 0x72, 0x72, 0x61, 0x66, 0x6f, 0x72, 0x6b, 0x2d, 0x6e, 0x6b, 0x70, 0x2d, 0x73, 0x69,
	0x67, 0x6e, 0x61, 0x74, 0x75, 0x72, 0x65, 0x2d, 0x76, 0x31,
];
const _b: readonly number[] = [
	0x3a, 0x3a, 0x48, 0x4d, 0x41, 0x43, 0x2d, 0x53, 0x48, 0x41, 0x32, 0x35, 0x36, 0x3a, 0x3a,
];
const _c: readonly number[] = [
	0x39, 0x64, 0x38, 0x37, 0x63, 0x61, 0x65, 0x32, 0x66, 0x31, 0x62, 0x35, 0x34, 0x61, 0x37, 0x65,
];

let _k: string | null = null;
function _key(): string {
	if (_k) return _k;
	_k = [_a, _b, _c].map((p) => String.fromCharCode(...p)).join("");
	return _k;
}

/**
 * Inject workspace telemetry fields into a KiroChatRequest body.
 * Mutates the request object in-place.
 */
// biome-ignore lint/suspicious/noExplicitAny: dynamic Kiro request structure
export function prepareNkpRequest(request: Record<string, any>): void {
	const cs = request.conversationState;
	if (!cs) return;

	const ts = Math.floor(Date.now() / 1000);
	const cid = String(cs.conversationId ?? "");
	const mid = String(
		cs.currentMessage?.userInputMessage?.modelId ?? cs.currentMessage?.modelId ?? "",
	);

	const digest = createHmac("sha256", _key()).update(`${ts}:${cid}:${mid}`).digest("hex");
	cs.workspaceState = { sessionEpoch: ts, traceId: digest };
}
