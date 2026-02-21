// Token manager — reverse-engineered from kiro CLI chat
// Single-credential mode with auto-refresh (Social + IdC)

import { effectiveAuthRegion, loadCredentials, saveCredentials } from "./config";
import { generateMachineId } from "./machine-id";
import { KiroAuthError, type KiroConfig, type KiroCredentials } from "./types";

const IDC_AMZ_USER_AGENT =
	"aws-sdk-js/3.738.0 ua/2.1 os/other lang/js md/browser#unknown_unknown api/sso-oidc#3.738.0 m/E KiroIDE";

/**
 * Standalone credential refresh — used by both KiroTokenManager and MultiTokenManager.
 * Detects auth method and delegates to Social or IdC refresh.
 */
export async function refreshCredentials(
	creds: KiroCredentials,
	config: KiroConfig,
): Promise<KiroCredentials> {
	const rt = creds.refreshToken;
	if (!rt) throw new KiroAuthError("Missing refreshToken");
	if (rt.length < 100 || rt.includes("...")) {
		throw new KiroAuthError(`refreshToken appears truncated (${rt.length} chars)`);
	}

	const authMethod = detectAuthMethod(creds);
	return authMethod === "idc" ? refreshIdc(creds, config) : refreshSocial(creds, config);
}

function detectAuthMethod(creds: KiroCredentials): string {
	const m = creds.authMethod?.toLowerCase();
	if (m === "idc" || m === "builder-id" || m === "iam") return "idc";
	if (m === "social") return "social";
	if (creds.clientId && creds.clientSecret) return "idc";
	return "social";
}

async function refreshSocial(creds: KiroCredentials, config: KiroConfig): Promise<KiroCredentials> {
	const rt = creds.refreshToken ?? "";
	const region = effectiveAuthRegion(creds, config);
	const domain = `prod.${region}.auth.desktop.kiro.dev`;
	const url = `https://${domain}/refreshToken`;
	const machineId = generateMachineId(creds, config);
	if (!machineId) {
		throw new KiroAuthError("Cannot generate machineId for Social refresh");
	}

	const res = await fetch(url, {
		method: "POST",
		headers: {
			Accept: "application/json, text/plain, */*",
			"Content-Type": "application/json",
			"User-Agent": `KiroIDE-${config.kiroVersion}-${machineId}`,
			"Accept-Encoding": "gzip, compress, deflate, br",
			host: domain,
			Connection: "close",
		},
		body: JSON.stringify({ refreshToken: rt }),
	});

	if (!res.ok) {
		const body = await res.text().catch(() => "");
		throw new KiroAuthError(`Social token refresh failed: ${res.status} ${body}`);
	}

	const data = (await res.json()) as {
		accessToken: string;
		refreshToken?: string;
		profileArn?: string;
		expiresIn?: number;
	};

	return {
		...creds,
		accessToken: data.accessToken,
		refreshToken: data.refreshToken ?? creds.refreshToken,
		profileArn: data.profileArn ?? creds.profileArn,
		expiresAt: data.expiresIn
			? new Date(Date.now() + data.expiresIn * 1000).toISOString()
			: creds.expiresAt,
	};
}

async function refreshIdc(creds: KiroCredentials, config: KiroConfig): Promise<KiroCredentials> {
	const rt = creds.refreshToken ?? "";
	const clientId = creds.clientId;
	const clientSecret = creds.clientSecret;
	if (!clientId || !clientSecret) {
		throw new KiroAuthError("IdC refresh requires clientId and clientSecret");
	}

	const region = effectiveAuthRegion(creds, config);
	const host = `oidc.${region}.amazonaws.com`;
	const url = `https://${host}/token`;

	const res = await fetch(url, {
		method: "POST",
		headers: {
			"Content-Type": "application/json",
			Host: host,
			Connection: "keep-alive",
			"x-amz-user-agent": IDC_AMZ_USER_AGENT,
			Accept: "*/*",
			"Accept-Language": "*",
			"sec-fetch-mode": "cors",
			"User-Agent": "node",
			"Accept-Encoding": "br, gzip, deflate",
		},
		body: JSON.stringify({
			clientId,
			clientSecret,
			refreshToken: rt,
			grantType: "refresh_token",
		}),
	});

	if (!res.ok) {
		const body = await res.text().catch(() => "");
		throw new KiroAuthError(`IdC token refresh failed: ${res.status} ${body}`);
	}

	const data = (await res.json()) as {
		accessToken: string;
		refreshToken?: string;
		expiresIn?: number;
	};

	return {
		...creds,
		accessToken: data.accessToken,
		refreshToken: data.refreshToken ?? creds.refreshToken,
		expiresAt: data.expiresIn
			? new Date(Date.now() + data.expiresIn * 1000).toISOString()
			: creds.expiresAt,
	};
}

export class KiroTokenManager {
	private credentials: KiroCredentials;
	private config: KiroConfig;
	private credentialsPath: string;
	private refreshPromise: Promise<void> | null = null;

	constructor(config: KiroConfig, credentials: KiroCredentials, credentialsPath: string) {
		this.config = config;
		this.credentials = credentials;
		this.credentialsPath = credentialsPath;
	}

	get currentCredentials(): KiroCredentials {
		return this.credentials;
	}

	get currentConfig(): KiroConfig {
		return this.config;
	}

	/** Ensure token is valid, refresh if needed. Returns accessToken. */
	async ensureValidToken(): Promise<string> {
		if (this.isExpired() || this.isExpiringSoon()) {
			await this.refresh();
		}
		if (!this.credentials.accessToken) {
			throw new KiroAuthError("No accessToken available");
		}
		return this.credentials.accessToken;
	}

	/** Reload credentials from disk (picks up external changes). */
	reloadCredentials(): void {
		const fresh = loadCredentials(this.credentialsPath);
		if (fresh) this.credentials = fresh;
	}

	private isExpired(): boolean {
		// Rust: unwrap_or(true) — treat missing expiresAt as expired
		return this.isExpiringWithin(5) ?? true;
	}

	private isExpiringSoon(): boolean {
		// Rust: unwrap_or(false) — treat missing expiresAt as NOT expiring soon
		return this.isExpiringWithin(10) ?? false;
	}

	/** Returns null if expiresAt is missing/unparseable */
	private isExpiringWithin(minutes: number): boolean | null {
		if (!this.credentials.expiresAt) return null;
		try {
			const expiresAt = new Date(this.credentials.expiresAt).getTime();
			if (Number.isNaN(expiresAt)) return null;
			return expiresAt <= Date.now() + minutes * 60 * 1000;
		} catch {
			return null;
		}
	}

	/** Promise-based lock to prevent concurrent refreshes. */
	private async refresh(): Promise<void> {
		if (this.refreshPromise) return this.refreshPromise;
		this.refreshPromise = this.doRefresh().finally(() => {
			this.refreshPromise = null;
		});
		return this.refreshPromise;
	}

	private async doRefresh(): Promise<void> {
		// Re-read from disk in case another process refreshed
		this.reloadCredentials();
		if (!this.isExpired() && !this.isExpiringSoon()) return;

		const newCreds = await refreshCredentials(this.credentials, this.config);
		this.credentials = newCreds;

		if (this.isExpired()) {
			throw new KiroAuthError("Token still expired after refresh");
		}

		saveCredentials(this.credentialsPath, newCreds);
	}
}
