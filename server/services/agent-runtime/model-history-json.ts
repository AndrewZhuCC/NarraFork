export const MODEL_JSON_CONTAINER_BYTES = 256;
export const MODEL_JSON_SLOT_BYTES = 32;
export const MODEL_JSON_MAX_CONTAINER_SLOTS = 16_384;

export interface ModelJsonShapeOptions {
	deadline: number;
	maxBytes?: number;
	maxContainerSlots?: number;
}
export interface ModelJsonShape {
	bytes: number;
	containers: number;
	slots: number;
	maxContainerSlots: number;
}

/**
 * Pure lexical preflight, BEFORE JSON.parse. Reserve objects/arrays AND primitive/property slots
 * across the worker, cloned source and provider views. Braces/commas/colons in strings are data.
 * This is an allocation guard, not a JSON validator; JSON.parse still validates accepted input.
 */
export function measureModelJsonShape(
	raw: unknown,
	options: ModelJsonShapeOptions,
): ModelJsonShape {
	const shape: ModelJsonShape = { bytes: 0, containers: 0, slots: 0, maxContainerSlots: 0 };
	if (typeof raw !== "string") return shape;
	const maxBytes = Math.min(options.maxBytes ?? 128 * 1024 * 1024, 128 * 1024 * 1024);
	const maxContainerSlots = options.maxContainerSlots ?? MODEL_JSON_MAX_CONTAINER_SLOTS;
	const stack: Array<{ array: boolean; expectingSlot: boolean; slots: number }> = [];
	let quoted = false;
	let nextDeadlineCheck = 0;
	const reserve = (bytes: number) => {
		shape.bytes += bytes;
		if (shape.bytes > maxBytes) throw new Error("Model history preparation exceeded 128MiB budget");
	};
	for (let index = 0; index < raw.length; index++) {
		const code = raw.charCodeAt(index);
		if (quoted) {
			if (code === 92) index++;
			else if (code === 34) quoted = false;
		} else {
			const parent = stack.at(-1);
			// An array element starts with any value token; an object property starts with its key.
			if (
				parent?.expectingSlot &&
				(parent.array
					? code !== 32 && code !== 9 && code !== 10 && code !== 13 && code !== 93 && code !== 44
					: code === 34)
			) {
				parent.expectingSlot = false;
				parent.slots++;
				shape.slots++;
				shape.maxContainerSlots = Math.max(shape.maxContainerSlots, parent.slots);
				reserve(MODEL_JSON_SLOT_BYTES);
				if (parent.slots > maxContainerSlots)
					throw new Error("Model history JSON container width exceeded allocation budget");
			}
			if (code === 34) quoted = true;
			else if (code === 123 || code === 91) {
				shape.containers++;
				reserve(MODEL_JSON_CONTAINER_BYTES);
				stack.push({ array: code === 91, expectingSlot: true, slots: 0 });
			} else if (code === 125 || code === 93) stack.pop();
			else if (code === 44 && parent) parent.expectingSlot = true;
		}
		if (index >= nextDeadlineCheck) {
			nextDeadlineCheck = index + 16_384;
			if (Date.now() >= options.deadline)
				throw new Error("Model history preparation exceeded 10s budget");
		}
	}
	return shape;
}

/** Compatibility name: now reserves all graph slots, not only containers. */
export function modelJsonContainerBytes(raw: unknown, deadline: number, maxBytes?: number): number {
	return measureModelJsonShape(raw, { deadline, maxBytes }).bytes;
}
