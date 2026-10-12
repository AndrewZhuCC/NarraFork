import { describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

type Step = { run: string; env?: Record<string, string>; uses?: string };
type Workflow = {
	on: Record<string, unknown>;
	permissions: Record<string, string>;
	jobs: Record<
		string,
		{
			"runs-on": string;
			"timeout-minutes": number;
			environment?: string;
			needs?: string[];
			steps: Step[];
		}
	>;
};
const root = resolve(import.meta.dir, "../..");
const workflow = Bun.YAML.parse(
	readFileSync(join(root, ".github/workflows/relay-probe.yml"), "utf8"),
) as Workflow;
const steps = Object.values(workflow.jobs).flatMap((job) => job.steps);
const scripts = steps.map((step) => {
	const lines = step.run.trimEnd().split("\n");
	if (lines.shift() !== "python3 - <<'PY'" || lines.pop() !== "PY") {
		throw new Error("Expected a standalone Python heredoc");
	}
	return lines.join("\n");
});

// Execute the workflow's real Python, but never real apt/rclone, HTTP, or credentials.
const harness = String.raw`
import contextlib, io, json, os, shutil, signal, socket, stat, subprocess, sys, tempfile
from pathlib import Path
from unittest.mock import patch
request = json.load(sys.stdin)
scripts, mode = request["scripts"], request["mode"]
for script in scripts:
    compile(script, "relay-probe.yml", "exec")
if mode == "syntax":
    print("syntax passed")
    raise SystemExit(0)
config = "[nf_update]\ntype = webdav\nurl = https://webdav.123pan.cn\nvendor = other\nuser = FAKE_USER\npass = FAKE_PASS\n"
secret = {
    "missing_config": "",
    "bad_host": config.replace("webdav.123pan.cn", "untrusted.example"),
    "extra_key": config + "bearer_token_command = unexpected-command\n",
    "extra_remote": config + "[other]\ntype=local\n",
}.get(mode, config)
real_mkdtemp, real_rmtree = tempfile.mkdtemp, shutil.rmtree
calls, directories, handlers = [], [], {}
remote_data = None

with tempfile.TemporaryDirectory(prefix="relay-probe-test-", dir=".") as wrapper:
    wrapper = Path(wrapper)
    summary = wrapper / "summary"
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "GITHUB_STEP_SUMMARY": str(summary),
           "NF_RELAY_RCLONE_CONFIG": secret, "REPOSITORY_ID": "123", "RUN_ID": "456", "RUN_ATTEMPT": "2",
           "GH_TOKEN": "FAKE_TOKEN", "REPOSITORY": "owner/repo", "EVENT_NAME": "workflow_dispatch",
           "DISPATCH_REF": "refs/heads/feature" if mode == "guard_wrong_ref" else "refs/heads/main"}
    def mkdir(*args, **kwargs):
        kwargs["dir"] = wrapper
        value = real_mkdtemp(*args, **kwargs)
        directories.append(Path(value))
        return value
    def cleanup(value, *args, **kwargs):
        if mode == "cleanup_local_failure" and Path(value) in directories:
            raise OSError("SENSITIVE_RESPONSE_BODY")
        return real_rmtree(value, *args, **kwargs)
    def mock_run(args, **kwargs):
        global remote_data
        calls.append(args)
        assert kwargs["timeout"] == 90
        assert kwargs["stdout"] == kwargs["stderr"] == subprocess.DEVNULL
        if args[0] == "sudo":
            assert args[:2] == ["sudo", "apt-get"]
            if mode == "install_timeout":
                raise subprocess.TimeoutExpired(args, 90)
            return subprocess.CompletedProcess(args, 100 if mode == "install_failure" else 0)
        assert args[0] == "rclone"
        assert set(kwargs["env"]) == {"PATH", "HOME"}
        assert "NF_RELAY_RCLONE_CONFIG" not in os.environ
        for flag, value in (("--retries", "1"), ("--low-level-retries", "1"),
                            ("--contimeout", "15s"), ("--timeout", "30s"),
                            ("--max-duration", "90s"), ("--max-transfer", "17Mi"),
                            ("--cutoff-mode", "HARD"), ("--stats", "0")):
            assert args[args.index(flag) + 1] == value
        credential = Path(args[args.index("--config") + 1])
        assert stat.S_IMODE(credential.stat().st_mode) == 0o600
        assert stat.S_IMODE(credential.parent.stat().st_mode) == 0o700
        assert credential.read_text() == config
        remote = next(arg for arg in args if arg.startswith("nf_update:"))
        import re
        assert re.fullmatch(r"nf_update:narrafork-ci-relay/probes/123/456/2/[0-9a-f]{32}\.bin", remote)
        assert "purge" not in args and "rmdir" not in args
        name = "cleanup_remote" if "deletefile" in args else "upload" if "--immutable" in args else "download"
        if mode == name + "_timeout":
            raise subprocess.TimeoutExpired(args, 90)
        if mode == name + "_failure":
            return subprocess.CompletedProcess(args, {"upload": 5, "download": 6, "cleanup_remote": 7}[name])
        if mode in ("signal_term", "signal_int") and name == "upload":
            number = signal.SIGTERM if mode == "signal_term" else signal.SIGINT
            handlers[number](number, None)
        if name == "upload":
            remote_data = Path(args[-2]).read_bytes()
            assert len(remote_data) == 16 * 1024 * 1024
        elif name == "download":
            Path(args[-1]).write_bytes(b"corrupt" if mode == "hash_mismatch" else remote_data)
        else:
            assert args[-2:] == ["deletefile", remote]
            assert remote == next(arg for arg in calls[0] if arg.startswith("nf_update:"))
        return subprocess.CompletedProcess(args, 0)
    class Response:
        def __enter__(self): return self
        def __exit__(self, *args): pass
        def read(self, size):
            assert size == 65537
            return json.dumps({"default_branch": "next" if mode == "guard_changed_default" else "main"}).encode()
    def urlopen(req, **kwargs):
        assert req.full_url == "https://api.github.com/repos/owner/repo" and kwargs == {"timeout": 20}
        if mode == "guard_http_failure":
            raise OSError("SENSITIVE_RESPONSE_BODY")
        return Response()
    index = 0 if mode.startswith("guard_") else 1 if mode.startswith("install_") else 2
    capture = io.StringIO()
    with patch.dict(os.environ, env, clear=True), patch("subprocess.run", side_effect=mock_run), \
         patch("tempfile.mkdtemp", side_effect=mkdir), patch("shutil.rmtree", side_effect=cleanup), \
         patch("signal.signal", side_effect=lambda number, handler: handlers.update({number: handler})), \
         patch("urllib.request.urlopen", side_effect=urlopen), \
         patch("socket.socket", side_effect=AssertionError("Network access forbidden")), \
         contextlib.redirect_stdout(capture):
        try:
            exec(scripts[index], {})
            raise AssertionError("Expected explicit exit")
        except SystemExit as error:
            status = error.code
    output = capture.getvalue()
    assert summary.read_text() == output
    assert all(value not in output for value in ("FAKE_USER", "FAKE_PASS", "FAKE_TOKEN", "SENSITIVE_RESPONSE_BODY", "webdav.123pan.cn"))
    if index == 2:
        result = json.loads(output)
        if mode in ("missing_config", "bad_host", "extra_key", "extra_remote"):
            assert not calls and result["phases"] == [{"phase": "config", "errorcode": 1}]
        else:
            assert calls[-1][-2] == "deletefile"
            assert any(item["phase"] == "cleanup_remote" for item in result["phases"])
        for directory in directories:
            if mode == "cleanup_local_failure":
                real_rmtree(directory)
            else:
                assert not directory.exists()
        print(json.dumps({"status": status, "result": result}))
    else:
        print(json.dumps({"status": status, "output": output, "calls": len(calls)}))
`;

function run(mode: string) {
	const result = spawnSync("python3", ["-c", harness], {
		cwd: root,
		env: { PATH: process.env.PATH ?? "/usr/bin:/bin" },
		input: JSON.stringify({ scripts, mode }),
		encoding: "utf8",
		timeout: 10_000,
		maxBuffer: 64 * 1024,
	});
	expect(result.error).toBeUndefined();
	expect(result.stderr).toBe("");
	expect(result.status).toBe(0);
	return result.stdout;
}

describe("protected relay probe", () => {
	test("real YAML is manual-only, read-only, protected, bounded, and has valid Python", () => {
		expect(Object.keys(workflow.on)).toEqual(["workflow_dispatch"]);
		expect(workflow.permissions).toEqual({ contents: "read" });
		expect(Object.keys(workflow.jobs)).toEqual(["guard", "probe"]);
		expect(workflow.jobs.probe.environment).toBe("release");
		expect(workflow.jobs.probe.needs).toEqual(["guard"]);
		for (const job of Object.values(workflow.jobs)) {
			expect(job["runs-on"]).toBe("ubuntu-24.04");
			expect(job["timeout-minutes"]).toBeLessThanOrEqual(10);
		}
		expect(steps.every((step) => !step.uses)).toBe(true);
		expect(steps.filter((step) => step.env?.NF_RELAY_RCLONE_CONFIG)).toEqual([steps[2]]);
		expect(scripts).toHaveLength(3);
		expect(run("syntax").trim()).toBe("syntax passed");
	});

	for (const [mode, phase, code] of [
		["success", "verify", 0],
		["missing_config", "config", 1],
		["bad_host", "config", 1],
		["extra_key", "config", 1],
		["extra_remote", "config", 1],
		["upload_failure", "upload", 5],
		["download_failure", "download", 6],
		["upload_timeout", "upload", 124],
		["download_timeout", "download", 124],
		["hash_mismatch", "verify", 1],
		["cleanup_remote_failure", "cleanup_remote", 7],
		["cleanup_remote_timeout", "cleanup_remote", 124],
		["cleanup_local_failure", "cleanup_local", 1],
		["signal_term", "upload", 1],
		["signal_int", "upload", 1],
	] as const) {
		test(`${mode}: safe output, hard byte cap, scoped cleanup, and truthful failure attribution`, () => {
			const { status, result } = JSON.parse(run(mode));
			expect(status).toBe(mode === "success" ? 0 : 1);
			expect(result.phases).toContainEqual({ phase, errorcode: code });
			expect(result.byteCount).toBe(16 * 1024 * 1024);
			expect(result.hashMatched).toBe(mode === "success" || mode.startsWith("cleanup_"));
			if (mode === "success") {
				expect(result.uploadBytesPerSecond).toBeGreaterThan(0);
				expect(result.uploadSeconds).toBeGreaterThanOrEqual(0);
				expect(result.downloadSeconds).toBeGreaterThanOrEqual(0);
			}
		});
	}

	for (const [mode, phase, code] of [
		["guard_success", "default_branch", 0],
		["guard_wrong_ref", "default_branch", 1],
		["guard_changed_default", "default_branch", 1],
		["guard_http_failure", "default_branch", 1],
		["install_success", "install", 0],
		["install_failure", "install", 100],
		["install_timeout", "install", 124],
	] as const) {
		test(`${mode}: fixed phase and code without command/HTTP output`, () => {
			const { status, output, calls } = JSON.parse(run(mode));
			expect(status).toBe(code === 0 ? 0 : 1);
			expect(output).toBe(`phase=${phase} errorcode=${code}\n`);
			expect(calls).toBe(mode === "install_success" ? 2 : phase === "install" ? 1 : 0);
		});
	}
});
