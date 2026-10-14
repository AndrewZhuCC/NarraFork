import type { SpecTabLayout } from "./spec-tab-order";

export interface SpecTabLayoutSaver {
	save(mutate: (current: SpecTabLayout) => SpecTabLayout): void;
	/** 等到目前已排队的保存全部结束。 */
	whenIdle(): Promise<void>;
}

/**
 * 串行保存一位叙述者的大纲标签排布。
 *
 * 每次手势都立刻写入缓存。网络请求一次只发一个，且发出去的总是当时的最新排布，
 * 因此较早的慢请求不会在新请求之后落库、把顺序盖回去。失败时只退回服务端已经
 * 确认过的排布；失败期间到来的更新手势会保留并接着发送。
 */
export function createSpecTabLayoutSaver(options: {
	/** 偏好尚未加载时返回 null。空排布表示已经加载、只是还没有排过。 */
	readLoadedLayout: () => SpecTabLayout | null;
	writeLayout: (layout: SpecTabLayout) => void;
	persist: (layout: SpecTabLayout) => Promise<unknown>;
	onPersistError: () => void;
}): SpecTabLayoutSaver {
	let latest: SpecTabLayout | null = null;
	let acked: SpecTabLayout | null = null;
	let chain: Promise<void> = Promise.resolve();

	const save = (mutate: (current: SpecTabLayout) => SpecTabLayout) => {
		if (latest === null) {
			const loaded = options.readLoadedLayout();
			if (!loaded) return;
			latest = cloneLayout(loaded);
			acked = cloneLayout(loaded);
		}
		const next = cloneLayout(mutate(latest));
		if (sameLayout(next, latest)) return;
		latest = next;
		options.writeLayout(cloneLayout(next));
		// 上一次 flush 被拒绝时也要继续排后面的保存。
		chain = chain.then(flush, flush);
	};

	async function flush(): Promise<void> {
		if (latest === null || (acked !== null && sameLayout(latest, acked))) return;
		const sending = cloneLayout(latest);
		try {
			await options.persist(sending);
			acked = sending;
			if (latest !== null && !sameLayout(latest, sending)) await flush();
		} catch {
			if (latest !== null && !sameLayout(latest, sending)) {
				await flush();
				return;
			}
			if (acked !== null) {
				latest = cloneLayout(acked);
				options.writeLayout(cloneLayout(acked));
			}
			options.onPersistError();
		}
	}

	return {
		save,
		whenIdle: () => chain,
	};
}

/** 只替换一位叙述者的排布，其他偏好字段和其他叙述者的记录保持不变。 */
export function patchNarratorSpecTabLayout<T extends object>(
	preferences: T,
	narratorId: string,
	layout: SpecTabLayout,
): Omit<T, "specTabLayout"> & { specTabLayout: Record<string, unknown> } {
	const current = preferences as T & { specTabLayout?: Record<string, unknown> };
	return {
		...preferences,
		specTabLayout: { ...current.specTabLayout, [narratorId]: layout },
	};
}

function cloneLayout(layout: SpecTabLayout): SpecTabLayout {
	return { order: [...layout.order], pinned: [...layout.pinned] };
}

function sameLayout(a: SpecTabLayout, b: SpecTabLayout): boolean {
	return samePaths(a.order, b.order) && samePaths(a.pinned, b.pinned);
}

function samePaths(a: readonly string[], b: readonly string[]): boolean {
	if (a.length !== b.length) return false;
	for (let index = 0; index < a.length; index++) {
		if (a[index] !== b[index]) return false;
	}
	return true;
}
