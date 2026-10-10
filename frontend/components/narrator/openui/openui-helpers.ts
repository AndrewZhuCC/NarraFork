import type { ActionEvent, ParseResult } from "@openuidev/react-lang";

export const MAX_OPENUI_SOURCE_BYTES = 64 * 1024;
export const MAX_OPENUI_STATE_BYTES = 16 * 1024;
export const MAX_OPENUI_MESSAGE_BYTES = 16 * 1024;
export const MAX_OPENUI_SESSIONS = 64;
export const MAX_OPENUI_FIELDS = 128;
const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function withinUtf8Limit(value: string, maxBytes: number): boolean {
	return value.length <= maxBytes && encoder.encode(value).byteLength <= maxBytes;
}

export function truncateUtf8(value: string, maxBytes: number): string {
	// Slice before encoding so even an enormous input only allocates bounded buffers.
	const bytes = encoder.encode(value.slice(0, maxBytes));
	if (bytes.byteLength <= maxBytes) return decoder.decode(bytes);
	let end = maxBytes;
	while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
	return decoder.decode(bytes.subarray(0, end));
}

function isRecord(value: unknown): value is Record<string, unknown> {
	if (!value || typeof value !== "object" || Array.isArray(value)) return false;
	const prototype = Object.getPrototypeOf(value);
	return prototype === null || prototype === Object.prototype;
}

/** Bound traversal before JSON.stringify, not just the resulting JSON text. */
function boundedSnapshot(
	value: unknown,
	maxBytes: number,
	unwrapFields = false,
): { json: string; value: unknown } | null {
	let remaining = maxBytes;
	let nodes = 1024;
	function clone(current: unknown, depth: number): unknown {
		if (--nodes < 0 || depth > 12 || --remaining < 0) throw new Error("limit");
		if (current === null || typeof current === "boolean") return current;
		if (typeof current === "string") {
			remaining -= current.length;
			if (remaining < 0) throw new Error("limit");
			return current;
		}
		if (typeof current === "number") return Number.isFinite(current) ? current : null;
		if (current instanceof Date) {
			remaining -= current.toISOString().length;
			if (remaining < 0) throw new Error("limit");
			return new Date(current.getTime());
		}
		if (Array.isArray(current)) {
			if (current.length > nodes) throw new Error("limit");
			return current.map((item) => clone(item, depth + 1));
		}
		if (!isRecord(current)) return undefined;
		if (
			unwrapFields &&
			Object.hasOwn(current, "value") &&
			Object.hasOwn(current, "componentType")
		) {
			return clone(current.value, depth + 1);
		}
		const entries: [string, unknown][] = [];
		for (const key in current) {
			if (!Object.hasOwn(current, key) || (unwrapFields && key === "componentType")) continue;
			remaining -= key.length;
			if (remaining < 0) throw new Error("limit");
			const item = clone(current[key], depth + 1);
			if (item !== undefined) entries.push([key, item]);
		}
		return Object.fromEntries(entries);
	}
	try {
		const snapshot = clone(value, 0);
		const json = JSON.stringify(snapshot);
		return json !== undefined && withinUtf8Limit(json, maxBytes) ? { json, value: snapshot } : null;
	} catch {
		return null;
	}
}

/** In-memory, tab-lifetime LRU; isolated clones retain DatePicker's Date values. */
export function createOpenUIStateCache() {
	const entries = new Map<string, Record<string, unknown>>();
	return {
		load(key: string): Record<string, unknown> | undefined {
			const snapshot = entries.get(key);
			if (!snapshot) return undefined;
			entries.delete(key);
			entries.set(key, snapshot);
			return structuredClone(snapshot);
		},
		save(key: string, state: Record<string, unknown>): boolean {
			if (!key || key.length > 1024) return false;
			const snapshot = boundedSnapshot(state, MAX_OPENUI_STATE_BYTES);
			entries.delete(key);
			if (snapshot === null || !isRecord(snapshot.value)) return false;
			entries.set(key, snapshot.value);
			if (entries.size > MAX_OPENUI_SESSIONS) {
				const oldest = entries.keys().next().value;
				if (oldest !== undefined) entries.delete(oldest);
			}
			return true;
		},
	};
}

export const OPENUI_FIELD_TYPES = new Set([
	"Input",
	"TextArea",
	"Select",
	"DatePicker",
	"Slider",
	"CheckBoxGroup",
	"RadioGroup",
	"SwitchGroup",
	"Chips",
	"OptionCards",
	"EditableTable",
]);

export function openUIFieldKey(formName: string | undefined, name: string): string {
	return JSON.stringify([formName ?? "", name]);
}
export interface OpenUIControlEvidence {
	label: string;
	visible: boolean;
	value?: unknown;
	unsupported?: boolean;
}

export type FormProjectionResult =
	| { ok: true; values: Record<string, unknown> }
	| { ok: false; reason: "unsupportedForm" | "messageTooLarge" };

/** Project rendered fields from SDK state; never forward the entire binding store. */
export function projectOpenUIFormValues(
	parsed: ParseResult | null,
	state: Record<string, unknown> | undefined,
	event: ActionEvent,
	controls: ReadonlyMap<string, OpenUIControlEvidence>,
): FormProjectionResult {
	if (!parsed?.root || parsed.meta.incomplete || parsed.meta.errors.length) {
		return { ok: false, reason: "unsupportedForm" };
	}
	const values = new Map<string, unknown>();
	let nodes = 0;
	function walk(node: unknown, formName: string | undefined, depth: number): void {
		if (++nodes > 512 || depth > 20) throw new Error("messageTooLarge");
		if (Array.isArray(node)) {
			for (const child of node) walk(child, formName, depth + 1);
			return;
		}
		if (!isRecord(node) || node.type !== "element" || !isRecord(node.props)) return;
		const props = node.props;
		if (node.typeName === "Form") {
			if (typeof props.name !== "string") throw new Error("unsupportedForm");
			walk(props.fields, props.name, depth + 1);
			return;
		}
		const typeName = String(node.typeName);
		if (OPENUI_FIELD_TYPES.has(typeName)) {
			const name = props.name;
			if (typeof name !== "string" || !name || name.length > 256)
				throw new Error("unsupportedForm");
			const scope = typeName === "EditableTable" ? name : formName;
			if (event.formName && event.formName !== scope) return;
			const evidence = controls.get(openUIFieldKey(scope, name));
			if (!evidence || evidence.unsupported) throw new Error("unsupportedForm");
			if (!evidence.visible) return;
			const payload = event.formState ?? state;
			const fields = scope && payload ? payload[scope] : payload;
			let value: unknown;
			if (Object.hasOwn(evidence, "value")) {
				value = evidence.value;
			} else if (
				isRecord(props.value) &&
				(props.value.k === "StateRef" || props.value.__reactive === "assign")
			) {
				const target = props.value.k === "StateRef" ? props.value.n : props.value.target;
				if (typeof target !== "string" || !target.startsWith("$"))
					throw new Error("unsupportedForm");
				value =
					payload && Object.hasOwn(payload, target)
						? payload[target]
						: state && Object.hasOwn(state, target)
							? state[target]
							: parsed?.stateDeclarations[target];
			} else if (isRecord(fields) && Object.hasOwn(fields, name)) {
				const field = fields[name];
				value = isRecord(field) && Object.hasOwn(field, "componentType") ? field.value : field;
			} else {
				value = props.value;
			}
			// Some stock controls display defaults without writing them into SDK state.
			if (typeName === "CheckBoxGroup" || typeName === "SwitchGroup") {
				const selected = isRecord(value) ? value : {};
				value = Object.fromEntries(
					(Array.isArray(props.items) ? props.items : []).map((item) => {
						if (!isRecord(item) || !isRecord(item.props) || typeof item.props.name !== "string")
							throw new Error("unsupportedForm");
						return [
							item.props.name,
							selected[item.props.name] ?? item.props.defaultChecked ?? false,
						];
					}),
				);
			} else if (value === undefined || value === null) {
				switch (typeName) {
					case "Slider":
						value = props.defaultValue ?? [props.min ?? 0];
						break;
					case "Chips":
					case "OptionCards":
						value = props.defaultValue ?? (props.type === "single" ? "" : []);
						break;
					case "EditableTable":
						value = props.data ?? [];
						break;
					case "DatePicker":
						value = null;
						break;
					default:
						value = props.defaultValue ?? "";
				}
			}
			let label = evidence.label || name;
			if (values.has(label)) label = `${label} (${scope ? `${scope}.` : ""}${name})`;
			if (values.has(label)) throw new Error("unsupportedForm");
			values.set(label, value);
			if (values.size > MAX_OPENUI_FIELDS) throw new Error("messageTooLarge");
			return;
		}
		for (const key in props) {
			if (Object.hasOwn(props, key)) walk(props[key], formName, depth + 1);
		}
	}
	try {
		walk(parsed.root, undefined, 0);
		return { ok: true, values: Object.fromEntries(values) };
	} catch (error) {
		return {
			ok: false,
			reason:
				error instanceof Error && error.message === "messageTooLarge"
					? "messageTooLarge"
					: "unsupportedForm",
		};
	}
}

export type ActionMessageResult =
	| { ok: true; message: string }
	| { ok: false; reason: "actionBlocked" | "messageTooLarge" };

export function buildOpenUIActionMessage(
	event: ActionEvent,
	visibleValues: Record<string, unknown>,
): ActionMessageResult {
	if (event.type !== "continue_conversation" || typeof event.humanFriendlyMessage !== "string") {
		return { ok: false, reason: "actionBlocked" };
	}
	if (!withinUtf8Limit(event.humanFriendlyMessage, 2048)) {
		return { ok: false, reason: "messageTooLarge" };
	}
	const label = event.humanFriendlyMessage.trim();
	if (!label) return { ok: false, reason: "actionBlocked" };
	const snapshot = boundedSnapshot(visibleValues, MAX_OPENUI_MESSAGE_BYTES, true);
	if (snapshot === null) return { ok: false, reason: "messageTooLarge" };
	// params/context may contain model-authored hidden instructions: do not forward them.
	const message =
		snapshot.json === "{}" ? label : `${label}\n\n${JSON.stringify(snapshot.value, null, 2)}`;
	return withinUtf8Limit(message, MAX_OPENUI_MESSAGE_BYTES)
		? { ok: true, message }
		: { ok: false, reason: "messageTooLarge" };
}
