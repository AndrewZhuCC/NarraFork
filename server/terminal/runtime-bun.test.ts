import { describe, expect, test } from "bun:test";
import { mkdtemp, readdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { IS_WINDOWS } from "../lib/platform";
import { spawnBunTerminal } from "./runtime-bun";

/**
 * Controlling-terminal regression tests for the Bun.Terminal runtime.
 *
 * A PTY on the child's stdio is NOT the same thing as a controlling terminal.
 * Without setsid() the child reads and writes /dev/pts/N fine — so a terminal
 * looks completely normal — but it has no ctty, which breaks exactly the things
 * a user reaches for last:
 *   - `sudo`/`ssh`/`gpg` reading a secret from /dev/tty get ENXIO and fail
 *     instead of prompting (the originally reported bug, hit whenever dtach was
 *     not installed, since the dtach path happened to mask it);
 *   - job control is dead, so Ctrl-C signals nothing.
 *
 * These assert on observable child behaviour rather than on the spawn options,
 * because the options are only a means: a future Bun release could give a ctty
 * some other way, and it is the child's view that has to be right.
 */

const collect = (cmd: string) =>
	new Promise<string>((resolve) => {
		let out = "";
		const runtime = spawnBunTerminal({
			cmd: ["/bin/bash", "-c", cmd],
			cwd: "/",
			env: { PATH: process.env.PATH ?? "/usr/bin:/bin", TERM: "xterm-256color" },
			cols: 80,
			rows: 24,
			onData: (data) => {
				out += data;
			},
		});
		runtime.exited.then(async () => {
			// The read loop is asynchronous, so let any trailing output land.
			await Bun.sleep(120);
			runtime.close();
			resolve(out);
		});
	});

describe.skipIf(IS_WINDOWS)("spawnBunTerminal controlling terminal", () => {
	test("a PTY without setsid has no controlling terminal (the defect being fixed)", async () => {
		// Negative control, built locally so the assertions above are known to be
		// testing something real rather than passing for free. This mirrors the old
		// runtime exactly — a Bun.Terminal on the child's stdio and nothing else —
		// and demonstrates that a PTY alone leaves /dev/tty unopenable.
		const pty = new Bun.Terminal({
			cols: 80,
			rows: 24,
			data(_term, data) {
				out += typeof data === "string" ? data : new TextDecoder().decode(data);
			},
		});
		let out = "";
		const proc = Bun.spawn(
			[
				"/bin/bash",
				"-c",
				"(echo probe > /dev/tty) >/dev/null 2>&1 && echo CTTY_OK || echo CTTY_FAIL; ps -o tty= -p $$; exit 0",
			],
			{
				cwd: "/",
				env: { PATH: process.env.PATH ?? "/usr/bin:/bin", TERM: "xterm-256color" },
				terminal: pty,
			},
		);
		await proc.exited;
		await Bun.sleep(120);
		pty.close();

		expect(out).toContain("CTTY_FAIL");
		// `ps` reports "?" precisely because there is no controlling terminal.
		expect(out).toMatch(/^\s*\?{1,2}\s*$/m);
	});

	test("the child can open /dev/tty", async () => {
		// This is the sudo-password path: sudo does not read the password from
		// stdin, it opens /dev/tty. With no ctty that open fails outright.
		const out = await collect(
			"(echo probe > /dev/tty) >/dev/null 2>&1 && echo CTTY_OK || echo CTTY_FAIL; exit 0",
		);
		expect(out).toContain("CTTY_OK");
		expect(out).not.toContain("CTTY_FAIL");
	});

	test("the child is its own session and process-group leader on the PTY", async () => {
		// `ps` reports TT as "?" for a process with no controlling terminal even
		// when its stdio is a pts — that "?" is the actual defect signature.
		const sessionColumn = process.platform === "darwin" ? "sess" : "sid";
		const out = await collect(`ps -o ${sessionColumn}=,pgid=,tty= -p $$; echo "SELF=$$"; exit 0`);
		const selfPid = /SELF=(\d+)/.exec(out)?.[1] ?? "";
		expect(selfPid).not.toBe("");
		const psLine =
			out
				.split(/\r?\n/)
				.map((line) => line.trim())
				.find((line) => /^\d+\s+\d+\s+\S+$/.test(line)) ?? "";
		expect(psLine, out).not.toBe("");
		const [sid, pgid, tty] = psLine.split(/\s+/);
		expect(tty).toMatch(process.platform === "darwin" ? /^ttys\w+$/ : /^pts\//);
		// setsid() makes the child both session and process-group leader, which is
		// the prerequisite for the shell being able to hand the foreground group to
		// a job (and therefore for Ctrl-C to reach anything).
		// Darwin's `ps sess` is not the POSIX SID (it may report 0).
		if (process.platform !== "darwin") expect(sid).toBe(selfPid);
		expect(pgid).toBe(selfPid);
	});

	test("an interactive bash has working job control", async () => {
		// bash announces the failure itself; asserting on that message keeps the
		// test honest about which capability is missing.
		let out = "";
		const runtime = spawnBunTerminal({
			cmd: ["/bin/bash", "-i"],
			cwd: "/",
			env: { PATH: process.env.PATH ?? "/usr/bin:/bin", TERM: "xterm-256color", PS1: "$ " },
			cols: 80,
			rows: 24,
			onData: (data) => {
				out += data;
			},
		});
		await Bun.sleep(700);
		runtime.write("exit\n");
		await Promise.race([runtime.exited, Bun.sleep(2500)]);
		runtime.kill();
		runtime.close();

		expect(out).not.toContain("no job control");
		expect(out).not.toContain("cannot set terminal process group");
	});
});

// A literal Windows device name is not a shell history sink. Reproduce its
// creation in a real Bash PTY, then verify the portable disabled-history value.
describe.skipIf(IS_WINDOWS)("terminal history file regression", () => {
	for (const history of ["NUL", ""])
		test(`HISTFILE=${JSON.stringify(history)} has the expected on-disk effect`, async () => {
			const root = await mkdtemp(join(tmpdir(), "nf-pty-history-"));
			let output = "";
			const runtime = spawnBunTerminal({
				cmd: ["/bin/bash", "--noprofile", "--norc", "-i"],
				cwd: root,
				env: {
					PATH: process.env.PATH ?? "/usr/bin:/bin",
					HOME: root,
					TERM: "xterm-256color",
					PS1: "NF_HISTORY_READY> ",
					HISTFILE: history,
				},
				cols: 100,
				rows: 30,
				onData: (data) => {
					output += data;
				},
			});
			try {
				const deadline = performance.now() + 2_500;
				while (!output.includes("NF_HISTORY_READY> ") && performance.now() < deadline)
					await Bun.sleep(10);
				expect(output).toContain("NF_HISTORY_READY> ");
				runtime.write("echo NF_HISTORY_ROUNDTRIP\nexit\n");
				const status = await Promise.race([runtime.exited, Bun.sleep(2_500).then(() => "timeout")]);
				expect(status).toBe(0);
				const files = await readdir(root);
				if (history) {
					expect(files).toContain("NUL");
					expect(await readFile(join(root, "NUL"), "utf8")).toContain("NF_HISTORY_ROUNDTRIP");
				} else expect(files).toEqual([]);
			} finally {
				runtime.kill();
				runtime.close();
				await rm(root, { recursive: true, force: true });
			}
		});
});

test("standalone terminal creation disables history without platform device filenames", async () => {
	const source = await readFile(
		new URL("../services/terminal-service.ts", import.meta.url),
		"utf8",
	);
	expect(source).toContain('HISTFILE: ""');
	expect(source).not.toContain("HISTFILE: DEV_NULL");
});
