// File identity survives the legacy image/text arrays without persisting browser-only metadata.
const orders = new WeakMap<File, number>();
let sequence = 0;

/** Unmarked restored/legacy files retain their existing stable array order. */
export function getComposerFileOrder(file: File): number {
	return orders.get(file) ?? Number.MAX_SAFE_INTEGER;
}

export function markComposerFileOrder(file: File): number {
	const existing = orders.get(file);
	if (existing !== undefined) return existing;
	const order = sequence++;
	orders.set(file, order);
	return order;
}

/** Resizing must not move an image behind the text files selected after it. */
export function inheritComposerFileOrder(result: File, source: File): File {
	orders.set(result, markComposerFileOrder(source));
	return result;
}
