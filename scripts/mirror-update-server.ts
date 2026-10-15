import { appendFile, mkdir, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { parseArgs } from "node:util";
import { validateWebDavConfig } from "../shared/webdav-transport";
import { resolveMirrorRelayConfig, resolveMirrorTransport } from "./lib/mirror-relay-stage";
import {
	readMirrorDataFile,
	resolveMirrorWorkflowTrigger,
	verifyMirrorPublicationReceipt,
	verifyProtectedMirrorEnvironment,
} from "./lib/mirror-workflow";
import { resolveUpdateServerMirrorConfig } from "./lib/update-server-bridge-http";
import {
	IndependentMirrorError,
	type IndependentMirrorReceipt,
	type MirrorPublishedOptions,
	mirrorPublishedMainRelease,
	mirrorReceipt,
} from "./lib/update-server-main-mirror";
import { mirrorPublishedToolsRelease } from "./lib/update-server-tools-mirror";

export { resolveUpdateServerMirrorConfig } from "./lib/update-server-bridge-http";
export type {
	IndependentMirrorReceipt,
	MirrorPublishedOptions,
} from "./lib/update-server-main-mirror";
/** A post-publication operation; its failures never mutate GitHub or participate in Core Release Gate. */
export async function mirrorPublishedGitHubRelease(
	options: MirrorPublishedOptions,
): Promise<IndependentMirrorReceipt> {
	try {
		const transport = resolveMirrorTransport({ NF_MIRROR_TRANSPORT: options.transport });
		if (transport === "webdav" && !options.dryRun) {
			if (!options.relay) throw new Error("Explicit WebDAV relay configuration required");
			validateWebDavConfig(options.relay.config);
			resolveMirrorRelayConfig({
				NF_MIRROR_WEBDAV_URL: options.relay.config.url,
				NF_MIRROR_WEBDAV_FEED: options.relay.config.feed,
				NF_MIRROR_RELAY_SIGNING_KEY: options.relay.signingKey,
				...("bearerToken" in options.relay.config.auth
					? { NF_MIRROR_WEBDAV_TOKEN: options.relay.config.auth.bearerToken }
					: {
							NF_MIRROR_WEBDAV_USERNAME: options.relay.config.auth.username,
							NF_MIRROR_WEBDAV_PASSWORD: options.relay.config.auth.password,
						}),
			});
		}
	} catch {
		throw new IndependentMirrorError({ ...mirrorReceipt(options), failureCode: "FAILED_CONFIG" });
	}
	if (options.transport !== "webdav" && !options.dryRun && !options.config)
		return { ...mirrorReceipt(options), status: "SKIPPED_UNCONFIGURED" };
	if (options.transport !== "webdav" && options.config && !options.dryRun) {
		try {
			resolveUpdateServerMirrorConfig({
				NF_UPDATE_SERVER: options.config.serverUrl,
				NF_UPDATE_TOKEN: options.config.token,
			});
		} catch {
			throw new IndependentMirrorError({ ...mirrorReceipt(options), failureCode: "FAILED_CONFIG" });
		}
	}
	return options.kind === "main"
		? mirrorPublishedMainRelease(options)
		: mirrorPublishedToolsRelease(options);
}
async function output(values: Record<string, string>) {
	if (!process.env.GITHUB_OUTPUT) return;
	for (const [key, value] of Object.entries(values)) {
		if (!/^[a-z_]+$/.test(key) || !/^[A-Za-z0-9._/-]+$/.test(value))
			throw new Error("Unsafe mirror workflow output");
		await appendFile(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
	}
}
async function main() {
	const { values } = parseArgs({
		options: {
			kind: { type: "string" },
			tag: { type: "string" },
			repository: { type: "string" },
			"dry-run": { type: "boolean" },
			"resolve-trigger": { type: "boolean" },
			"event-file": { type: "string" },
			"receipt-file": { type: "string" },
			"verify-trigger-receipt": { type: "boolean" },
			"receipt-output": { type: "string", default: "artifacts/mirror/receipt.json" },
		},
		strict: true,
		allowPositionals: false,
	});
	const repository = values.repository ?? process.env.GITHUB_REPOSITORY ?? "";
	const event = values["event-file"]
		? await readMirrorDataFile(values["event-file"], 256 * 1024)
		: undefined;
	let trigger: Awaited<ReturnType<typeof resolveMirrorWorkflowTrigger>>;
	if (event && process.env.GITHUB_EVENT_NAME === "workflow_run") {
		trigger = await resolveMirrorWorkflowTrigger(event, repository);
		if (values["resolve-trigger"]) {
			if (trigger) await verifyProtectedMirrorEnvironment(repository);
			await output(
				trigger
					? { eligible: "true", artifact_name: trigger.artifactName, run_id: String(trigger.runId) }
					: { eligible: "false" },
			);
			return;
		}
		if (!trigger || !values["receipt-file"])
			throw new Error("Successful source publication receipt required");
	} else if (values["resolve-trigger"]) {
		const e = event as { repository?: { full_name: string; default_branch: string } };
		if (
			process.env.GITHUB_EVENT_NAME !== "workflow_dispatch" ||
			e?.repository?.full_name?.toLowerCase() !== repository.toLowerCase() ||
			process.env.GITHUB_REF !== `refs/heads/${e.repository.default_branch}`
		)
			throw new Error("Mirror workflow dispatch must use the trusted default branch");
		await verifyProtectedMirrorEnvironment(repository);
		await output({ eligible: "true" });
		return;
	}
	let kind = values.kind;
	let tag = values.tag;
	if (trigger) {
		const receipt = await verifyMirrorPublicationReceipt(
			await readMirrorDataFile(values["receipt-file"] as string),
			trigger,
		);
		if (values["verify-trigger-receipt"]) {
			await output({ eligible: "true" });
			return;
		}
		kind = receipt.kind;
		tag = receipt.tag;
	}
	if (!["main", "helpers", "executor"].includes(kind ?? "") || !tag)
		throw new Error(
			"Use --kind=main|helpers|executor --tag=exact-public-tag --repository=owner/repo",
		);
	const controller = new AbortController();
	const cancel = () => controller.abort();
	process.once("SIGINT", cancel);
	process.once("SIGTERM", cancel);
	const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(25 * 60_000)]);
	const options: MirrorPublishedOptions = {
		kind: kind as MirrorPublishedOptions["kind"],
		repository,
		tag,
		dryRun: values["dry-run"],
		signal,
	};
	let receipt = mirrorReceipt(options);
	let failed = false;
	try {
		try {
			options.transport = resolveMirrorTransport(process.env);
			if (options.transport === "webdav") {
				options.relay = values["dry-run"] ? undefined : resolveMirrorRelayConfig(process.env);
			} else {
				options.config = values["dry-run"]
					? undefined
					: resolveUpdateServerMirrorConfig(process.env);
			}
			receipt = mirrorReceipt(options);
		} catch {
			throw new IndependentMirrorError({ ...receipt, failureCode: "FAILED_CONFIG" });
		}
		receipt = await mirrorPublishedGitHubRelease(options);
	} catch (error) {
		failed = true;
		receipt =
			error instanceof IndependentMirrorError
				? error.receipt
				: { ...receipt, failureCode: signal.aborted ? "CANCELLED" : "MIRROR_FAILED" };
	} finally {
		process.removeListener("SIGINT", cancel);
		process.removeListener("SIGTERM", cancel);
		const path = values["receipt-output"] as string;
		await mkdir(dirname(path), { recursive: true });
		await writeFile(path, `${JSON.stringify(receipt, null, 2)}\n`, { mode: 0o600 });
	}
	console.log(
		`Independent mirror: ${receipt.status}${receipt.failureCode ? `/${receipt.failureCode}` : ""}; uploaded=${receipt.uploaded.length}; server-verified=${receipt.verified.length}; transport=${receipt.transport}${receipt.relay ? `; retention=${receipt.relay.retention}; expires=${receipt.relay.expiresAt}` : ""}; GitHub publication unchanged`,
	);
	if (receipt.failureCode === "SERVER_RECEIPT_REQUIRED")
		console.error(
			"Upgrade update server for server-sha256 verification/commit receipts; asset GET fallback is disabled",
		);
	if (failed) process.exitCode = 1;
}
if (import.meta.main)
	main().catch(() => {
		console.error(
			"Independent mirror failed before upload; GitHub publication unchanged (hard cancellation may leave no receipt)",
		);
		process.exitCode = 1;
	});
