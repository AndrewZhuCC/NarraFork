import { lstatSync, readFileSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { getNarraforkPath } from "./narrafork-home";
import { safeSpawn } from "./spawn";
import { GIT_COMMIT } from "./version";

export interface StablePathInstall {
	launchdLabel: "dev.narrafork.server";
	fromCommit: string;
	toCommit: string;
	certificateSha1: string;
}

/** Local installer opt-in only: ordinary downloaded updates must not accept same-exec paths. */
export async function verifyStablePathInstall(
	candidate: string,
	install: StablePathInstall,
	context = {
		platform: process.platform as string,
		parentPid: process.ppid,
		service: process.env.XPC_SERVICE_NAME,
		execPath: process.execPath,
		stablePath: join(homedir(), ".local/bin/narrafork-server"),
		identityPath: getNarraforkPath("code-signing", "identity.json"),
		commit: GIT_COMMIT,
		uid: process.getuid?.(),
		verifySignature: async (path: string, fingerprint: string) => {
			const result = await safeSpawn({
				cmd: [
					"/usr/bin/codesign",
					"--verify",
					"--strict",
					"-R",
					`=identifier "app.narrafork.selfuse" and certificate leaf = H"${fingerprint}"`,
					path,
				],
				timeout: 15_000,
				maxOutputBytes: 4096,
			});
			return result.exitCode === 0;
		},
	},
): Promise<boolean> {
	try {
		if (
			context.platform !== "darwin" ||
			context.parentPid !== 1 ||
			context.service !== "dev.narrafork.server" ||
			install.launchdLabel !== context.service ||
			!/^([a-f\d]{8}|[a-f\d]{40})$/.test(install.fromCommit) ||
			!/^([a-f\d]{8}|[a-f\d]{40})$/.test(install.toCommit) ||
			!context.commit ||
			!install.fromCommit.startsWith(context.commit) ||
			install.toCommit.startsWith(context.commit) ||
			!/^[a-f\d]{40}$/i.test(install.certificateSha1) ||
			resolve(candidate) !== context.stablePath ||
			resolve(context.execPath) !== context.stablePath
		)
			return false;
		const stat = lstatSync(candidate);
		if (
			stat.size <= 0 ||
			stat.size > 512 * 1024 * 1024 ||
			!stat.isFile() ||
			stat.isSymbolicLink() ||
			stat.uid !== context.uid ||
			(stat.mode & 0o022) !== 0 ||
			realpathSync(candidate) !== context.stablePath
		)
			return false;
		const identityStat = lstatSync(context.identityPath);
		if (
			!identityStat.isFile() ||
			identityStat.isSymbolicLink() ||
			identityStat.size > 4096 ||
			identityStat.uid !== context.uid ||
			(identityStat.mode & 0o022) !== 0
		)
			return false;
		const identity = JSON.parse(readFileSync(context.identityPath, "utf8"));
		if (
			typeof identity.fingerprint !== "string" ||
			identity.fingerprint.toLowerCase() !== install.certificateSha1.toLowerCase()
		)
			return false;
		return await context.verifySignature(candidate, install.certificateSha1);
	} catch {
		return false;
	}
}
