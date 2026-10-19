import type {
	RecordingIdentity,
	RecordingPinReason,
	RecordingSettings,
} from "../../shared/request-recording";
import type { DiskSafetySettings } from "./disk-safety-config";

export const REQUEST_RECORDING_DIR = "request-recordings";
export const REQUEST_RECORDING_SAVED_DIR = "saved-request-recordings";
export const RECORDING_CHUNK_BYTES = 64 * 1024;
export const RECORDING_QUEUE_BYTES = 4 * 1024 * 1024;
export const RECORDING_SEGMENT_BYTES = 256 * 1024;
export const RECORDING_MAX_RECORDS = 2048;
export const RECORDING_MAX_SEGMENTS = 16384;
export const RECORDING_METADATA_BYTES = 4 * 1024 * 1024;
export const RECORDING_ARCHIVE_BYTES = 512 * 1024 * 1024;
export const RECORDING_MANIFEST_BYTES = 8 * 1024 * 1024;
export const RECORDING_SAVE_LIMIT = 10000;
export const RECORDING_DOWNLOAD_LEASE_MS = 120_000;
export const RECORDING_MAX_DOWNLOADS = 4;

export type RecordingSide = "request" | "response";
export type RecorderOperation =
	| { type: "configure"; settings: RecordingSettings; diskSafety?: DiskSafetySettings }
	| { type: "begin"; identity: RecordingIdentity; continuous: boolean; errorsOnly: boolean }
	| { type: "meta"; requestId: string; side: RecordingSide; value: Record<string, unknown> }
	| {
			type: "chunk";
			requestId: string;
			side: RecordingSide;
			kind: string;
			data: Uint8Array;
			packet: number;
			offset: number;
			total: number;
			time: string;
	  }
	| { type: "gap"; requestId: string; side: RecordingSide; bytes: number; reason: string }
	| { type: "end"; requestId: string; error?: string; status?: string }
	| { type: "bind"; requestId: string; apiRequestId: string }
	| { type: "pin"; requestId: string; reason: RecordingPinReason }
	| { type: "save"; narratorId: string }
	| { type: "detail"; narratorId: string; requestId: string }
	| { type: "save-specific"; narratorId: string; requestId: string }
	| { type: "list"; narratorId: string; cursor?: string; limit?: number }
	| { type: "delete"; narratorId: string; saveId: string }
	| { type: "archive"; narratorId: string; saveId: string }
	| { type: "flush" };

export interface RecorderMessage {
	id: number;
	op: RecorderOperation;
}
export interface RecorderReply {
	id: number;
	value?: unknown;
	error?: { message: string; status: number; code: string };
}
