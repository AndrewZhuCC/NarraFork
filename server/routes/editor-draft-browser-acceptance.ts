import { expect } from "bun:test";
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import type { CDPSession, Page } from "puppeteer-core";
import type {} from "../../scripts/smoke-editor-cache-fixture";

export async function editorDraftBrowserAcceptance(
	page: Page,
	fixture: {
		path: string;
		origin: string;
		original: string;
		downloadDirectory: string;
		writeDisk(text: string): Promise<void>;
		readDisk(): Promise<string>;
		removeDisk(): Promise<void>;
		revoke(revoked: boolean): void;
		changeContext(changed: boolean): void;
		commits(): number;
		loseNextReceipt(): void;
		restartService(): Promise<void>;
	},
) {
	const wait = (predicate: Parameters<Page["waitForFunction"]>[0]) =>
		page.waitForFunction(predicate, { timeout: 12_000, polling: 50 });
	const ready = () =>
		wait(
			() =>
				window.__editorCache?.snapshot().views === 2 &&
				window.__editorCache.snapshot().registry.loads === 0,
		);
	const state = () => page.evaluate(() => window.__editorCache.snapshot());
	const edit = (text: string) =>
		page.evaluate((value) => window.__editorCache.edit("file-0", value), text);
	const rows = () =>
		page.evaluate(async () => {
			const db = await new Promise<IDBDatabase>((resolve, reject) => {
				const r = indexedDB.open("narrafork-editor-drafts", 1);
				r.onsuccess = () => resolve(r.result);
				r.onerror = () => reject(r.error);
			});
			const result = await new Promise<
				{
					writerId: string;
					revision: number;
					generation: string;
					scope: { resourceKey: string };
					operation?: unknown;
					anchor?: { anchorId: string; checkpointGeneration: string };
				}[]
			>((resolve, reject) => {
				const r = db.transaction("metadata").objectStore("metadata").getAll(undefined, 33);
				r.onsuccess = () => resolve(r.result);
				r.onerror = () => reject(r.error);
			});
			db.close();
			return result;
		});
	const save = async (target: Page = page) => {
		await target.evaluate(() => window.__editorCache.focus("file-0"));
		await target.keyboard.down("Control");
		await target.keyboard.press("s");
		await target.keyboard.up("Control");
	};
	page.on("dialog", (dialog) => {
		void dialog.accept();
	});
	await ready();
	const initial = await state();
	expect(initial.registry.tabs).toBe(1000);
	expect(initial.monacoModels).toBe(1);
	expect(initial.drafts.bodyReads).toBe(0);
	expect(initial.drafts.metadataReads).toBe(2);
	const writerBeforeRefresh = initial.drafts.writerId;
	await edit("/* LOCAL_REFRESH */");
	await wait(() => window.__editorCache.snapshot().drafts.published === 1);
	expect(await rows()).toHaveLength(1);
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.waitForSelector("[data-editor-draft-restore]");
	expect((await state()).drafts.writerId).not.toBe(writerBeforeRefresh);
	expect((await state()).drafts.bodyReads).toBe(0);
	await page.click("[data-editor-draft-restore]");
	await wait(() => !!window.__editorCache.snapshot().registry.pinReasons.dirty);
	expect(
		(await state()).panes.every((pane) => pane.firstLine?.startsWith("/* LOCAL_REFRESH */")),
	).toBe(true);
	expect(await page.$("[data-editor-draft-restored]")).not.toBeNull();
	expect((await state()).drafts.bodyReads).toBeGreaterThan(0);
	// After a real external write, restoration must keep the pre-crash base and conflict.
	await fixture.writeDisk(`/* DISK_CHANGED */${fixture.original}`);
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.waitForSelector("[data-editor-draft-restore]");
	await page.click("[data-editor-draft-restore]");
	await wait(
		() =>
			!!document
				.querySelector("[data-editor-draft-restored]")
				?.textContent?.includes("Both the draft and disk changed"),
	);
	await save();
	await wait(() => !!window.__editorCache.snapshot().registry.pinReasons.conflict);
	expect(await fixture.readDisk()).toBe(`/* DISK_CHANGED */${fixture.original}`);
	expect((await state()).registry.pinReasons.dirty).toBe(1);
	await wait(
		() =>
			window.__editorCache.snapshot().drafts.running === 0 &&
			window.__editorCache.snapshot().drafts.queued === 0,
	);
	// Crash the fixture renderer, NOT the host/agent server. No unload checkpoint is assumed.
	const session = await page.createCDPSession();
	let browserSession: CDPSession | undefined;
	let browserAttachExpired = false;
	let browserAttachTimer: ReturnType<typeof setTimeout> | undefined;
	const browserProcess = page.browser().process();
	const crashStarted = performance.now();
	// Temporary bounded evidence for the CI-only missing crash event; never a success signal.
	const environment = {
		bun: process.versions.bun,
		platform: process.platform,
		browserPid: browserProcess?.pid,
		disableBreakpad: browserProcess?.spawnargs.includes("--disable-breakpad"),
		disableCrashReporter: browserProcess?.spawnargs.includes("--disable-crash-reporter"),
		target: {} as object,
		coreLimit: "unavailable",
		corePattern: "unavailable",
		samples: [] as object[],
		stderrBytes: 0,
		crashStderr: "",
		errors: [] as { stage: string; name: string }[],
	};
	const noteDiagnosticError = (stage: string, error: unknown) => {
		if (environment.errors.length < 8)
			environment.errors.push({ stage, name: error instanceof Error ? error.name : "unknown" });
	};
	const diagnostic = async <T>(stage: string, work: () => Promise<T>): Promise<T | undefined> => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		try {
			return await Promise.race([
				work(),
				new Promise<undefined>((resolve) => {
					timer = setTimeout(() => {
						noteDiagnosticError(`${stage}.timeout`, new Error("Diagnostic read timed out"));
						resolve(undefined);
					}, 250);
				}),
			]);
		} catch (error) {
			noteDiagnosticError(stage, error);
			return undefined;
		} finally {
			clearTimeout(timer);
		}
	};
	const detachBrowserSession = async (attached: CDPSession) => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		try {
			await Promise.race([
				attached.detach(),
				new Promise<never>((_, reject) => {
					timer = setTimeout(() => reject(new Error("Diagnostic detach timed out")), 250);
				}),
			]);
		} catch (error) {
			noteDiagnosticError("browser.detach", error);
		} finally {
			clearTimeout(timer);
		}
	};
	let rendererProcesses: { id: number; cpuTime: number }[] = [];
	const sampleProcesses = async (stage: string) => {
		if (process.platform !== "linux" || !browserSession) return;
		try {
			if (stage === "before-send") {
				const { processInfo } = await browserSession.send("SystemInfo.getProcessInfo", undefined, {
					timeout: 250,
				});
				rendererProcesses = processInfo.filter((info) => info.type === "renderer").slice(0, 8);
			}
			// Later proc reads must not depend on the browser's DevTools queue staying responsive.
			const renderers = await diagnostic(`proc.${stage}`, () =>
				Promise.all(
					rendererProcesses.map(async (info) => {
						try {
							const status = await Bun.file(`/proc/${info.id}/status`).slice(0, 4096).text();
							return {
								pid: info.id,
								cpuSecondsAtSetup: info.cpuTime,
								status: status
									.split("\n")
									.filter((line) => /^(State|CoreDumping|TracerPid|VmRSS|Threads):/.test(line)),
							};
						} catch (error) {
							return {
								pid: info.id,
								readError:
									error instanceof Error && "code" in error ? String(error.code) : "unknown",
							};
						}
					}),
				),
			);
			if (renderers)
				environment.samples.push({
					stage,
					ms: Math.round(performance.now() - crashStarted),
					renderers,
				});
		} catch (error) {
			noteDiagnosticError(stage, error);
		}
	};
	const onBrowserStderr = (chunk: Buffer | string) => {
		environment.stderrBytes += Buffer.byteLength(chunk);
		const remaining = 2048 - Buffer.byteLength(environment.crashStderr);
		if (remaining <= 0) return;
		const text =
			typeof chunk === "string" ? chunk.slice(0, 4096) : chunk.subarray(0, 4096).toString();
		const summary = text
			.split("\n")
			.filter((line) =>
				/NOTREACHED|Check failed|FATAL|Received signal|crashpad.*(?:error|failed)/i.test(line),
			)
			.map((line) =>
				line.replace(/(?:https?|file):\/\/\S+/g, "[url]").replace(/[^\x20-\x7e]/g, "?"),
			)
			.join("\n");
		environment.crashStderr += summary.slice(0, remaining);
	};
	browserProcess?.stderr?.on("data", onBrowserStderr);
	const diagnosticTimers: ReturnType<typeof setTimeout>[] = [];
	const diagnosticReads: Promise<void>[] = [];
	const crashDiagnostics: { event: string; ms: number; error?: string }[] = [];
	const recordCrash = (event: string, error?: unknown) => {
		if (crashDiagnostics.length < 10)
			crashDiagnostics.push({
				event,
				ms: Math.round(performance.now() - crashStarted),
				// These are controlled CDP command errors, never document content or URLs.
				...(error === undefined ? {} : { error: String(error).slice(0, 240) }),
			});
	};
	let observedCrash!: () => void;
	const crashed = new Promise<void>((resolve) => {
		observedCrash = resolve;
	});
	let observing = true;
	const onTargetCrashed = () => {
		recordCrash("Inspector.targetCrashed");
		if (observing && performance.now() - crashStarted <= 5000) observedCrash();
	};
	// Puppeteer's primary session is diagnostic only; success requires our enabled session.
	const onPageError = () => recordCrash("page.error");
	session.on("Inspector.targetCrashed", onTargetCrashed);
	page.on("error", onPageError);
	let crashTimeout: ReturnType<typeof setTimeout> | undefined;
	for (const ms of [1000, 4900])
		diagnosticTimers.push(
			setTimeout(
				() => {
					diagnosticReads.push(sampleProcesses(`${ms}ms`));
				},
				Math.max(0, ms - (performance.now() - crashStarted)),
			),
		);
	try {
		await Promise.race([
			new Promise<never>((_, reject) => {
				crashTimeout = setTimeout(
					() => reject(new Error("Renderer did not crash")),
					Math.max(0, 5000 - (performance.now() - crashStarted)),
				);
			}),
			(async () => {
				try {
					await Promise.race([
						page
							.browser()
							.target()
							.createCDPSession()
							.then(
								async (attached) => {
									if (browserAttachExpired || !observing) await detachBrowserSession(attached);
									else browserSession = attached;
								},
								(error) => noteDiagnosticError("browser.attach", error),
							),
						new Promise<void>((resolve) => {
							browserAttachTimer = setTimeout(() => {
								browserAttachExpired = true;
								noteDiagnosticError(
									"browser.attach-timeout",
									new Error("Diagnostic attach timed out"),
								);
								resolve();
							}, 250);
						}),
					]);
				} catch (error) {
					noteDiagnosticError("browser.attach", error);
				} finally {
					clearTimeout(browserAttachTimer);
				}
				await session
					.send("Target.getTargetInfo", undefined, { timeout: 250 })
					.then(({ targetInfo }) => {
						const contextId = page.browserContext().id;
						environment.target = {
							id: targetInfo.targetId,
							type: targetInfo.type,
							matchesPageUrl: targetInfo.url === page.url(),
							contextIdComparable: contextId !== undefined,
							...(contextId === undefined
								? {}
								: { matchesContext: targetInfo.browserContextId === contextId }),
						};
					})
					.catch((error) => noteDiagnosticError("target", error));
				if (process.platform === "linux") {
					const core = await diagnostic("core-config", () =>
						Promise.all([
							Bun.file("/proc/self/limits").slice(0, 4096).text(),
							Bun.file("/proc/sys/kernel/core_pattern").slice(0, 256).text(),
						]),
					);
					if (core) {
						environment.coreLimit =
							core[0].split("\n").find((line) => line.startsWith("Max core file size")) ??
							"missing";
						environment.corePattern = core[1].trim();
					}
				}
				await sampleProcesses("before-send");
				if (!observing) return await crashed;
				// Preparation and injection share the original five-second observation window.
				await session.send("Inspector.enable");
				recordCrash("Inspector.enabled");
				if (!observing || performance.now() - crashStarted > 5000) return await crashed;
				recordCrash("Page.crash.sent");
				void session.send("Page.crash").then(
					() => recordCrash("Page.crash.resolved"),
					(error) => recordCrash("Page.crash.rejected", error),
				);
				await crashed;
			})(),
		]);
	} catch (error) {
		throw new Error(`Renderer crash observation failed: ${JSON.stringify(crashDiagnostics)}`, {
			cause: error,
		});
	} finally {
		observing = false;
		browserAttachExpired = true;
		clearTimeout(crashTimeout);
		clearTimeout(browserAttachTimer);
		for (const timer of diagnosticTimers) clearTimeout(timer);
		session.off("Inspector.targetCrashed", onTargetCrashed);
		page.off("error", onPageError);
		browserProcess?.stderr?.off("data", onBrowserStderr);
		await session.detach().catch((error) => recordCrash("session.detach.rejected", error));
		if (browserSession) await detachBrowserSession(browserSession);
		await diagnostic("cleanup", () => Promise.allSettled(diagnosticReads));
		console.info("[editor-renderer-crash]", JSON.stringify(crashDiagnostics));
		console.info("[editor-renderer-environment]", JSON.stringify(environment));
	}
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.waitForSelector("[data-editor-draft-restore]");
	expect((await state()).drafts.bodyReads).toBe(0);
	await page.click("[data-editor-draft-restore]");
	await wait(() => !!window.__editorCache.snapshot().registry.pinReasons.dirty);
	const recoveredPanes = await page.evaluate(() =>
		["file-0", "file-1"].map((id) => window.__editorCache.paneState(id).text),
	);
	expect(recoveredPanes).toEqual([
		`/* LOCAL_REFRESH */${fixture.original}`,
		`/* LOCAL_REFRESH */${fixture.original}`,
	]);
	expect(await page.$("[data-editor-draft-restored]")).not.toBeNull();
	expect((await state()).drafts.bodyReads).toBeGreaterThan(0);
	expect(await fixture.readDisk()).toBe(`/* DISK_CHANGED */${fixture.original}`);
	await wait(
		() =>
			window.__editorCache.snapshot().drafts.running === 0 &&
			window.__editorCache.snapshot().drafts.queued === 0,
	);
	// Rediscover without loading bodies for the independent multi-window and ACL checks below.
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.waitForSelector("[data-editor-draft-restore]");
	expect((await state()).drafts.bodyReads).toBe(0);
	const beforeSecond = await rows();
	await page.evaluate(() => sessionStorage.setItem("fixture-cloned-writer", "same-session-value"));
	const opened = page.browser().waitForTarget((target) => target.opener() === page.target());
	await page.evaluate(() => {
		window.open(location.href, "draft-second-window");
	});
	const second = await (await opened).page();
	if (!second) throw new Error("window.open did not create a page");
	second.on("dialog", (dialog) => {
		void dialog.accept();
	});
	try {
		await second.waitForFunction(
			() =>
				window.__editorCache?.snapshot().views === 2 &&
				window.__editorCache.snapshot().registry.loads === 0,
			{ timeout: 12_000 },
		);
		const secondWriter = await second.evaluate(
			() => window.__editorCache.snapshot().drafts.writerId,
		);
		expect(secondWriter).not.toBe((await state()).drafts.writerId);
		expect(await second.evaluate(() => sessionStorage.getItem("fixture-cloned-writer"))).toBe(
			"same-session-value",
		);
		await second.evaluate(() => window.__editorCache.edit("file-0", "/* SECOND_WINDOW */"));
		await second.waitForFunction(() => window.__editorCache.snapshot().drafts.published === 1, {
			timeout: 12_000,
		});
		const both = await rows();
		expect(both.some((row) => row.writerId === secondWriter)).toBe(true);
		for (const row of beforeSecond)
			expect(both.some((candidate) => candidate.generation === row.generation)).toBe(true);
		await save(second);
		await second.waitForFunction(
			() =>
				!window.__editorCache.snapshot().registry.pinReasons.dirty &&
				!window.__editorCache.snapshot().drafts.running,
			{ timeout: 12_000 },
		);
		const remaining = await rows();
		expect(remaining.some((row) => row.writerId === secondWriter)).toBe(false);
		for (const row of beforeSecond)
			expect(remaining.some((candidate) => candidate.generation === row.generation)).toBe(true);
	} finally {
		await second.close();
		await page.bringToFront();
	}
	// Revoking ACL while the model stays resident must also hide disk-backed discovery.
	fixture.revoke(true);
	await page.evaluate(() => window.__editorCache.workspaceVisible(false));
	await wait(() => window.__editorCache.snapshot().views === 0);
	const deniedResident = page.waitForResponse(
		(response) =>
			response.url().includes("/editor-documents/recovery-scope?") &&
			[403, 404].includes(response.status()),
	);
	await page.evaluate(() => window.__editorCache.workspaceVisible(true));
	await deniedResident;
	expect(await page.$("[data-editor-local-draft]")).toBeNull();
	expect((await state()).drafts.bodyReads).toBe(0);
	fixture.revoke(false);
	fixture.changeContext(true);
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.waitForSelector("[data-editor-draft-export]");
	expect(await page.$("[data-editor-draft-restore]")).toBeNull();
	expect((await state()).drafts.bodyReads).toBe(0);
	fixture.changeContext(false);
	await fixture.removeDisk();
	await page.reload({ waitUntil: "domcontentloaded" });
	await page.waitForSelector("[data-editor-draft-export]");
	expect(await page.$("[data-editor-draft-restore]")).toBeNull();
	expect((await state()).monacoModels).toBe(0);
	expect((await state()).drafts.bodyReads).toBe(0);
	// Export the actual complete IDB body through Chrome while the real gate is blocked.
	await mkdir(fixture.downloadDirectory);
	const downloadSession = await page.browser().target().createCDPSession();
	await downloadSession.send("Browser.setDownloadBehavior", {
		behavior: "allow",
		downloadPath: fixture.downloadDirectory,
		eventsEnabled: true,
	});
	let complete!: () => void,
		fail!: (error: Error) => void,
		guid = "";
	const downloaded = new Promise<void>((resolve, reject) => {
		complete = resolve;
		fail = reject;
	});
	downloadSession.on("Browser.downloadWillBegin", (event) => {
		if (event.suggestedFilename === "editor-recovered-draft.utf8.txt") guid = event.guid;
	});
	downloadSession.on("Browser.downloadProgress", (event) => {
		if (event.guid === guid) {
			if (event.state === "completed") complete();
			else if (event.state === "canceled") fail(new Error("Draft download cancelled"));
		}
	});
	const downloadTimeout = setTimeout(() => fail(new Error("Draft export timed out")), 10_000);
	try {
		await page.click("[data-editor-draft-export]");
		await downloaded;
		expect(
			await readFile(join(fixture.downloadDirectory, "editor-recovered-draft.utf8.txt"), "utf8"),
		).toBe(`/* LOCAL_REFRESH */${fixture.original}`);
		expect((await state()).drafts.bodyReads).toBeGreaterThan(0);
		expect((await state()).registry.pinReasons["recovery-candidate"]).toBe(1);
		expect((await state()).registry.pinnedBytes).toBeGreaterThan(0);
	} finally {
		clearTimeout(downloadTimeout);
		await downloadSession.detach();
	}
	await wait(() => !window.__editorCache.snapshot().registry.pinReasons["recovery-candidate"]);
	expect((await state()).registry.pinnedBytes).toBe(0);
	fixture.revoke(true);
	const denied = page.waitForResponse(
		(response) =>
			response.url().includes("/editor-documents/recovery-scope?") &&
			[403, 404].includes(response.status()),
	);
	await page.reload({ waitUntil: "domcontentloaded" });
	await denied;
	await wait(() => !!window.__editorCache && !!document.querySelector("[data-cache-pane]"));
	expect(await page.$("[data-editor-local-draft]")).toBeNull();
	expect((await state()).drafts.bodyReads).toBe(0);
	expect((await state()).drafts.metadataReads).toBe(0);
	fixture.revoke(false);
	await fixture.writeDisk(fixture.original);
	// IndexedDB unavailable must not disable ordinary v1 editing or saving.
	const disabled = await page.evaluateOnNewDocument(() =>
		Object.defineProperty(globalThis, "indexedDB", {
			configurable: true,
			get() {
				throw new DOMException("disabled", "SecurityError");
			},
		}),
	);
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await edit("/* IDB_DISABLED */");
	await page.waitForSelector("[data-editor-backup-incomplete]");
	await save();
	await wait(() => !window.__editorCache.snapshot().registry.pinReasons.dirty);
	expect(await fixture.readDisk()).toBe(`/* IDB_DISABLED */${fixture.original}`);
	expect(
		await page.evaluate(() =>
			window.dispatchEvent(new Event("beforeunload", { cancelable: true })),
		),
	).toBe(true);
	await page.removeScriptToEvaluateOnNewDocument(disabled.identifier);
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await page.evaluate(() => {
		const original = IDBObjectStore.prototype.put;
		IDBObjectStore.prototype.put = function (...args) {
			if (this.name === "chunks") throw new DOMException("fixture quota", "QuotaExceededError");
			return original.apply(this, args);
		};
	});
	await edit("/* QUOTA_FAILURE */");
	await page.waitForSelector("[data-editor-backup-incomplete]");
	expect((await state()).registry.pinReasons.dirty).toBe(1);
	expect(
		await page.evaluate(
			() => !window.dispatchEvent(new Event("beforeunload", { cancelable: true })),
		),
	).toBe(true);
	await save();
	await wait(() => !window.__editorCache.snapshot().registry.pinReasons.dirty);
	expect(await fixture.readDisk()).toBe(`/* QUOTA_FAILURE *//* IDB_DISABLED */${fixture.original}`);
	// Existing ordinary checkpoint + marker/delete failure + lost commit reply. The
	// backend anchor, not an IDB hint or the current disk hash, must preserve query-first.
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	await edit("/* PROVENANCE_RACE */");
	await wait(() => window.__editorCache.snapshot().drafts.published === 1);
	const raceWriter = (await state()).drafts.writerId;
	const ordinary = (await rows()).find((row) => row.writerId === raceWriter);
	expect(ordinary?.operation).toBeUndefined();
	expect(ordinary?.anchor).toBeDefined();
	await page.evaluate(() => {
		const counters = { puts: 0, deletes: 0 };
		Reflect.set(window, "__editorDraftIdbFaults", counters);
		const put = IDBObjectStore.prototype.put,
			remove = IDBObjectStore.prototype.delete;
		IDBObjectStore.prototype.put = function (...args) {
			if (this.name === "metadata") {
				counters.puts++;
				throw new DOMException("fixture marker failure", "QuotaExceededError");
			}
			return put.apply(this, args);
		};
		IDBObjectStore.prototype.delete = function (...args) {
			if (this.name === "metadata") {
				counters.deletes++;
				throw new DOMException("fixture cleanup failure", "QuotaExceededError");
			}
			return remove.apply(this, args);
		};
	});
	expect(await page.evaluate(() => window.__editorCache.tryCheckpointCleanup())).toEqual({
		attempted: true,
		failed: true,
	});
	const commitsBeforeLoss = fixture.commits();
	fixture.loseNextReceipt();
	await save();
	await wait(() => !!window.__editorCache.snapshot().registry.pinReasons.unknown);
	expect(await page.$("[data-editor-backup-incomplete]")).not.toBeNull();
	expect((await rows()).find((row) => row.writerId === raceWriter)?.operation).toBeUndefined();
	const faults = await page.evaluate(
		() => Reflect.get(window, "__editorDraftIdbFaults") as { puts: number; deletes: number },
	);
	expect(faults.puts).toBeGreaterThan(0);
	expect(faults.deletes).toBeGreaterThan(0);
	expect(await fixture.readDisk()).toBe(
		`/* PROVENANCE_RACE *//* QUOTA_FAILURE *//* IDB_DISABLED */${fixture.original}`,
	);
	expect(fixture.commits()).toBe(commitsBeforeLoss + 1);
	await fixture.restartService();
	await page.reload({ waitUntil: "domcontentloaded" });
	await ready();
	const raceSelector = `[data-editor-draft-anchor="${ordinary?.anchor?.anchorId}"]`;
	await page.waitForSelector(`${raceSelector} [data-editor-draft-query]`);
	expect(await page.$(`${raceSelector} [data-editor-draft-restore]`)).toBeNull();
	expect((await state()).drafts.bodyReads).toBe(0);
	await page.click(`${raceSelector} [data-editor-draft-query]`);
	await page.waitForSelector(`${raceSelector} [data-editor-draft-receipt="saved"]`);
	expect(fixture.commits()).toBe(commitsBeforeLoss + 1);
	console.info(
		"EDITOR_DRAFT_PROVENANCE_CHROMIUM",
		JSON.stringify({
			idbMarkerFailures: faults.puts,
			idbDeleteFailures: faults.deletes,
			lostCommitReplies: 1,
			serviceColdRestarts: 1,
			recoveredOriginalSavedReceipt: 1,
			reapply: 0,
		}),
	);
	console.info(
		"EDITOR_DRAFT_CHROMIUM",
		JSON.stringify({
			tabs: 1000,
			initialModels: 1,
			hiddenBodyReads: 0,
			rendererCrashes: 1,
			windows: 2,
			clonedSessionStorageWriterIsolation: true,
			blockedGateExports: 1,
			changedBindingReadOnly: 1,
			unavailableSave: 1,
			quotaFailureSave: 1,
			revokedMetadataReads: 0,
			revokedBodyReads: 0,
			retainedWriters: beforeSecond.length,
		}),
	);
	await page.evaluate(() => window.__editorCache.dispose());
}
