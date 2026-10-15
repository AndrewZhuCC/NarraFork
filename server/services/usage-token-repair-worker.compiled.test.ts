import { expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { getTestDb } from "../../tests/setup";
import { openSchemaCopyAt } from "../../tests/sqlite-schema-image";

test("compiled historical repair embeds both worker boundaries and completes a read-only preview", () => {
	const directory = mkdtempSync(join(tmpdir(), "token-repair-compiled-"));
	const sourceDirectory = mkdtempSync(join(resolve("server"), ".token-repair-compiled-"));
	const path = join(directory, "test.sqlite");
	const db = openSchemaCopyAt(getTestDb().sqlite, path);
	try {
		const dump = JSON.stringify({
			request: { url: "https://example.invalid/custom-gateway" },
			response: {
				status: 200,
				bodyText: JSON.stringify({
					usage: { input_tokens: 100, input_tokens_details: { cached_tokens: 40 } },
				}),
			},
		});
		db.query(
			"INSERT INTO api_requests (id,provider,model,created_at,input_tokens,cached_input_tokens,raw_dump_json) VALUES ('r','openai','test','2026-01-01',100,40,?)",
		).run(dump);
		const harness = join(sourceDirectory, "entry.ts");
		writeFileSync(
			harness,
			`import { TokenRepairClient } from ${JSON.stringify(resolve("server/services/usage-token-repair-service.ts"))};
const c = new TokenRepairClient({backend:'sqlite',sqlitePath:Bun.argv[2]},Bun.argv[3]);
try { const p=await c.probe(); if(!p.hasPending) throw Error('probe'); const j=await c.start('admin',false,[{provider:'openai',model:'test',protocol:'openai'}]); const deadline=Date.now()+10000; let ready=false; while(Date.now()<deadline){ const s=await c.getJob(j.id); if(s?.status==='ready'){ready=true;break;} if(s?.status!=='analyzing')throw Error('status'); await Bun.sleep(10); } if(!ready)throw Error('timeout'); const rows=await c.listCandidates(j.id); if(rows.records[0]?.after?.inputTokens!==60||rows.records[0]?.manualProtocol!=='openai')throw Error('manual evidence'); console.log('TOKEN_REPAIR_COMPILED_OK'); } finally { await c.close(); }
`,
		);
		const executable = join(directory, process.platform === "win32" ? "probe.exe" : "probe");
		const compile = Bun.spawnSync(
			[
				process.execPath,
				"build",
				harness,
				resolve("server/services/usage-token-repair-worker.ts"),
				resolve("server/services/usage-token-repair-evidence-worker.ts"),
				"--root",
				resolve("server"),
				"--compile",
				"--minify",
				"--asset-naming=[dir]/[name].[ext]",
				"--outfile",
				executable,
			],
			{ stdout: "pipe", stderr: "pipe", timeout: 60_000, maxBuffer: 1024 * 1024 },
		);
		expect(compile.exitCode, new TextDecoder().decode(compile.stderr).slice(0, 4000)).toBe(0);
		const run = Bun.spawnSync([executable, path, directory], {
			stdout: "pipe",
			stderr: "pipe",
			timeout: 20_000,
			maxBuffer: 1024 * 1024,
		});
		expect(run.exitCode, new TextDecoder().decode(run.stderr).slice(0, 4000)).toBe(0);
		expect(new TextDecoder().decode(run.stdout)).toContain("TOKEN_REPAIR_COMPILED_OK");
		expect(db.query("SELECT input_tokens FROM api_requests WHERE id='r'").get()).toEqual({
			input_tokens: 100,
		});
		// Missing parser code is a deployment failure, not a reason to silently mark all rows reviewed.
		db.exec(
			"DELETE FROM usage_token_repair_jobs; UPDATE api_requests SET token_accounting_review_version=NULL",
		);
		const negative = join(sourceDirectory, "negative.ts");
		writeFileSync(
			negative,
			`import { TokenRepairClient } from ${JSON.stringify(resolve("server/services/usage-token-repair-service.ts"))};
const c=new TokenRepairClient({backend:'sqlite',sqlitePath:Bun.argv[2]},Bun.argv[3]);
try { const j=await c.start('admin',false,[{provider:'openai',model:'test',protocol:'openai'}]); const deadline=Date.now()+10000; let failed=false; while(Date.now()<deadline){ const s=await c.getJob(j.id); if(s?.status==='failed'){failed=true;break;} if(s?.status!=='analyzing') throw Error('missing worker must fail closed'); await Bun.sleep(10); } if(!failed)throw Error('timeout'); console.log('TOKEN_REPAIR_MISSING_WORKER_REJECTED'); }finally{await c.close();}
`,
		);
		const missingExecutable = join(
			directory,
			process.platform === "win32" ? "missing.exe" : "missing",
		);
		const missingBuild = Bun.spawnSync(
			[
				process.execPath,
				"build",
				negative,
				resolve("server/services/usage-token-repair-worker.ts"),
				"--root",
				resolve("server"),
				"--compile",
				"--minify",
				"--asset-naming=[dir]/[name].[ext]",
				"--outfile",
				missingExecutable,
			],
			{ stdout: "pipe", stderr: "pipe", timeout: 60_000, maxBuffer: 1024 * 1024 },
		);
		expect(
			missingBuild.exitCode,
			new TextDecoder().decode(missingBuild.stderr).slice(0, 4000),
		).toBe(0);
		const missingRun = Bun.spawnSync([missingExecutable, path, directory], {
			stdout: "pipe",
			stderr: "pipe",
			timeout: 20_000,
			maxBuffer: 1024 * 1024,
		});
		expect(missingRun.exitCode, new TextDecoder().decode(missingRun.stderr).slice(0, 4000)).toBe(0);
		expect(new TextDecoder().decode(missingRun.stdout)).toContain(
			"TOKEN_REPAIR_MISSING_WORKER_REJECTED",
		);
		expect(
			db.query("SELECT token_accounting_review_version FROM api_requests WHERE id='r'").get(),
		).toEqual({ token_accounting_review_version: null });
	} finally {
		db.close();
		rmSync(directory, { recursive: true, force: true });
		rmSync(sourceDirectory, { recursive: true, force: true });
	}
}, 90_000);
