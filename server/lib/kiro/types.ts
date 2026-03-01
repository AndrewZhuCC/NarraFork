// Kiro plugin types — reverse-engineered from kiro CLI chat

export interface KiroCredentials {
	id?: string;
	accessToken?: string;
	refreshToken?: string;
	profileArn?: string;
	expiresAt?: string; // RFC3339
	authMethod?: string; // "social" | "idc"
	clientId?: string;
	clientSecret?: string;
	priority?: number; // lower = higher priority, default 0
	region?: string;
	authRegion?: string;
	apiRegion?: string;
	machineId?: string;
	email?: string;
	displayName?: string;
	subscriptionTitle?: string;
	proxyUrl?: string;
	proxyUsername?: string;
	proxyPassword?: string;
	startUrl?: string; // AWS SSO start URL — presence indicates enterprise (IDC)
	/** Accumulated metering usage from streaming responses (local tracking). */
	meterUsage?: number;
	/** Model IDs available to this credential (fetched from ListAvailableModels API). */
	availableModels?: string[];
}

export interface KiroConfig {
	region: string;
	authRegion?: string;
	apiRegion?: string;
	kiroVersion: string;
	machineId?: string;
	systemVersion: string;
	nodeVersion: string;
	loadBalancingMode?: LoadBalancingMode;
}

export type KiroModel =
	| "claude-sonnet-4.5"
	| "claude-opus-4.5"
	| "claude-opus-4.6"
	| "claude-haiku-4.5";

export interface KiroPluginConfig {
	credentialsPath: string;
	configPath: string;
	defaultModel?: KiroModel;
}

// Error hierarchy
export class KiroError extends Error {
	constructor(
		public code: string,
		message: string,
	) {
		super(message);
		this.name = "KiroError";
	}
}

export class KiroAuthError extends KiroError {
	constructor(message: string) {
		super("AUTH_ERROR", message);
		this.name = "KiroAuthError";
	}
}

export class KiroApiError extends KiroError {
	constructor(
		code: string,
		message: string,
		public statusCode?: number,
	) {
		super(code, message);
		this.name = "KiroApiError";
	}
}

export class KiroQuotaError extends KiroError {
	constructor(message: string) {
		super("QUOTA_EXHAUSTED", message);
		this.name = "KiroQuotaError";
	}
}

// --- Multi-credential management ---

export type DisabledReason = "manual" | "too_many_failures" | "quota_exhausted";
export type LoadBalancingMode = "priority" | "balanced";

export interface CredentialEntry {
	id: string;
	credentials: KiroCredentials;
	failureCount: number;
	disabled: boolean;
	disabledReason?: DisabledReason;
	successCount: number;
	lastUsedAt?: string;
}

export interface CallContext {
	id: string;
	credentials: KiroCredentials;
	token: string;
}

export interface CredentialSnapshot {
	id: string;
	priority: number;
	disabled: boolean;
	disabledReason?: DisabledReason;
	failureCount: number;
	successCount: number;
	lastUsedAt?: string;
	email?: string;
	displayName?: string;
	hasProfileArn: boolean;
	hasStartUrl: boolean;
	expiresAt?: string;
	authMethod?: string;
	subscriptionTitle?: string;
	region?: string;
	meterUsage?: number;
	availableModels?: string[];
}

export interface ManagerSnapshot {
	entries: CredentialSnapshot[];
	currentId: string;
	loadBalancingMode: LoadBalancingMode;
	total: number;
	available: number;
}

// --- Usage ---

export interface UsageCacheEntry {
	id: string;
	subscriptionTitle?: string;
	currentUsage: number;
	usageLimit: number;
	remaining: number;
	usagePercentage: number;
	nextResetAt?: number;
	queriedAt: string; // ISO 8601
}

// --- Decoder ---

export type ParseErrorKind =
	| "prelude_crc_mismatch"
	| "message_crc_mismatch"
	| "message_too_small"
	| "message_too_large"
	| "header_parse_failed"
	| "buffer_overflow"
	| "too_many_errors";

export class KiroParseError extends KiroError {
	constructor(
		public kind: ParseErrorKind,
		message: string,
	) {
		super("PARSE_ERROR", message);
		this.name = "KiroParseError";
	}
}

// --- Chat request types (matches official kiro-cli-chat Smithy protocol) ---

export interface KiroImage {
	format: "jpeg" | "png" | "gif" | "webp";
	source: { bytes: string }; // base64
}

export interface KiroToolSpecification {
	name: string;
	description: string;
	inputSchema: { json: Record<string, unknown> };
}

export interface KiroTool {
	toolSpecification: KiroToolSpecification;
}

export interface KiroToolResult {
	toolUseId: string;
	content: Array<Record<string, unknown>>;
	status?: string;
	isError?: boolean;
}

export interface KiroUserInputMessageContext {
	toolResults?: KiroToolResult[];
	tools?: KiroTool[];
}

export interface KiroUserInputMessage {
	content: string;
	modelId: string;
	userInputMessageContext?: KiroUserInputMessageContext;
	userIntent?: string;
	origin?: string;
	images?: KiroImage[];
	cachePoint?: string;
	clientCacheConfig?: Record<string, unknown>;
	agentMode?: string;
	source?: string;
}

export interface KiroHistoryUserMessage {
	userInputMessage: {
		content: string;
		modelId: string;
		origin?: string;
		images?: KiroImage[];
		userInputMessageContext?: KiroUserInputMessageContext;
	};
}

export interface KiroHistoryAssistantMessage {
	assistantResponseMessage: {
		content?: string;
		toolUses?: Array<{
			toolUseId: string;
			name: string;
			input: Record<string, unknown>;
		}>;
	};
}

export type KiroHistoryMessage = KiroHistoryUserMessage | KiroHistoryAssistantMessage;

export interface KiroEnvState {
	operatingSystem: "LINUX" | "MAC" | "WINDOWS";
	currentWorkingDirectory: string;
	environmentVariables?: Array<{ key: string; value: string }>;
	timezoneOffset?: string;
}

export interface KiroShellState {
	shellName: string;
}

export interface KiroConversationState {
	conversationId: string;
	currentMessage: {
		userInputMessage: KiroUserInputMessage;
	};
	chatTriggerType?: string;
	history?: KiroHistoryMessage[];
	agentContinuationId?: string;
	agentTaskType?: string;
	workspaceId?: string;
	envState?: KiroEnvState;
	shellState?: KiroShellState;
}

/** Full chat request body matching the official Smithy protocol. */
export interface KiroChatRequest {
	conversationState: KiroConversationState;
	profileArn?: string; // injected by server from credentials
}

// --- MCP ---

export interface McpRequest {
	id: string;
	jsonrpc: "2.0";
	method: "tools/call" | "tools/list" | (string & {});
	params: Record<string, unknown>;
}

export interface McpResponse {
	error?: { code?: number; message?: string };
	id: string;
	jsonrpc: string;
	result?: {
		content: Array<{ type: string; text: string }>;
		isError: boolean;
	};
}
