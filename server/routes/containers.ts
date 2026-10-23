import { Hono } from "hono";
import { getContainerUnsupportedReason, supportsContainers } from "../lib/platform";
import { safeSpawn } from "../lib/spawn";
import { requireAdmin } from "../middleware/auth";
import {
	getContainerSetupStatus,
	getPodmanStatus,
	resetPodmanCache,
} from "../services/container-service";

/** Host-level container tooling: detection and installation. Nothing here is per-session. */
export const containerRoutes = new Hono();

function containerUnsupportedPayload() {
	return {
		error: getContainerUnsupportedReason() ?? "Container management is unsupported",
		code: "CONTAINERS_UNSUPPORTED",
		supported: false,
	};
}

containerRoutes.get("/setup", async (c) => {
	const refresh = c.req.query("refresh") === "true";
	return c.json(await getContainerSetupStatus(refresh));
});

containerRoutes.get("/podman/status", async (c) => {
	const platform =
		process.platform === "darwin" ? "macos" : process.platform === "win32" ? "windows" : "linux";
	if (!supportsContainers()) {
		return c.json({ installed: false, platform, ...containerUnsupportedPayload() });
	}
	const status = await getPodmanStatus();
	return c.json({ ...status, platform, supported: true });
});

containerRoutes.post("/podman/install", requireAdmin, async (c) => {
	if (!supportsContainers()) {
		return c.json({ ok: false, ...containerUnsupportedPayload() }, 400);
	}
	const platform = process.platform;
	let cmd: string;
	if (platform === "darwin") {
		cmd = "brew install podman && podman machine init && podman machine start";
	} else if (platform === "win32") {
		cmd = "winget install -e --id RedHat.Podman";
	} else {
		// Linux — try common package managers
		cmd = [
			"(command -v apt-get >/dev/null 2>&1 && sudo apt-get update && sudo apt-get install -y podman)",
			"|| (command -v dnf >/dev/null 2>&1 && sudo dnf install -y podman)",
			"|| (command -v pacman >/dev/null 2>&1 && sudo pacman -S --noconfirm podman)",
			'|| (echo "No supported package manager found" && exit 1)',
		].join(" ");
	}

	const result = await safeSpawn({
		cmd: ["sh", "-c", cmd],
		env: process.env as Record<string, string>,
	});

	if (result.exitCode === 0) {
		resetPodmanCache();
		const status = await getPodmanStatus();
		return c.json({ ok: true, ...status });
	}
	return c.json({ ok: false, error: (result.stderr || result.stdout).trim() }, 500);
});
