/**
 * Keyboard batching for the remote browser preview.
 *
 * Keystrokes are queued and flushed after a short idle window. The queue is
 * never dropped while a screenshot/type interaction is in flight — those keys
 * are flushed when the in-flight interaction settles. That serialisation is
 * what makes rapid typing work: the first flush may start a slow round-trip,
 * but subsequent keys must wait in line instead of being discarded.
 */

export type BrowserKeyInput = { text?: string; key?: string };

const SPECIAL_KEYS: Record<string, string> = {
	Enter: "Enter",
	Tab: "Tab",
	Escape: "Escape",
	Backspace: "Backspace",
	Delete: "Delete",
	ArrowUp: "ArrowUp",
	ArrowDown: "ArrowDown",
	ArrowLeft: "ArrowLeft",
	ArrowRight: "ArrowRight",
	Home: "Home",
	End: "End",
	PageUp: "PageUp",
	PageDown: "PageDown",
	" ": "Space",
	F1: "F1",
	F2: "F2",
	F3: "F3",
	F4: "F4",
	F5: "F5",
	F6: "F6",
	F7: "F7",
	F8: "F8",
	F9: "F9",
	F10: "F10",
	F11: "F11",
	F12: "F12",
};

export type BrowserKeyMapResult = BrowserKeyInput | "ignore";

/** Map a DOM keyboard event to a remote browser key press, or "ignore". */
export function mapBrowserKeyEvent(
	key: string,
	mods: { ctrlKey: boolean; metaKey: boolean; altKey: boolean },
): BrowserKeyMapResult {
	const mappedKey = SPECIAL_KEYS[key];
	if (mappedKey) return { key: mappedKey };
	if (key.length === 1 && !mods.ctrlKey && !mods.metaKey && !mods.altKey) {
		return { text: key };
	}
	return "ignore";
}

/** Append one key input, merging consecutive printable characters. */
export function appendBrowserKeyInput(
	queue: readonly BrowserKeyInput[],
	item: BrowserKeyInput,
): BrowserKeyInput[] {
	if (item.text != null) {
		const last = queue[queue.length - 1];
		if (last?.text != null) {
			return [...queue.slice(0, -1), { text: last.text + item.text }];
		}
	}
	return [...queue, item];
}

/**
 * Debounced keystroke queue that cooperates with in-flight remote interactions.
 *
 * Contract:
 * - `push` always records the keystroke (even while blocked).
 * - `enqueueInteraction` puts a mouse gesture behind all earlier keys, but ahead
 *   of keys pressed afterward.
 * - `release` unblocks after an interaction settles and sends the next item
 *   immediately (no extra debounce — the user already waited).
 */
export class BrowserKeyStrokeBroker {
	private queue: Array<
		{ type: "keys"; keys: BrowserKeyInput[] } | { type: "interaction"; run: () => void }
	> = [];
	private timer: ReturnType<typeof setTimeout> | null = null;
	private blocked = false;
	private disposed = false;

	constructor(
		private readonly send: (keys: BrowserKeyInput[]) => void,
		private readonly debounceMs = 150,
	) {}

	get isBlocked(): boolean {
		return this.blocked;
	}

	get pendingCount(): number {
		return this.queue.reduce(
			(count, item) => count + (item.type === "keys" ? item.keys.length : 0),
			0,
		);
	}

	/** Record a keystroke. Safe to call while an interaction is in flight. */
	push(item: BrowserKeyInput): void {
		if (this.disposed) return;
		const last = this.queue.at(-1);
		if (last?.type === "keys") last.keys = appendBrowserKeyInput(last.keys, item);
		else this.queue.push({ type: "keys", keys: [item] });
		this.arm();
	}

	/** Queue a mouse gesture behind earlier keys, preserving input order. */
	enqueueInteraction(run: () => void): void {
		if (this.disposed) return;
		this.queue.push({ type: "interaction", run });
		this.drain();
	}

	/** Prevent drains (used while an interaction outside the broker is in flight). */
	block(): void {
		if (!this.disposed) this.blocked = true;
	}

	/** Try to send the next item now. A blocked drain leaves it queued for `release`. */
	drain(): void {
		this.clearTimer();
		if (this.disposed || this.blocked || this.queue.length === 0) return;
		const item = this.queue.shift();
		if (!item) return;
		this.blocked = true;
		try {
			if (item.type === "keys") this.send(item.keys);
			else item.run();
		} catch (error) {
			this.queue.unshift(item);
			this.blocked = false;
			throw error;
		}
	}

	/** Call on success, error, or cancellation; sends the next queued item. */
	release(): void {
		if (this.disposed) return;
		this.blocked = false;
		if (this.queue.length > 0) this.drain();
	}

	dispose(): void {
		this.disposed = true;
		this.clearTimer();
		this.queue = [];
		this.blocked = false;
	}

	private arm(): void {
		this.clearTimer();
		this.timer = setTimeout(() => {
			this.timer = null;
			this.drain();
		}, this.debounceMs);
	}

	private clearTimer(): void {
		if (this.timer) {
			clearTimeout(this.timer);
			this.timer = null;
		}
	}
}
