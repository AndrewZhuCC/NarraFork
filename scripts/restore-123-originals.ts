import { createHash } from "node:crypto";
import { lstat, mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { verifyReleaseBundle } from "./lib/ci-release-bundle";
import { runCiGh } from "./lib/ci-release-io";
import { validateCiReleasePlan } from "./lib/ci-release-plan";
import { downloadArchive } from "./lib/ci-release-restore";
import { runBridgeProcess } from "./lib/ci-update-server-bridge-restore";

// Explicit operator recovery of the already-published 0.9.1 originals, never a build.
const repository = "NarraFork/NarraFork";
const product = "05a12a3148b8d985a78e24ffe2d1570ae6bbf970";
const sources = [
	{
		kind: "bridge",
		id: 11746942787,
		name: "update-server-bridge-main-38248612889-1",
		run: 38248612889,
		sha: "d12fb6ae55a9f6b13988e2b22b590083ae5e653a",
		bytes: 31110308,
		digest: "sha256:920b6ccec156febcb170b243136daddecb3f22c5c275738b40a85f8a6fe0735f",
		source: "scripts/lib/ci-update-server-bridge-restore.ts",
		symbol: "EXTRACT",
		limit: 512 * 1024 ** 2,
	},
	{
		kind: "bundle",
		id: 11742723027,
		name: "release-bundle-38238815855-1",
		run: 38238815855,
		sha: product,
		bytes: 440116801,
		digest: "sha256:5d5d47e1f9833f0e85c0d704d9bc4e59384b562a7f77d49c76a77276dc6ee3d3",
		source: "scripts/lib/ci-release-restore.ts",
		symbol: "EXTRACT_BUNDLE",
		limit: 24 * 1024 ** 3,
	},
] as const;

export async function restore123Originals(root: string) {
	if (!process.env.GH_TOKEN) throw new Error("Original artifact read credential required");
	await mkdir(root, { mode: 0o700 });
	for (const expected of sources) {
		const artifact = JSON.parse(
			await runCiGh(["api", `repos/${repository}/actions/artifacts/${expected.id}`]),
		);
		if (
			artifact.id !== expected.id ||
			artifact.name !== expected.name ||
			artifact.expired ||
			artifact.size_in_bytes !== expected.bytes ||
			artifact.digest !== expected.digest ||
			artifact.workflow_run.id !== expected.run ||
			artifact.workflow_run.head_sha !== expected.sha ||
			artifact.workflow_run.head_branch !== "main" ||
			artifact.workflow_run.repository_id !== 1183360584 ||
			artifact.workflow_run.head_repository_id !== 1183360584
		)
			throw new Error("Original artifact provenance changed");
		const archive = join(root, `${expected.kind}.zip`);
		await downloadArchive(artifact, repository, archive, process.env, ((input, init) => {
			const headers = new Headers(init?.headers);
			headers.set("User-Agent", "NarraFork-123Distribution/0.9.1");
			return fetch(input, { ...init, headers });
		}) as typeof fetch);
		const destination = join(root, expected.kind);
		try {
			await lstat(destination);
			throw new Error("Extraction destination already exists");
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
		await mkdir(destination, { mode: 0o700 });
		const source = await Bun.file(expected.source).text();
		const match = source.match(new RegExp(`const ${expected.symbol} = \\x60([\\s\\S]*?)\\x60;`));
		if (!match) throw new Error("Existing strict archive extractor missing");
		await runBridgeProcess(
			"python3",
			["-I", "-c", match[1], archive, destination, String(expected.limit)],
			{ timeoutMs: 900000, maximumOutputBytes: 65536, environment: process.env },
		);
		if (expected.kind === "bridge") {
			const raw = await Bun.file(join(destination, "prepared-main-mirror.json")).text();
			const sealHash = createHash("sha256").update(raw).digest("hex");
			const seal = JSON.parse(raw);
			const envelope = await Bun.file(join(destination, "bridge-envelope.json")).json();
			if (
				sealHash !== "6effd788d034477ae1bc33f3c2be5e976ce96056f0d9d0f5e8f6133823e07f7a" ||
				seal.commit !== product ||
				seal.runId !== 38238815855 ||
				seal.version !== "0.9.1" ||
				seal.platforms.length !== 8 ||
				envelope.tag !== "v0.9.1"
			)
				throw new Error("Original bridge identity mismatch");
			console.log(
				JSON.stringify({
					restored: expected.kind,
					artifactId: artifact.id,
					artifactDigest: artifact.digest,
					sealSha256: sealHash,
				}),
			);
		} else {
			const manifest = await Bun.file(join(destination, "manifest.json")).json();
			const plan = validateCiReleasePlan(manifest.plan);
			if (
				plan.commit !== product ||
				plan.version !== "0.9.1" ||
				plan.runId !== 38238815855 ||
				plan.repository !== repository
			)
				throw new Error("Original product identity mismatch");
			await verifyReleaseBundle(destination, plan);
			console.log(
				JSON.stringify({
					restored: expected.kind,
					artifactId: artifact.id,
					artifactDigest: artifact.digest,
					commit: product,
					verifiedFiles: manifest.files.length,
				}),
			);
		}
	}
}

if (import.meta.main) {
	try {
		if (Bun.argv.length !== 3 || !Bun.argv[2].startsWith("--output-dir="))
			throw new Error("Expected one output directory");
		await restore123Originals(resolve(Bun.argv[2].slice(13)));
	} catch {
		console.error("Original 0.9.1 distribution restore failed; no credentials logged");
		process.exitCode = 1;
	}
}
