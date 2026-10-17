/**
 * What the reader has typed or picked in an inline UI, per tool call, for this page.
 *
 * A card's frame is replaced when the card is drawn again (the list recycles rows that
 * scroll away) and a second frame opens for the expanded view. Both start from the values
 * kept here, and a change in either reaches the other, so the reader's input is not lost
 * to either.
 */
type Values = Record<string, unknown>;

const MAX_CARDS = 100;
const states = new Map<string, Values>();
const listeners = new Map<string, Set<(values: Values, from: unknown) => void>>();

export function readUiState(key: string): Values | undefined {
	return states.get(key);
}

/** Record a frame's values and pass them to the card's other frames. */
export function writeUiState(key: string, values: Values, from: unknown): void {
	const next = { ...states.get(key), ...values };
	states.delete(key);
	states.set(key, next);
	if (states.size > MAX_CARDS) states.delete(states.keys().next().value as string);
	for (const listener of listeners.get(key) ?? []) listener(next, from);
}

export function subscribeUiState(
	key: string,
	listener: (values: Values, from: unknown) => void,
): () => void {
	let set = listeners.get(key);
	if (!set) {
		set = new Set();
		listeners.set(key, set);
	}
	set.add(listener);
	return () => {
		set.delete(listener);
		if (set.size === 0) listeners.delete(key);
	};
}

/** Test seam. */
export function resetUiStateForTest(): void {
	states.clear();
	listeners.clear();
}
