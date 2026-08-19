/**
 * CodeWhisperer / Kiro wire protocol types.
 *
 * These describe the on-the-wire shapes exchanged with AWS CodeWhisperer-style
 * chat endpoints. They are shared by two consumers:
 *
 * - the NarraFork core, for the NUG `kiro` channel (`/v1/kiro/chat`);
 * - bundled plugin code that talks to the upstream service directly.
 *
 * Because both sides must agree byte-for-byte, the definitions live here rather
 * than beside either consumer. Field names mirror the wire format exactly —
 * including the deliberate snake_case `reasoning_content` — so do not "tidy"
 * them.
 *
 * Credential, quota and account-management types are NOT here: those belong to
 * whichever component owns Kiro accounts.
 */

/** A single decoded event from the upstream AWS event-stream response. */
export interface StreamEvent {
	eventType: string;
	data: Record<string, unknown>;
}

export interface KiroImage {
	format: "jpeg" | "png" | "gif" | "webp";
	source: { bytes: string };
	/**
	 * Optional content-addressed reference (e.g. "sha256:<hex>") used by the NUG
	 * image dedup protocol. When set on a history image, `source.bytes` may be
	 * empty: the NUG gateway restores the payload from its cache by this ref.
	 * Ignored by all non-NUG transports.
	 */
	imageRef?: string;
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

/**
 * Reasoning/thinking content echoed back in the conversation history.
 *
 * Mirrors the Kiro native `ReasoningContentForHistory` shape (verified against
 * the kiro-cli 2.2.1 binary): a single object holding the visible thinking text
 * with its verification `signature`, and/or a base64 `redactedContent` block for
 * encrypted thinking. The Anthropic-backed Kiro models require the signature to
 * be echoed verbatim so previous thinking can be reused across turns.
 */
export interface KiroReasoningContent {
	reasoningText?: {
		text: string;
		signature?: string;
	};
	redactedContent?: string;
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
		/**
		 * Reasoning/thinking content to echo back to the Kiro service.
		 *
		 * NOTE: The Kiro streaming client binary (chat_cli) serializes this
		 * field as `reasoning_content` (snake_case) rather than camelCase,
		 * despite most other Kiro protocol fields using camelCase.  When this
		 * field is sent as camelCase the upstream NUG/Kiro proxy returns a
		 * 400 ValidationException.  Using snake_case here to match the actual
		 * wire format expected by the service.
		 */
		reasoning_content?: KiroReasoningContent;
		toolUses?: Array<{
			toolUseId: string;
			name: string;
			input: Record<string, unknown>;
		}>;
	};
}

export type KiroHistoryMessage = KiroHistoryUserMessage | KiroHistoryAssistantMessage;

export interface KiroChatRequest {
	conversationState: {
		conversationId: string;
		currentMessage: {
			userInputMessage: {
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
			};
		};
		chatTriggerType?: string;
		history?: KiroHistoryMessage[];
		agentContinuationId?: string;
		agentTaskType?: string;
		workspaceId?: string;
		/**
		 * Telemetry only; nothing branches on it. Typed structurally rather than as a loose
		 * record so this request stays assignable to the server-side `KiroChatRequest`, whose
		 * `KiroEnvState` requires both fields. `buildKiroRequest` always populates both, so the
		 * looser type described a shape that never occurred while making the two declarations
		 * incompatible at every boundary that passes a request from here to there.
		 */
		envState?: {
			operatingSystem: "LINUX" | "MAC" | "WINDOWS";
			currentWorkingDirectory: string;
			environmentVariables?: Array<{ key: string; value: string }>;
			timezoneOffset?: string;
		};
		/** Telemetry only, and always populated by `buildKiroRequest`. Typed for the same
		 * reason as `envState` above: the server-side `KiroShellState` requires `shellName`. */
		shellState?: { shellName: string };
	};
	profileArn?: string;
}
