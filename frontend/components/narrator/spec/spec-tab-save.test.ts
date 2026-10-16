import { describe, expect, it } from "bun:test";
import type { SpecTabLayout } from "./spec-tab-order";
import { createSpecTabLayoutSaver, patchNarratorSpecTabLayout } from "./spec-tab-save";

const layoutA: SpecTabLayout = { order: ["a.md", "b.md"], pinned: [] };
const layoutB: SpecTabLayout = { order: ["b.md", "a.md"], pinned: [] };
const layoutC: SpecTabLayout = { order: ["b.md"], pinned: ["a.md"] };

function deferred() {
	let resolve: () => void = () => {};
	let reject: (error: unknown) => void = () => {};
	const promise = new Promise<void>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

async function settleMicrotasks() {
	await Promise.resolve();
	await Promise.resolve();
}

describe("createSpecTabLayoutSaver", () => {
	it("把连续手势收成一次写入，并且只提交最终排布", async () => {
		const written: SpecTabLayout[] = [];
		const persisted: SpecTabLayout[] = [];
		const gate = deferred();
		const saver = createSpecTabLayoutSaver({
			readLoadedLayout: () => layoutA,
			writeLayout: (layout) => written.push(layout),
			persist: (layout) => {
				persisted.push(layout);
				return gate.promise;
			},
			onPersistError: () => {
				throw new Error("unexpected error");
			},
		});

		saver.save(() => layoutB);
		saver.save(() => layoutC);
		await settleMicrotasks();

		expect(written).toEqual([layoutB, layoutC]);
		expect(persisted).toEqual([layoutC]);
		gate.resolve();
		await saver.whenIdle();
		expect(persisted).toEqual([layoutC]);
	});

	it("进行中的请求结束后才发送更新的排布，不会再补发旧快照", async () => {
		const persisted: SpecTabLayout[] = [];
		const gates = [deferred(), deferred()];
		const saver = createSpecTabLayoutSaver({
			readLoadedLayout: () => layoutA,
			writeLayout: () => {},
			persist: (layout) => {
				const gate = gates[persisted.length];
				persisted.push(layout);
				return gate.promise;
			},
			onPersistError: () => {
				throw new Error("unexpected error");
			},
		});

		saver.save(() => layoutB);
		await settleMicrotasks();
		expect(persisted).toEqual([layoutB]);

		saver.save(() => layoutC);
		await settleMicrotasks();
		expect(persisted).toEqual([layoutB]);

		gates[0].resolve();
		await settleMicrotasks();
		expect(persisted).toEqual([layoutB, layoutC]);
		gates[1].resolve();
		await saver.whenIdle();
	});

	it("失败时回到上次已确认的排布，并保留失败期间更新的手势", async () => {
		const written: SpecTabLayout[] = [];
		const persisted: SpecTabLayout[] = [];
		const gates = [deferred(), deferred()];
		let errors = 0;
		const saver = createSpecTabLayoutSaver({
			readLoadedLayout: () => layoutA,
			writeLayout: (layout) => written.push(layout),
			persist: (layout) => {
				const gate = gates[persisted.length];
				persisted.push(layout);
				return gate.promise;
			},
			onPersistError: () => {
				errors += 1;
			},
		});

		saver.save(() => layoutB);
		await settleMicrotasks();
		saver.save(() => layoutC);
		gates[0].reject(new Error("offline"));
		await settleMicrotasks();
		expect(errors).toBe(0);
		expect(persisted).toEqual([layoutB, layoutC]);
		expect(written.at(-1)).toEqual(layoutC);

		gates[1].reject(new Error("offline"));
		await saver.whenIdle();
		expect(errors).toBe(1);
		expect(written.at(-1)).toEqual(layoutA);
	});

	it("没有更新的手势时，失败会退回服务端已有排布", async () => {
		const written: SpecTabLayout[] = [];
		const gate = deferred();
		let errors = 0;
		const saver = createSpecTabLayoutSaver({
			readLoadedLayout: () => layoutA,
			writeLayout: (layout) => written.push(layout),
			persist: () => gate.promise,
			onPersistError: () => {
				errors += 1;
			},
		});

		saver.save(() => layoutB);
		await settleMicrotasks();
		gate.reject(new Error("offline"));
		await saver.whenIdle();
		expect(errors).toBe(1);
		expect(written).toEqual([layoutB, layoutA]);
	});

	it("偏好还没加载时不写入", async () => {
		let writes = 0;
		let persists = 0;
		const saver = createSpecTabLayoutSaver({
			readLoadedLayout: () => null,
			writeLayout: () => {
				writes += 1;
			},
			persist: async () => {
				persists += 1;
			},
			onPersistError: () => {},
		});

		saver.save(() => layoutB);
		await saver.whenIdle();
		expect(writes).toBe(0);
		expect(persists).toBe(0);
	});
});

describe("patchNarratorSpecTabLayout", () => {
	it("只替换当前叙述者的排布，保留其他字段和其他叙述者", () => {
		const patched = patchNarratorSpecTabLayout(
			{
				language: "zh-CN",
				specTabLayout: { other: { order: ["z.md"], pinned: ["y.md"] } },
			},
			"n1",
			layoutB,
		);
		expect(patched).toEqual({
			language: "zh-CN",
			specTabLayout: {
				other: { order: ["z.md"], pinned: ["y.md"] },
				n1: layoutB,
			},
		});
	});
});
