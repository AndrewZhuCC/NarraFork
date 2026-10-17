export const DEFAULT_RECORDING_REQUEST_BYTES = 16 * 1024 * 1024;
export const DEFAULT_RECORDING_RESPONSE_BYTES = 128 * 1024 * 1024;
// A nonzero window must fit the per-frame identity/order envelope as well as payload bytes.
export const MIN_RECORDING_WINDOW_BYTES = 1024;
export const MAX_RECORDING_REQUEST_BYTES = 512 * 1024 * 1024;
export const MAX_RECORDING_RESPONSE_BYTES = 2 * 1024 * 1024 * 1024;
export const RECORDING_SCHEMA = "narrafork.request-recording.v1" as const;

export interface RecordingIdentity {
	requestId: string;
	narratorId: string;
	userId?: string | null;
	provider: string;
	model: string;
	kind?: string;
	apiRequestId?: string;
}

export interface RecordingRequest {
	transport?: string;
	url?: string;
	method?: string;
	headers?: Record<string, string>;
	bodyText?: string;
}

export interface RecordingFrame {
	direction: "request" | "response";
	type: string;
	text?: string | Uint8Array;
	/** Bytes were lost before reaching the recorder, not merely from a legacy preview. */
	sourceTruncated?: "plugin" | "host";
	metadata?: Record<string, unknown>;
}

/** Why a recording left the ring window and became a pinned save. */
export type RecordingPinReason =
	| "manual"
	| "request"
	| "continuous"
	| "error"
	| "malformed"
	| "leak";

export interface RequestRecording {
	readonly requestId: string;
	/** Pin this request when it ends (immediately if it already ended). */
	pin(reason: RecordingPinReason): void;
	request(request: RecordingRequest): void;
	responseMeta(meta: Record<string, unknown>): void;
	responseChunk(value: string | Uint8Array): void;
	frame(frame: RecordingFrame): void;
	finish(result?: { error?: string; status?: string }): void;
}

export interface SavedRecording {
	id: string;
	narratorId: string;
	createdAt: string;
	requestIds: string[];
	apiRequestIds: string[];
	requestCount: number;
	bytes: number;
	partial: boolean;
	truncated: boolean;
	continuous?: boolean;
	/** Absent on saves written before pin reasons existed. */
	reason?: RecordingPinReason;
}

export interface RequestRecordingDetail {
	requestId: string;
	narratorId: string;
	apiRequestId?: string;
	provider: string;
	model: string;
	startedAt: string;
	endedAt?: string;
	status?: string;
	error?: string;
	partial: boolean;
	truncated: boolean;
	requestPreview: string;
	responsePreview: string;
	previewTruncated: { request: boolean; response: boolean };
	savedRecording?: SavedRecording;
	apiRequest?: {
		id: string;
		createdAt: string;
		durationMs: number | null;
		ttftMs: number | null;
		errorMessage: string | null;
	};
}

/** Which finished requests are pinned automatically, beyond explicit saves. */
export type RequestEvidenceAutoPin = "off" | "errors" | "all";
export const REQUEST_EVIDENCE_AUTO_PIN_VALUES = ["off", "errors", "all"] as const;

/** `settings.requestEvidence`: the single configuration for request evidence. */
export interface RecordingSettings {
	/** Bounded instance-wide ring window of recent requests. */
	ringEnabled: boolean;
	/** Ring request-side capacity in bytes; 0 disables that side of the ring. */
	ringRequestBytes: number;
	/** Ring response-side capacity in bytes; 0 disables that side of the ring. */
	ringResponseBytes: number;
	/** `all` pins every request from its start; `errors` pins failed ones when they end. */
	autoPin: RequestEvidenceAutoPin;
}
