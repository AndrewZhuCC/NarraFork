import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { assertReleaseGate } from "../../scripts/ci-release";
import { assertHelperReleaseGate } from "../../scripts/lib/helper-release-control";
import { parseHelperReleaseArguments } from "../../scripts/release-helpers";

type Step = {
	name?: string;
	id?: string;
	if?: string;
	uses?: string;
	run?: string;
	env?: Record<string, string>;
	with?: Record<string, unknown>;
};
type Job = {
	if?: string;
	environment?: string;
	env?: Record<string, string>;
	concurrency?: { group: string; "cancel-in-progress": boolean };
	"timeout-minutes"?: number;
	steps: Step[];
	outputs?: Record<string, string>;
};
type Workflow = {
	concurrency: { group: string };
	on: { workflow_dispatch: { inputs: Record<string, { default?: unknown }> } };
	jobs: Record<string, Job>;
};
const load = (name: string) =>
	Bun.YAML.parse(
		readFileSync(new URL(`../../.github/workflows/${name}.yml`, import.meta.url), "utf8"),
	) as Workflow;
const workflows = [
	{ workflow: load("release"), publisher: "publish" },
	{ workflow: load("helpers-release"), publisher: "publisher" },
];
describe("GitHub-only protected primary workflows", () => {
	for (const { workflow, publisher } of workflows)
		describe(publisher, () => {
			test("publishing stays protected and never takes the independent mirror lock", () => {
				expect(workflow.jobs[publisher]?.environment).toBe("release");
				expect(workflow.jobs[publisher]?.concurrency).toBeUndefined();
				expect(workflow.concurrency.group).not.toBe("update-server-publish");
			});
			test("actual publisher condition rejects cancellation even with already-successful source jobs", () => {
				const condition = workflow.jobs[publisher]?.if?.trim();
				if (!condition?.startsWith("${{") || !condition.endsWith("}}"))
					throw new Error("Missing Actions expression");
				// Execute this repository-owned boolean expression against Actions contexts,
				// rather than merely asserting it contains a cancellation substring.
				const evaluate = new Function(
					"always",
					"cancelled",
					"inputs",
					"needs",
					`return (${condition
						.slice(3, -2)
						.replace(/needs\.([a-z-]+)\.outputs\.([a-z-]+)/g, 'needs["$1"].outputs["$2"]')
						.replace(/needs\.([a-z-]+)\.result/g, 'needs["$1"].result')});`,
				) as (
					always: () => boolean,
					cancelled: () => boolean,
					inputs: { publish: boolean },
					needs: Record<string, { result: string; outputs?: Record<string, string> }>,
				) => boolean;
				for (const restoring of [false, true]) {
					const needs = {
						preflight: {
							result: "success",
							outputs: {
								"restore-mode": String(restoring),
								"build-reuse-mode": "false",
								"smoke-only": "false",
							},
						},
						verify: { result: restoring ? "skipped" : "success" },
						build: { result: restoring ? "skipped" : "success" },
						smoke: { result: restoring ? "skipped" : "success" },
						assemble: { result: restoring ? "skipped" : "success" },
						restore: { result: restoring ? "success" : "skipped" },
					};
					expect(
						evaluate(
							() => true,
							() => false,
							{ publish: true },
							needs,
						),
					).toBe(true);
					expect(
						evaluate(
							() => true,
							() => true,
							{ publish: true },
							needs,
						),
					).toBe(false);
					expect(
						evaluate(
							() => true,
							() => false,
							{ publish: false },
							needs,
						),
					).toBe(false);
					const failed = { ...needs, [restoring ? "restore" : "assemble"]: { result: "failure" } };
					expect(
						evaluate(
							() => true,
							() => false,
							{ publish: true },
							failed,
						),
					).toBe(false);
				}
			});
			test("default build-only and real publishers never obtain mirror credentials or inputs", () => {
				expect(workflow.on.workflow_dispatch.inputs.publish?.default).toBe(false);
				for (const key of ["mirror_only", "bridge_run_id", "bridge_run_attempt"])
					expect(workflow.on.workflow_dispatch.inputs[key]).toBeUndefined();
				expect(JSON.stringify(workflow)).not.toMatch(
					/NF_UPDATE_SERVER|NF_UPDATE_TOKEN|update-server-publish|prepare-bridge|mirror-required|bridge-artifact/,
				);
			});
			test("only successful publication uploads a bounded metadata receipt; source validation stays first", () => {
				const steps = workflow.jobs[publisher]?.steps ?? [];
				const publish = steps.findIndex((step) => /control.js publish/.test(step.run ?? ""));
				const receipt = steps.findIndex(
					(step) => step.name === "Record verified GitHub publication",
				);
				expect(publish).toBeGreaterThan(0);
				expect(receipt).toBeGreaterThan(publish);
				expect(steps[receipt]?.if).toBeUndefined();
				expect(steps[receipt]?.with?.["if-no-files-found"]).toBe("error");
				expect(steps[receipt]?.with?.path).toBe("artifacts/publication/publication-receipt.json");
				expect(steps.some((step) => /apt-get.*zstd/.test(step.run ?? ""))).toBe(false);
				expect(JSON.stringify(workflow.jobs.preflight)).toContain("inputs.source_run_id");
			});
			test("publication has its own fixed deadline and cancellation guard", () => {
				const job = workflow.jobs[publisher];
				expect(job?.["timeout-minutes"]).toBe(30);
				expect(job?.steps[0]?.run).toContain("NF_RELEASE_DEADLINE_MS");
				expect(job?.steps[0]?.run).toContain("25 * 60 * 1000");
				expect(
					job?.steps.filter((step) => /NF_RELEASE_DEADLINE_MS/.test(step.run ?? "")),
				).toHaveLength(1);
			});
		});
});
function env(kind: "main" | "helpers", restore = false): NodeJS.ProcessEnv {
	return {
		PREFLIGHT_RESULT: "success",
		RESTORE_MODE: String(restore),
		SOURCE_RUN_ID: restore ? "11" : "",
		PUBLISH_REQUESTED: "true",
		VERIFY_RESULT: restore ? "skipped" : "success",
		BUILD_RESULT: restore ? "skipped" : "success",
		SMOKE_RESULT: restore ? "skipped" : "success",
		ASSEMBLE_RESULT: restore ? "skipped" : "success",
		RESTORE_RESULT: restore ? "success" : "skipped",
		PUBLISH_RESULT: "success",
		INDEX_ONLY: "false",
		MIRROR_ONLY: "false",
		MIRROR_REQUIRED: "false",
		MIRROR_STATUS: "",
		INDEX_COMMIT: kind === "main" ? "a".repeat(40) : "",
		INDEX_GENERATION: kind === "main" ? "1" : "",
	};
}
for (const kind of ["main", "helpers"] as const)
	describe(`${kind} real gate decisions`, () => {
		const gate = kind === "main" ? assertReleaseGate : assertHelperReleaseGate;
		test("GitHub-only succeeds without fabricated mirror receipt", () => {
			expect(() => gate(env(kind))).not.toThrow();
		});
		for (const status of ["", "PREPARED", "FAILED", "cancelled", "partial", "MIRRORED"])
			test(`independent mirror ${status || "absent"} never changes core success`, () => {
				expect(() =>
					gate({
						...env(kind),
						MIRROR_REQUIRED: "invalid",
						MIRROR_STATUS: status,
						NF_UPDATE_SERVER: "not a URL",
						NF_UPDATE_TOKEN: "fixture",
					}),
				).not.toThrow();
			});
		if (kind === "main")
			test("mirror output cannot bypass missing canonical index proof", () => {
				expect(() =>
					gate({
						...env(kind),
						INDEX_COMMIT: "",
						INDEX_GENERATION: "",
						MIRROR_ONLY: "true",
						MIRROR_STATUS: "MIRRORED",
					}),
				).toThrow("index receipt");
			});
		for (const result of ["failure", "cancelled", "skipped"])
			test(`publisher ${result} never becomes success despite mirror output`, () => {
				expect(() =>
					gate({
						...env(kind),
						PUBLISH_RESULT: result,
						MIRROR_REQUIRED: "true",
						MIRROR_STATUS: "MIRRORED",
					}),
				).toThrow();
			});
	});
describe("helper CLI mirror-only restrictions before any IO", () => {
	for (const args of [
		["--publish=false", "--mirror-only=true", "--source-run-id=11", "--bridge-run-id=22"],
		["--publish=true", "--mirror-only=true", "--source-run-id=11"],
		["--publish=true", "--mirror-only=true", "--source-run-id=-1", "--bridge-run-id=22"],
	])
		test(`rejects ${JSON.stringify(args)}`, () => {
			expect(() =>
				parseHelperReleaseArguments([
					"preflight",
					"--kind=helpers",
					"--tag=helpers-v1.0.0",
					...args,
				]),
			).toThrow();
		});
	test("rejects removed bridge arguments even with an otherwise valid trusted plan", () => {
		expect(() =>
			parseHelperReleaseArguments([
				"preflight",
				"--kind=helpers",
				"--tag=helpers-v1.0.0",
				"--publish=true",
				"--source-run-id=11",
				"--bridge-run-id=22",
			]),
		).toThrow("Unknown option");
	});
});
