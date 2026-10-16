// Mirror of shared/editor-provider-contracts.ts. These are frozen wire shapes
// for editor Phases 6-9; no handler in this executor implements them yet and no
// feature for them is advertised in Capabilities. The shared fixture
// shared/editor-provider-contracts.golden.json is decoded strictly here and
// type-checked on the TS side. Required-nullable fields are pointers WITHOUT
// omitempty (they always encode, as null when absent); optional fields use
// omitempty.
package rpc

type EditorProviderCapabilities struct {
	BoundedRead  bool `json:"boundedRead"`
	V1Save       bool `json:"v1Save"`
	RawRead      bool `json:"rawRead"`
	Stage        bool `json:"stage"`
	DurableApply bool `json:"durableApply"`
	DurableQuery bool `json:"durableQuery"`
	Watch        bool `json:"watch"`
}

type EditorResourceIdentity struct {
	DeviceID          string `json:"deviceId"`
	PathFlavor        string `json:"pathFlavor"`
	CanonicalPath     string `json:"canonicalPath"`
	WorkspaceIdentity string `json:"workspaceIdentity"`
	RuntimeEpoch      string `json:"runtimeEpoch"`
	RuntimeGeneration int64  `json:"runtimeGeneration"`
	FencingToken      int64  `json:"fencingToken"`
}

type EditorRawReadPreflight struct {
	ResourceKey string                 `json:"resourceKey"`
	Identity    EditorResourceIdentity `json:"identity"`
	MaxBytes    int64                  `json:"maxBytes"`
	TimeoutMs   int64                  `json:"timeoutMs"`
}

type EditorRawReadOpen struct {
	ReadID           string                 `json:"readId"`
	SourceGeneration string                 `json:"sourceGeneration"`
	TotalBytes       int64                  `json:"totalBytes"`
	RawDigest        string                 `json:"rawDigest"`
	Identity         EditorResourceIdentity `json:"identity"`
}

type EditorRawReadChunkRequest struct {
	ReadID    string `json:"readId"`
	Offset    int64  `json:"offset"`
	MaxBytes  int64  `json:"maxBytes"`
	TimeoutMs int64  `json:"timeoutMs"`
}

type EditorRawReadChunk struct {
	ReadID           string `json:"readId"`
	SourceGeneration string `json:"sourceGeneration"`
	Offset           int64  `json:"offset"`
	Bytes            int64  `json:"bytes"`
	DataB64          string `json:"dataB64"`
	EOF              bool   `json:"eof"`
}

type EditorRawReadClose struct {
	ReadID string `json:"readId"`
	Abort  bool   `json:"abort"`
}

type EditorStageOpen struct {
	ResourceKey   string `json:"resourceKey"`
	MaxBytes      int64  `json:"maxBytes"`
	IdleTimeoutMs int64  `json:"idleTimeoutMs"`
}

type EditorStageHandle struct {
	StageID     string `json:"stageId"`
	ResourceKey string `json:"resourceKey"`
	ExpiresAt   int64  `json:"expiresAt"`
}

type EditorStageAppend struct {
	StageID string `json:"stageId"`
	Seq     int64  `json:"seq"`
	Offset  int64  `json:"offset"`
	Bytes   int64  `json:"bytes"`
	Digest  string `json:"digest"`
	DataB64 string `json:"dataB64"`
}

type EditorStageSeal struct {
	StageID    string `json:"stageId"`
	TotalBytes int64  `json:"totalBytes"`
	RawDigest  string `json:"rawDigest"`
}

type EditorStageAbort struct {
	StageID string `json:"stageId"`
	Reason  string `json:"reason"`
}

type EditorApplyRequest struct {
	OperationID   string `json:"operationId"`
	RequestDigest string `json:"requestDigest"`
	StageID       string `json:"stageId"`
	// Required; nil encodes as null and means the target must not exist.
	ExpectedRawHash *string                `json:"expectedRawHash"`
	Identity        EditorResourceIdentity `json:"identity"`
	MetadataPolicy  string                 `json:"metadataPolicy"`
}

type EditorProviderError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EditorApplyResult struct {
	OperationID string  `json:"operationId"`
	Outcome     string  `json:"outcome"`
	RawDigest   *string `json:"rawDigest"`
	TotalBytes  *int64  `json:"totalBytes"`
	// Optional; omitted on committed.
	Error *EditorProviderError `json:"error,omitempty"`
}

type EditorQueryRequest struct {
	Namespace     string `json:"namespace"`
	OperationID   string `json:"operationId"`
	RequestDigest string `json:"requestDigest"`
}

type EditorQueryResult struct {
	OperationID string `json:"operationId"`
	Outcome     string `json:"outcome"`
	// Required; nil encodes as null when no durable result exists.
	Result *EditorApplyResult `json:"result"`
}

type EditorWatchOpen struct {
	ResourceKey string `json:"resourceKey"`
}

type EditorWatchHandle struct {
	SubscriptionID string `json:"subscriptionId"`
	ResourceKey    string `json:"resourceKey"`
	Generation     int64  `json:"generation"`
}

type EditorWatchNotify struct {
	SubscriptionID string `json:"subscriptionId"`
	Seq            int64  `json:"seq"`
	Generation     int64  `json:"generation"`
	Reason         string `json:"reason"`
}

type EditorWatchClose struct {
	SubscriptionID string `json:"subscriptionId"`
}

var (
	EditorPathFlavors       = []string{"posix", "windows"}
	EditorStageAbortReasons = []string{"cancel", "idle_timeout", "superseded"}
	EditorMetadataPolicies  = []string{"preserve", "reject"}
	EditorApplyOutcomes     = []string{"committed", "rejected", "uncertain"}
	EditorQueryOutcomes     = []string{"committed", "rejected", "uncertain", "not_found", "proven_not_executed"}
	EditorWatchReasons      = []string{"content_changed", "metadata_changed", "deleted", "replaced", "generation_reset"}
	EditorProviderErrors    = []string{
		"EDITOR_IDENTITY_CHANGED",
		"EDITOR_RUNTIME_CHANGED",
		"EDITOR_FENCING_REJECTED",
		"EDITOR_STAGE_DIGEST_MISMATCH",
		"EDITOR_STAGE_SEAL_FAILED",
		"EDITOR_APPLY_REJECTED",
		"EDITOR_NOT_FOUND",
		"EDITOR_UNCERTAIN",
		"EDITOR_PROVIDER_UNSUPPORTED",
	}
)
