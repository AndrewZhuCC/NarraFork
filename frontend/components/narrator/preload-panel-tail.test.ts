import { expect, mock, test } from "bun:test";
import { preloadPendingPanelTail } from "./preload-panel-tail";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
function fixture(detailReady = false) {
	const current = { narratorId: "n", detailReady };
	const controller = new AbortController();
	const preload = mock(async () => {});
	const module = {
		preloadPretextDocumentTail: preload,
		firstScreenPageSizeForLod: (lod: number) => (lod <= 2 ? 100 : 40),
	};
	const loadModule = mock(async () => module);
	return {
		current,
		controller,
		preload,
		module,
		loadModule,
		opts: {
			narratorId: "n",
			signal: controller.signal,
			readOpeningLod: () => 5,
			getCurrent: () => current,
			loadModule,
		},
	};
}
test("ready panels leave tail loading to the canonical child, not a late preload producer", async () => {
	const f = fixture(true);
	await preloadPendingPanelTail(f.opts);
	expect(f.loadModule).not.toHaveBeenCalled();
	expect(f.preload).not.toHaveBeenCalled();
});
test("detail becomes ready during dynamic import: do not issue a post-adoption second tail", async () => {
	const f = fixture();
	const gate = deferred<typeof f.module>();
	const pending = preloadPendingPanelTail({ ...f.opts, loadModule: () => gate.promise });
	f.current.detailReady = true;
	gate.resolve(f.module);
	await pending;
	expect(f.preload).not.toHaveBeenCalled();
});
test("identity changes during import: old panels cannot start a transport after handoff", async () => {
	const f = fixture();
	const gate = deferred<typeof f.module>();
	const pending = preloadPendingPanelTail({ ...f.opts, loadModule: () => gate.promise });
	f.current.narratorId = "other";
	gate.resolve(f.module);
	await pending;
	expect(f.preload).not.toHaveBeenCalled();
});
test("cancelled imports never initiate tail I/O", async () => {
	const f = fixture();
	const gate = deferred<typeof f.module>();
	const pending = preloadPendingPanelTail({ ...f.opts, loadModule: () => gate.promise });
	f.controller.abort();
	gate.resolve(f.module);
	await pending;
	expect(f.preload).not.toHaveBeenCalled();
});
test("pending-detail non-route panels keep their LOD-bounded early tail fallback", async () => {
	const f = fixture();
	await preloadPendingPanelTail(f.opts);
	expect(f.preload).toHaveBeenCalledTimes(1);
	expect(f.preload).toHaveBeenCalledWith("n", {
		firstScreenPageSize: 40,
		signal: f.controller.signal,
	});
});
