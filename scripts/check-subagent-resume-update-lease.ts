import assert from "node:assert/strict";
import {
	beginQuiescingTools,
	getUpdateCoordinationStatus,
	resetUpdateCoordinationForTests,
	scheduleUpdate,
	tryAcquireFinalUpdateExecution,
	waitForOrdinaryToolDrain,
} from "../server/services/update-coordinator";

const runnerSource = await Bun.file(
	new URL("../server/services/subagent-runner.ts", import.meta.url),
).text();
const recoverySource = await Bun.file(
	new URL("../server/services/update-recovery-service.ts", import.meta.url),
).text();
const sendSource = await Bun.file(
	new URL("../server/lib/agent/tools/send.ts", import.meta.url),
).text();
assert.match(
	runnerSource,
	/claimSubagentUpdateExecutionLease\(\s*input\.updateExecutionLease,\s*"resumable",\s*subagentId,/,
);
assert.doesNotMatch(runnerSource, /resumableUpdateLease/);
assert.match(recoverySource, /for \(const activeResume of listActiveSubagentResumeRuns\(\)\)/);
assert.match(recoverySource, /kind: "foreground_agent"/);
assert.match(recoverySource, /!isLiveNarratorForCheckpoint\(snapshot, originToolCall\.narratorId\)/);
assert.match(recoverySource, /!\["initializing", "pending", "running"\]\.includes\(originToolCall\.status\)/);
assert.match(recoverySource, /\.select\(\{\s*id: narratorToolCalls\.id,/);
assert.doesNotMatch(sendSource, /updateExecutionLease/);

try {
	const ordinary = tryAcquireFinalUpdateExecution("ordinary", "ordinary-tool");
	const resumable = tryAcquireFinalUpdateExecution("resumable", "continued-subagent");
	assert(ordinary);
	assert(resumable);

	scheduleUpdate("lease-self-check");
	beginQuiescingTools();
	let drained = false;
	const drain = waitForOrdinaryToolDrain().then(() => {
		drained = true;
	});
	await Promise.resolve();
	assert.equal(drained, false);

	ordinary.release();
	await drain;
	assert.equal(drained, true);
	assert.deepEqual(
		{
			ordinary: getUpdateCoordinationStatus().pendingOrdinaryExecutionCount,
			resumable: getUpdateCoordinationStatus().resumableExecutionCount,
		},
		{ ordinary: 0, resumable: 1 },
	);
	assert.equal(tryAcquireFinalUpdateExecution("ordinary", "late-ordinary"), null);
	assert.equal(tryAcquireFinalUpdateExecution("resumable", "late-resumable"), null);
	resumable.release();

	console.log("subagent resume update lease self-check passed");
} finally {
	resetUpdateCoordinationForTests();
}
