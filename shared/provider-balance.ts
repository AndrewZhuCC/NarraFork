/** Normalized, credential-free projections of relay wallet/key allowances. */
export type ProviderBalanceAdapter = "sub2api" | "new-api";
export type ProviderBalanceScope = "wallet" | "key" | "subscription";
export type ProviderBalanceStatus = "pending" | "probing" | "supported" | "unsupported" | "error";

export interface ProviderBalanceAmount {
	/** USD/CNY/TOKENS/QUOTA or a validated site currency symbol. */
	unit: string;
	remaining: number | null;
	used?: number;
	total?: number;
	/** For example 5h, 1d, 7d, daily, weekly or monthly. */
	window?: string;
}

export interface ProviderBalanceData {
	scope: ProviderBalanceScope;
	unlimited: boolean;
	amounts: ProviderBalanceAmount[];
}

export interface ProviderBalanceSnapshot {
	generation: string;
	status: ProviderBalanceStatus;
	adapter?: ProviderBalanceAdapter;
	balance: ProviderBalanceData | null;
	fetchedAt: number;
	attemptedAt: number;
	/** Safe fixed codes only, never upstream response text or transport exceptions. */
	error: "request_failed" | "invalid_configuration" | null;
}
