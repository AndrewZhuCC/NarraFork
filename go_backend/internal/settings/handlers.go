package settings

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/restart"
)

type ContainerProxyRuntime interface {
	EnsureContainerProxy(ctx context.Context, enabled bool, port int) error
}

type Handler struct {
	Store        *config.Store
	ProxyRuntime ContainerProxyRuntime
	Restart      *restart.Manager
}

var settingsExecutablePath = os.Executable
var settingsStartReplacement = startReplacementProcess

const testModelPromptMaxChars = 10000

func NewHandler(store *config.Store) *Handler { return &Handler{Store: store} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.get))
	r.Patch("/", httpx.Handler(h.patch))
	r.Get("/context-thresholds", httpx.Handler(h.contextThresholds))
	r.Post("/test-model", httpx.Handler(h.testModel))
	r.Post("/generate-tls", httpx.Handler(h.generateTLS))
	r.Post("/retry-rules", httpx.Handler(h.addRetryRule))
}

func (h *Handler) get(w http.ResponseWriter, _ *http.Request) error {
	if h.Store == nil {
		return httpx.BadRequest("Settings unavailable")
	}
	raw := h.Store.Raw()
	ensureOptionalProviderArraysForResponse(raw)
	normalizeProviderCompatibilityForResponse(raw)
	augmentProviderModelSettings(raw)
	sanitizeSettingsForResponse(raw)
	httpx.WriteJSON(w, http.StatusOK, raw)
	return nil
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) error {
	if h.Store == nil {
		return httpx.BadRequest("Settings unavailable")
	}
	var patch map[string]any
	if err := httpx.DecodeLooseJSON(r, &patch); err != nil {
		return err
	}
	patch = normalizeLegacySettingsPatch(patch)
	current := h.Store.Raw()
	if err := prepareSettingsPatch(current, patch); err != nil {
		return err
	}
	oldHost, oldPort, oldTLSEnabled := serverAddressState(current)
	oldProxyEnabled, oldProxyPort := containerProxyState(current)
	prospective := cloneMap(current)
	mergeSettingsPatch(prospective, patch)
	newProxyEnabled, newProxyPort := containerProxyState(prospective)
	proxyRuntimeChanged := oldProxyEnabled != newProxyEnabled || oldProxyPort != newProxyPort
	if proxyRuntimeChanged && h.ProxyRuntime != nil {
		if err := h.ProxyRuntime.EnsureContainerProxy(r.Context(), newProxyEnabled, newProxyPort); err != nil {
			return err
		}
	}
	next, err := h.Store.Patch(patch)
	if err != nil {
		if proxyRuntimeChanged && h.ProxyRuntime != nil {
			_ = h.ProxyRuntime.EnsureContainerProxy(context.Background(), oldProxyEnabled, oldProxyPort)
		}
		return err
	}
	next, _ = h.applyProviderRemovalSideEffects(current, next)
	newHost, newPort, newTLSEnabled := serverAddressState(next)
	if oldHost != newHost || oldPort != newPort || oldTLSEnabled != newTLSEnabled {
		restartPayload, restartErr := h.serverRestartResponse(r.Context(), newHost, newPort, newTLSEnabled)
		if restartErr != nil {
			return restartErr
		}
		for key, value := range restartPayload {
			next[key] = value
		}
	}
	ensureOptionalProviderArraysForResponse(next)
	normalizeProviderCompatibilityForResponse(next)
	augmentProviderModelSettings(next)
	sanitizeSettingsForResponse(next)
	httpx.WriteJSON(w, http.StatusOK, next)
	return nil
}

func ensureOptionalProviderArraysForResponse(raw map[string]any) {
	for _, field := range []string{"nkpProviders", "nugProviders", "clineProviders"} {
		if _, ok := raw[field].([]any); !ok {
			raw[field] = []any{}
		}
	}
}

func normalizeProviderCompatibilityForResponse(raw map[string]any) {
	customProviders := anySlice(raw["customApiProviders"])
	if len(customProviders) > 0 {
		raw["openaiProviders"] = customApiProvidersToOpenAI(customProviders)
		raw["anthropicProviders"] = customApiProvidersToAnthropic(customProviders)
		return
	}
	openaiProviders := anySlice(raw["openaiProviders"])
	anthropicProviders := anySlice(raw["anthropicProviders"])
	if len(openaiProviders) > 0 || len(anthropicProviders) > 0 {
		raw["customApiProviders"] = deriveCustomApiProvidersFromLegacy(openaiProviders, anthropicProviders)
	}
}

func augmentProviderModelSettings(raw map[string]any) {
	kiroModels := loadKiroModelsForSettings(raw)
	if len(kiroModels) == 0 {
		raw["kiroModels"] = nil
	} else {
		raw["kiroModels"] = kiroModels
	}
	openaiCache := loadProviderModelCache("openai")
	raw["openaiModels"] = aggregateSettingsModels(raw, "openaiProviders", "openai", openaiCache)
	raw["openaiModelsGrouped"] = groupedSettingsModels(raw, "openaiProviders", "openai", openaiCache)
	raw["anthropicModelsGrouped"] = groupedSettingsModels(raw, "anthropicProviders", "anthropic", loadProviderModelCache("anthropic"))
	raw["nkpModelsGrouped"] = groupedSettingsModels(raw, "nkpProviders", "nkp", loadProviderModelCache("nkp"))
	raw["nugModelsGrouped"] = groupedSettingsModels(raw, "nugProviders", "nug", loadProviderModelCache("nug"))
	raw["clineModelsGrouped"] = clineEnabledModelsGrouped(raw, loadProviderModelCache("cline"))
	raw["customApiQuotas"] = loadObjectCache("custom-api-quotas.json")
	raw["nkpQuotas"] = loadQuotaObjectCache("nkp-quotas.json", "quotaBalance", "quotaTotalGranted")
	delete(raw, "nugQuotas")
	raw["codexAvailable"] = len(loadCodexCredentialEntries()) > 0
	raw["codexModels"] = builtinCodexModels()
	raw["builtinModelContextWindows"] = builtinCodexContextWindows()
	raw["lanAddresses"] = lanAddresses()
	raw["summaryModelAvailable"] = summaryModelAvailable(raw)
}

func sanitizeSettingsForResponse(raw map[string]any) {
	if auth, ok := raw["auth"].(map[string]any); ok {
		auth["jwtSecret"] = nil
	}
	if server, ok := raw["server"].(map[string]any); ok {
		if tls, ok := server["tls"].(map[string]any); ok {
			if stringValue(tls["passphrase"]) != "" {
				tls["passphrase"] = "********"
			}
		}
	}
	maskProviderArray(raw, "customApiProviders", "apiKey")
	maskProviderArray(raw, "openaiProviders", "apiKey")
	maskProviderArray(raw, "anthropicProviders", "apiKey")
	maskProviderArray(raw, "nkpProviders", "apiKey")
	maskProviderArray(raw, "nugProviders", "apiKey")
	maskProviderArray(raw, "nugProviders", "oauthClientSecret")
	maskProviderArray(raw, "clineProviders", "accessToken")
	if vnet, ok := raw["vnet"].(map[string]any); ok {
		if token := stringValue(vnet["relayToken"]); token != "" {
			vnet["relayToken"] = maskSecret(token)
		}
	}
}

func maskProviderArray(raw map[string]any, field, secretKey string) {
	providers, _ := raw[field].([]any)
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if secret := stringValue(provider[secretKey]); secret != "" {
			provider[secretKey] = maskSecret(secret)
		}
	}
}

func maskSecret(value string) string {
	// Mirror the TypeScript maskApiKey (server/routes/settings.ts): operate on
	// UTF-16 code units (key.length / key.slice(-4)), not Go byte length. For the
	// usual ASCII API keys these are identical, but a non-ASCII secret (e.g. a
	// relay token) would otherwise diverge on the <=4 length check and could be
	// byte-sliced mid-rune, emitting invalid UTF-8 in the masked display value.
	if httpx.JSStringLength(value) <= 4 {
		return strings.Repeat("*", httpx.JSStringLength(value))
	}
	return "********" + httpx.JSStringSliceFromEnd(value, 4)
}

func lanAddresses() []string {
	interfaces, _ := net.Interfaces()
	addresses := []string{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil {
				continue
			}
			if ip[0] == 10 || (ip[0] == 192 && ip[1] == 168) || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) {
				addresses = append(addresses, ip.String())
			}
		}
	}
	return addresses
}

func summaryModelAvailable(raw map[string]any) bool {
	agent, _ := raw["agent"].(map[string]any)
	model := stringValue(agent["summaryModel"])
	if strings.TrimSpace(model) == "" {
		return false
	}
	if strings.Contains(model, ":") {
		return true
	}
	return model != ""
}

func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".narrafork")
}

func loadProviderModelCache(kind string) map[string][]map[string]any {
	fileName := kind + "-models-providers.json"
	if kind == "cline" {
		fileName = "cline-models.json"
	}
	path := filepath.Join(dataDir(), fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]map[string]any{}
	}
	cache := map[string][]map[string]any{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string][]map[string]any{}
	}
	return cache
}

func loadObjectCache(fileName string) map[string]any {
	data, err := os.ReadFile(filepath.Join(dataDir(), fileName))
	if err != nil {
		return map[string]any{}
	}
	cache := map[string]any{}
	if err := json.Unmarshal(data, &cache); err != nil {
		return map[string]any{}
	}
	return cache
}

func loadQuotaObjectCache(fileName string, keys ...string) map[string]any {
	raw := loadObjectCache(fileName)
	out := map[string]any{}
	for providerID, value := range raw {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		entry := map[string]any{}
		for _, key := range keys {
			entry[key] = record[key]
		}
		out[providerID] = entry
	}
	return out
}

func groupedSettingsModels(raw map[string]any, providerField, kind string, cache map[string][]map[string]any) []any {
	providers, _ := raw[providerField].([]any)
	groups := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		providerID := stringValue(provider["id"])
		models, ok := cache[providerID]
		if !ok {
			continue
		}
		groups = append(groups, map[string]any{
			"providerId":   providerID,
			"providerName": firstString(stringValue(provider["name"]), stringValue(provider["prefix"]), kind),
			"models":       models,
		})
	}
	return groups
}

func aggregateSettingsModels(raw map[string]any, providerField, kind string, cache map[string][]map[string]any) []any {
	providers, _ := raw[providerField].([]any)
	active := map[string]bool{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if ok && !boolValue(provider["disabled"]) {
			active[stringValue(provider["id"])] = true
		}
	}
	seen := map[string]bool{}
	out := []any{}
	idKey := "id"
	if kind == "nkp" {
		idKey = "model_id"
	}
	for providerID, models := range cache {
		if !active[providerID] {
			continue
		}
		for _, model := range models {
			modelID := settingsModelID(model, idKey)
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			out = append(out, model)
		}
	}
	return out
}

func clineEnabledModelsGrouped(raw map[string]any, cache map[string][]map[string]any) []any {
	providers, _ := raw["clineProviders"].([]any)
	groups := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		enabled := stringList(provider["enabledModels"])
		if len(enabled) == 0 {
			continue
		}
		pool := cache[stringValue(provider["id"])]
		models := make([]map[string]any, 0, len(enabled))
		for _, modelID := range enabled {
			if match := findCachedModel(pool, modelID); match != nil {
				models = append(models, match)
			} else {
				models = append(models, map[string]any{"id": modelID})
			}
		}
		groups = append(groups, map[string]any{
			"providerId":   stringValue(provider["id"]),
			"providerName": firstString(stringValue(provider["name"]), "Cline"),
			"models":       models,
		})
	}
	return groups
}

func findCachedModel(models []map[string]any, modelID string) map[string]any {
	for _, model := range models {
		if stringValue(model["id"]) == modelID {
			return model
		}
	}
	return nil
}

func loadKiroModelsForSettings(raw map[string]any) []any {
	entries := loadKiroCredentialEntries(raw)
	seen := map[string]bool{}
	models := []any{}
	for _, entry := range entries {
		for _, modelID := range stringList(entry["availableModels"]) {
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			models = append(models, map[string]any{"model_id": modelID, "model_short_name": modelID, "model_name": modelID})
		}
	}
	return models
}

func loadKiroCredentialEntries(raw map[string]any) []map[string]any {
	path := filepath.Join(dataDir(), "kiro", "credentials.json")
	if kiro, ok := raw["kiro"].(map[string]any); ok {
		if configured := strings.TrimSpace(stringValue(kiro["credentialsPath"])); configured != "" {
			path = configured
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []map[string]any{}
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return []map[string]any{}
	}
	if strings.HasPrefix(trimmed, "[") {
		entries := []map[string]any{}
		if err := json.Unmarshal(data, &entries); err != nil {
			return []map[string]any{}
		}
		return entries
	}
	entry := map[string]any{}
	if err := json.Unmarshal(data, &entry); err != nil {
		return []map[string]any{}
	}
	return []map[string]any{entry}
}

func loadCodexCredentialEntries() []map[string]any {
	data, err := os.ReadFile(filepath.Join(dataDir(), "codex-credentials.json"))
	if err != nil {
		return []map[string]any{}
	}
	entries := []map[string]any{}
	if err := json.Unmarshal(data, &entries); err != nil {
		return []map[string]any{}
	}
	return entries
}

func builtinCodexModels() []any {
	return []any{
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.3-codex-spark",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex-mini",
	}
}

func builtinCodexContextWindows() map[string]any {
	windows := map[string]any{}
	for _, model := range builtinCodexModels() {
		modelID := stringValue(model)
		if modelID != "" {
			windows["codex:"+modelID] = float64(272000)
		}
	}
	return windows
}

func settingsModelID(model map[string]any, idKey string) string {
	if value := stringValue(model[idKey]); value != "" {
		return value
	}
	if idKey == "model_id" {
		if value := stringValue(model["modelId"]); value != "" {
			return value
		}
		return stringValue(model["id"])
	}
	return ""
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func isDangerReflectionLevel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "light", "standard", "strict":
		return true
	default:
		return false
	}
}

func normalizeLegacySettingsPatch(patch map[string]any) map[string]any {
	agent, ok := patch["agent"].(map[string]any)
	if !ok {
		return patch
	}
	if stringValue(agent["defaultPermissionMode"]) == "plan" {
		agent["defaultPermissionMode"] = "default"
		agent["defaultStartInPlanMode"] = true
	}
	if _, hasDanger := agent["dangerSkipReadOnlyConfirmations"]; !hasDanger {
		if value, hasLegacy := agent["yoloSkipReadOnlyConfirmations"]; hasLegacy {
			agent["dangerSkipReadOnlyConfirmations"] = value
		}
	}
	if rawLevel, hasLevel := agent["dangerReflectionLevel"]; hasLevel {
		level := strings.ToLower(strings.TrimSpace(stringValue(rawLevel)))
		if isDangerReflectionLevel(level) {
			agent["dangerReflectionLevel"] = level
			agent["dangerReflectionEnabled"] = level != "off"
		}
	} else if enabled, ok := agent["dangerReflectionEnabled"].(bool); ok {
		if enabled {
			agent["dangerReflectionLevel"] = "standard"
		} else {
			agent["dangerReflectionLevel"] = "off"
		}
	}
	return patch
}

func prepareSettingsPatch(current, patch map[string]any) error {
	if err := validateSettingsPatchKeys(patch); err != nil {
		return err
	}
	if err := rejectAuthJWTSecretPatch(patch); err != nil {
		return err
	}
	stripUnknownSettingsPatchFields(patch)
	if err := validateServerSettingsPatch(patch); err != nil {
		return err
	}
	if err := validatePathsSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateAgentSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateChaptersSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateEditorSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateCodexSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateRoutinesSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateContainersSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateVNetSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateUpdateSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateSharesSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateAuthSettingsPatch(patch); err != nil {
		return err
	}
	if err := validateProviderSettingsPatch(patch); err != nil {
		return err
	}
	if err := preserveMaskedSecrets(current, patch); err != nil {
		return err
	}
	syncLegacyProvidersFromCustomPatch(patch)
	syncCustomProvidersFromLegacyPatch(current, patch)
	if err := validateProviderPrefixes(current, patch); err != nil {
		return err
	}
	if err := validateTLSFiles(patch); err != nil {
		return err
	}
	return nil
}

var allowedSettingsPatchRoots = map[string]struct{}{
	"server":             {},
	"paths":              {},
	"agent":              {},
	"chapters":           {},
	"containers":         {},
	"editor":             {},
	"auth":               {},
	"customApiProviders": {},
	"openaiProviders":    {},
	"anthropicProviders": {},
	"nkpProviders":       {},
	"nugProviders":       {},
	"clineProviders":     {},
	"codex":              {},
	"routines":           {},
	"update":             {},
	"vnet":               {},
	"shares":             {},
}

var settingsPatchNestedKeyAllowlist = map[string]map[string]struct{}{
	"server":     stringSet("port", "host", "openBrowser", "tls"),
	"paths":      stringSet("defaultProjectDir"),
	"chapters":   stringSet("maxActiveWorktrees", "maxActiveContainers", "worktreeSizeWarningMb", "autoSaveOnDormant", "dormantAfterMinutes"),
	"containers": stringSet("portRangeStart", "portRangeEnd", "proxy"),
	"editor":     stringSet("type"),
	"auth":       stringSet("registrationOpen"),
	"codex":      stringSet("proxy", "loadBalancingMode", "tierOrder", "defaultReasoningEffort", "useWebSearch", "useImageGeneration"),
	"routines":   stringSet("disabledRoutines", "enabledRoutines"),
	"update":     stringSet("serverUrl", "product", "channel", "checkIntervalMinutes", "autoDownload"),
	"vnet":       stringSet("enabled", "relayToken", "allowAnonymousRelay", "maxPeersPerNetwork", "maxMessageBytes", "udp"),
	"shares":     stringSet("defaultExpiryHours", "maxFileSizeMb"),
	"agent": stringSet(
		"defaultModel", "defaultPermissionMode", "defaultStartInPlanMode", "summaryModel",
		"customModels", "hiddenModels", "maxTurns", "subagentModels", "subagentAllowedModels",
		"legacyEncoding", "freshShellEnv", "translateReasoning", "requestDumpEnabled",
		"requestDumpErrorsOnly", "defaultRelaxedPlan", "planModeAllowInlinePlan",
		"planReflectionAutoApprove",
		"planReflectionAllowAutoCompact", "questionReflectionEnabled", "questionReflectionTimeoutMs",
		"dangerReflectionLevel", "dangerReflectionEnabled", "dangerSkipReadOnlyConfirmations",
		"defaultReasoningEffort", "maxTransientRetries", "silentToolCallThreshold",
		"retryBackoffCeilMs", "firstTokenTimeoutMs", "customRetryRules", "modelContextWindows",
		"whitelistDirs", "blacklistDirs", "commandWhitelist", "commandBlacklist", "contextThresholds",
		"autoCompactKeepPairs", "autoCompactPruneThreshold", "webFetchPolicy", "defaultSystemPrompt",
		"modelAggregations", "providerOrder", "disabledProviders",
	),
}

var customAPIProviderPatchAllowedKeys = stringSet(
	"id", "name", "prefix", "apiKey", "baseUrl", "defaultModel", "protocol",
	"defaultContextWindow", "defaultReasoningEffort", "proxy", "tlsRejectUnauthorized",
	"codexAccountId", "codexWebSocket", "codexWebSearch", "codexImageGeneration", "disabled",
)

var openAIProviderPatchAllowedKeys = stringSet(
	"id", "name", "prefix", "apiKey", "baseUrl", "defaultModel", "responsesApi", "apiMode",
	"codexAccountId", "codexWebSocket", "codexWebSearch", "codexImageGeneration",
	"defaultContextWindow", "disabled",
)

var anthropicProviderPatchAllowedKeys = stringSet(
	"id", "name", "prefix", "apiKey", "baseUrl", "defaultModel", "defaultReasoningEffort",
	"proxy", "tlsRejectUnauthorized", "officialApi", "disabled",
)

var nkpProviderPatchAllowedKeys = stringSet("id", "name", "prefix", "apiKey", "baseUrl", "defaultModel", "disabled")

var nugProviderPatchAllowedKeys = stringSet(
	"id", "name", "prefix", "apiKey", "baseUrl", "defaultModel", "nugUsername", "nugUserId",
	"oauthClientId", "oauthClientSecret", "oauthDeviceId", "oauthCallbackUrl", "disabled",
)

var clineProviderPatchAllowedKeys = stringSet(
	"id", "name", "prefix", "baseUrl", "accessToken", "defaultModel", "defaultContextWindow",
	"enabledModels", "disabled",
)

func stringSet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func rejectAuthJWTSecretPatch(patch map[string]any) error {
	authPatch, ok := patch["auth"].(map[string]any)
	if !ok {
		return nil
	}
	if _, exists := authPatch["jwtSecret"]; exists {
		return httpx.ValidationError("auth.jwtSecret cannot be updated")
	}
	return nil
}

func stripUnknownSettingsPatchFields(patch map[string]any) {
	for root, allowlist := range settingsPatchNestedKeyAllowlist {
		stripUnknownMapKeys(patch[root], allowlist)
	}
	if server, ok := patch["server"].(map[string]any); ok {
		stripUnknownMapKeys(server["tls"], stringSet("enabled", "certFile", "keyFile", "passphrase", "caFile"))
	}
	if containers, ok := patch["containers"].(map[string]any); ok {
		stripUnknownMapKeys(containers["proxy"], stringSet("enabled", "port"))
	}
	if vnet, ok := patch["vnet"].(map[string]any); ok {
		stripUnknownMapKeys(vnet["udp"], stringSet("enabled", "host", "port"))
	}
	stripUnknownAgentPatchFields(patch)
	stripUnknownProviderPatchFields(patch)
}

func stripUnknownAgentPatchFields(patch map[string]any) {
	agent, ok := patch["agent"].(map[string]any)
	if !ok {
		return
	}
	stripUnknownMapKeys(agent["subagentModels"], stringSet("explore", "plan"))
	stripUnknownMapKeys(agent["subagentAllowedModels"], stringSet("explore", "plan", "general"))
	if thresholds, ok := agent["contextThresholds"].(map[string]any); ok {
		stripUnknownMapKeys(thresholds, stringSet("standard", "large"))
		stripUnknownMapKeys(thresholds["standard"], stringSet("pruneStart", "compactStart"))
		stripUnknownMapKeys(thresholds["large"], stringSet("pruneStart", "compactStart"))
	}
	if policy, ok := agent["webFetchPolicy"].(map[string]any); ok {
		stripUnknownMapKeys(policy, stringSet("allowAll", "whitelist", "blacklist", "proxy"))
		stripUnknownArrayObjectKeys(policy["whitelist"], stringSet("pattern", "enabled"))
		stripUnknownArrayObjectKeys(policy["blacklist"], stringSet("pattern", "enabled"))
		stripUnknownMapKeys(policy["proxy"], stringSet("mode", "url"))
	}
	stripUnknownArrayObjectKeys(agent["customModels"], stringSet("value", "label", "provider", "channel", "channelType"))
	stripUnknownArrayObjectKeys(agent["customRetryRules"], stringSet("id", "domain", "statusCode", "keyword", "enabled", "note"))
	stripUnknownArrayObjectKeys(agent["modelAggregations"], stringSet("id", "name", "models", "routingMode"))
	stripUnknownArrayObjectKeys(agent["whitelistDirs"], stringSet("path", "accessLevel", "enabled"))
	stripUnknownArrayObjectKeys(agent["blacklistDirs"], stringSet("path", "denyLevel", "enabled"))
	stripUnknownArrayObjectKeys(agent["commandWhitelist"], stringSet("pattern", "enabled"))
	stripUnknownArrayObjectKeys(agent["commandBlacklist"], stringSet("pattern", "denyPrompt", "enabled"))
}

func stripUnknownProviderPatchFields(patch map[string]any) {
	stripUnknownArrayObjectKeys(patch["customApiProviders"], customAPIProviderPatchAllowedKeys)
	stripUnknownArrayObjectKeys(patch["openaiProviders"], openAIProviderPatchAllowedKeys)
	stripUnknownArrayObjectKeys(patch["anthropicProviders"], anthropicProviderPatchAllowedKeys)
	stripUnknownArrayObjectKeys(patch["nkpProviders"], nkpProviderPatchAllowedKeys)
	stripUnknownArrayObjectKeys(patch["nugProviders"], nugProviderPatchAllowedKeys)
	stripUnknownArrayObjectKeys(patch["clineProviders"], clineProviderPatchAllowedKeys)
}

func stripUnknownArrayObjectKeys(value any, allowlist map[string]struct{}) {
	items, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		stripUnknownMapKeys(item, allowlist)
	}
}

func stripUnknownMapKeys(value any, allowlist map[string]struct{}) {
	item, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key := range item {
		if _, allowed := allowlist[key]; !allowed {
			delete(item, key)
		}
	}
}

func validateSettingsPatchKeys(patch map[string]any) error {
	for key, value := range patch {
		if _, ok := allowedSettingsPatchRoots[key]; !ok {
			return httpx.ValidationError("unsupported settings field: " + key)
		}
		switch key {
		case "server", "paths", "agent", "chapters", "containers", "editor", "auth", "codex", "routines", "update", "vnet", "shares":
			if value != nil {
				if _, ok := value.(map[string]any); !ok {
					return httpx.ValidationError(key + " must be an object")
				}
			}
		case "customApiProviders", "openaiProviders", "anthropicProviders", "nkpProviders", "nugProviders", "clineProviders":
			if value != nil {
				if _, ok := value.([]any); !ok {
					return httpx.ValidationError(key + " must be an array")
				}
			}
		}
	}
	return nil
}

func patchIntValue(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed), true
		}
	}
	return 0, false
}

func validateServerSettingsPatch(patch map[string]any) error {
	rawServer, exists := patch["server"]
	if !exists {
		return nil
	}
	serverPatch, ok := rawServer.(map[string]any)
	if !ok {
		return httpx.ValidationError("server must be an object")
	}
	for key, value := range serverPatch {
		switch key {
		case "port":
			port, ok := patchIntValue(value)
			if !ok || port < 1 || port > 65535 {
				return httpx.ValidationError("server.port must be an integer between 1 and 65535")
			}
		case "host":
			if strings.TrimSpace(stringValue(value)) == "" || httpx.JSStringLength(strings.TrimSpace(stringValue(value))) > 255 {
				return httpx.ValidationError("server.host must be a non-empty string up to 255 characters")
			}
		case "openBrowser":
			valueText := strings.TrimSpace(stringValue(value))
			if valueText != "off" && valueText != "browser" && valueText != "app" {
				return httpx.ValidationError("server.openBrowser must be one of off, browser, or app")
			}
		case "tls":
			tlsPatch, ok := value.(map[string]any)
			if !ok {
				return httpx.ValidationError("server.tls must be an object")
			}
			for tlsKey, tlsValue := range tlsPatch {
				switch tlsKey {
				case "enabled":
					if _, ok := tlsValue.(bool); !ok {
						return httpx.ValidationError("server.tls.enabled must be boolean")
					}
				case "certFile", "keyFile", "caFile", "passphrase":
					if _, ok := tlsValue.(string); !ok {
						return httpx.ValidationError("server.tls." + tlsKey + " must be a string")
					}
				default:
					return httpx.ValidationError("server.tls." + tlsKey + " cannot be updated")
				}
			}
		default:
			return httpx.ValidationError("server." + key + " cannot be updated")
		}
	}
	return nil
}

func validatePathsSettingsPatch(patch map[string]any) error {
	rawPaths, exists := patch["paths"]
	if !exists {
		return nil
	}
	pathsPatch, ok := rawPaths.(map[string]any)
	if !ok {
		return httpx.ValidationError("paths must be an object")
	}
	for key, value := range pathsPatch {
		switch key {
		case "defaultProjectDir":
			if strings.TrimSpace(stringValue(value)) == "" {
				return httpx.ValidationError("paths.defaultProjectDir must be a non-empty string")
			}
		default:
			return httpx.ValidationError("paths." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAgentSettingsPatch(patch map[string]any) error {
	rawAgent, exists := patch["agent"]
	if !exists {
		return nil
	}
	agentPatch, ok := rawAgent.(map[string]any)
	if !ok {
		return httpx.ValidationError("agent must be an object")
	}
	for _, key := range []string{"defaultStartInPlanMode", "legacyEncoding", "freshShellEnv", "translateReasoning", "requestDumpEnabled", "requestDumpErrorsOnly", "defaultRelaxedPlan", "planModeAllowInlinePlan", "planReflectionAutoApprove", "questionReflectionEnabled", "dangerReflectionEnabled", "dangerSkipReadOnlyConfirmations"} {
		if value, ok := agentPatch[key]; ok {
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("agent." + key + " must be boolean")
			}
		}
	}
	if value, ok := agentPatch["dangerReflectionLevel"]; ok && !isDangerReflectionLevel(stringValue(value)) {
		return httpx.ValidationError("agent.dangerReflectionLevel must be off, light, standard, or strict")
	}
	if value, ok := agentPatch["summaryModel"]; ok {
		if _, ok := value.(string); !ok {
			return httpx.ValidationError("agent.summaryModel must be a string")
		}
	}
	for _, key := range []string{"defaultModel"} {
		if value, ok := agentPatch[key]; ok && strings.TrimSpace(stringValue(value)) == "" {
			return httpx.ValidationError("agent." + key + " must be a non-empty string")
		}
	}
	if value, ok := agentPatch["defaultPermissionMode"]; ok && !isSettingsPermissionMode(stringValue(value)) {
		return httpx.ValidationError("agent.defaultPermissionMode is not supported")
	}
	if value, ok := agentPatch["defaultReasoningEffort"]; ok {
		effort, ok := value.(string)
		if !ok || (effort != "none" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh") {
			return httpx.ValidationError("agent.defaultReasoningEffort is not supported")
		}
	}
	intRanges := map[string][2]int{
		"maxTurns":                    {1, 1000},
		"maxTransientRetries":         {-1, 100},
		"silentToolCallThreshold":     {-1, 1000},
		"retryBackoffCeilMs":          {1000, 300000},
		"firstTokenTimeoutMs":         {0, 600000},
		"questionReflectionTimeoutMs": {10000, 3600000},
		"autoCompactKeepPairs":        {1, 25},
		"autoCompactPruneThreshold":   {0, 100},
	}
	for key, bounds := range intRanges {
		if value, ok := agentPatch[key]; ok {
			if err := validateSettingsIntRange(value, "agent."+key, bounds[0], bounds[1]); err != nil {
				return err
			}
		}
	}
	if err := validateAgentCustomRetryRules(agentPatch); err != nil {
		return err
	}
	if err := validateAgentPromptAndAggregations(agentPatch); err != nil {
		return err
	}
	if value, ok := agentPatch["contextThresholds"]; ok {
		thresholds, ok := value.(map[string]any)
		if !ok {
			return httpx.ValidationError("agent.contextThresholds must be an object")
		}
		if err := validateAgentContextThresholds(thresholds); err != nil {
			return err
		}
	}
	if err := validateAgentModelSelectionSettings(agentPatch); err != nil {
		return err
	}
	if err := validateAgentPolicySettings(agentPatch); err != nil {
		return err
	}
	return validateAgentWebFetchPolicy(agentPatch)
}

func isSettingsPermissionMode(value string) bool {
	switch value {
	case "default", "acceptEdits", "bypassPermissions", "readOnly", "dontAsk":
		return true
	default:
		return false
	}
}

func validateAgentPromptAndAggregations(agentPatch map[string]any) error {
	if value, ok := agentPatch["defaultSystemPrompt"]; ok {
		prompt, ok := value.(string)
		if !ok {
			return httpx.ValidationError("agent.defaultSystemPrompt must be a string")
		}
		if httpx.JSStringLength(prompt) > 50000 {
			return httpx.ValidationError("agent.defaultSystemPrompt must be at most 50000 characters")
		}
	}
	value, ok := agentPatch["modelAggregations"]
	if !ok {
		return nil
	}
	aggregations, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("agent.modelAggregations must be an array")
	}
	if len(aggregations) > 50 {
		return httpx.ValidationError("agent.modelAggregations must contain at most 50 items")
	}
	for i, item := range aggregations {
		agg, ok := item.(map[string]any)
		path := "agent.modelAggregations[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := validateSettingsStringFieldBounds(agg, "id", path, 1, 20); err != nil {
			return err
		}
		if err := validateSettingsStringFieldBounds(agg, "name", path, 1, 100); err != nil {
			return err
		}
		if err := validateModelAggregationModels(agg, path); err != nil {
			return err
		}
		routingMode := stringValue(agg["routingMode"])
		if routingMode != "priority" && routingMode != "balanced" {
			return httpx.ValidationError(path + ".routingMode must be priority or balanced")
		}
	}
	return nil
}

func validateModelAggregationModels(agg map[string]any, path string) error {
	value, ok := agg["models"]
	if !ok {
		return httpx.ValidationError(path + ".models is required")
	}
	models, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(path + ".models must be an array")
	}
	if len(models) < 1 || len(models) > 20 {
		return httpx.ValidationError(path + ".models must contain between 1 and 20 items")
	}
	for i, item := range models {
		model, ok := item.(string)
		if !ok || strings.TrimSpace(model) == "" {
			return httpx.ValidationError(path + ".models[" + strconv.Itoa(i) + "] must be a non-empty string")
		}
	}
	return nil
}

func validateSettingsStringFieldBounds(item map[string]any, key string, path string, minLen int, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	if httpx.JSStringLength(text) < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if httpx.JSStringLength(text) > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func validateAgentCustomRetryRules(agentPatch map[string]any) error {
	value, ok := agentPatch["customRetryRules"]
	if !ok {
		return nil
	}
	rules, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("agent.customRetryRules must be an array")
	}
	if len(rules) > 100 {
		return httpx.ValidationError("agent.customRetryRules must contain at most 100 items")
	}
	for i, item := range rules {
		rule, ok := item.(map[string]any)
		path := "agent.customRetryRules[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if _, err := settingsRequiredStringField(rule, "id", path, 1); err != nil {
			return err
		}
		for _, key := range []string{"domain", "keyword"} {
			if err := settingsOptionalStringField(rule, key, path); err != nil {
				return err
			}
		}
		if value, ok := rule["statusCode"]; ok {
			if err := validateSettingsIntRange(value, path+".statusCode", 100, 599); err != nil {
				return err
			}
		}
		if err := settingsOptionalBoolField(rule, "enabled", path); err != nil {
			return err
		}
		if err := settingsOptionalStringMaxField(rule, "note", path, 200); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentModelSelectionSettings(agentPatch map[string]any) error {
	if err := validateAgentCustomModels(agentPatch); err != nil {
		return err
	}
	if err := settingsStringArrayField(agentPatch, "hiddenModels", "agent", -1); err != nil {
		return err
	}
	if err := settingsStringArrayField(agentPatch, "providerOrder", "agent", 50); err != nil {
		return err
	}
	if err := settingsStringArrayField(agentPatch, "disabledProviders", "agent", 200); err != nil {
		return err
	}
	if err := validateAgentModelContextWindows(agentPatch); err != nil {
		return err
	}
	if err := validateAgentSubagentModels(agentPatch); err != nil {
		return err
	}
	return validateAgentSubagentAllowedModels(agentPatch)
}

func validateAgentCustomModels(agentPatch map[string]any) error {
	value, ok := agentPatch["customModels"]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("agent.customModels must be an array")
	}
	for i, item := range items {
		model, ok := item.(map[string]any)
		path := "agent.customModels[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if _, err := settingsRequiredStringField(model, "value", path, 1); err != nil {
			return err
		}
		if _, err := settingsRequiredStringField(model, "label", path, 1); err != nil {
			return err
		}
		for _, key := range []string{"provider", "channel", "channelType"} {
			if err := settingsOptionalStringField(model, key, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAgentModelContextWindows(agentPatch map[string]any) error {
	value, ok := agentPatch["modelContextWindows"]
	if !ok {
		return nil
	}
	windows, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("agent.modelContextWindows must be an object")
	}
	for key, value := range windows {
		if key == "" {
			return httpx.ValidationError("agent.modelContextWindows keys must be non-empty")
		}
		if err := validateSettingsIntRange(value, "agent.modelContextWindows."+key, 1, int(^uint(0)>>1)); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentSubagentModels(agentPatch map[string]any) error {
	value, ok := agentPatch["subagentModels"]
	if !ok {
		return nil
	}
	models, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("agent.subagentModels must be an object")
	}
	for key, value := range models {
		switch key {
		case "explore", "plan":
			if _, ok := value.(string); !ok {
				return httpx.ValidationError("agent.subagentModels." + key + " must be a string")
			}
		default:
			return httpx.ValidationError("agent.subagentModels." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAgentSubagentAllowedModels(agentPatch map[string]any) error {
	value, ok := agentPatch["subagentAllowedModels"]
	if !ok {
		return nil
	}
	allowed, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("agent.subagentAllowedModels must be an object")
	}
	for key := range allowed {
		switch key {
		case "explore", "plan", "general":
			if err := settingsStringArrayField(allowed, key, "agent.subagentAllowedModels", -1); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("agent.subagentAllowedModels." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAgentWebFetchPolicy(agentPatch map[string]any) error {
	value, ok := agentPatch["webFetchPolicy"]
	if !ok {
		return nil
	}
	policy, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("agent.webFetchPolicy must be an object")
	}
	for key, value := range policy {
		switch key {
		case "allowAll":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("agent.webFetchPolicy.allowAll must be boolean")
			}
		case "whitelist", "blacklist":
			if err := validateAgentWebFetchPatternList(value, "agent.webFetchPolicy."+key); err != nil {
				return err
			}
		case "proxy":
			proxy, ok := value.(map[string]any)
			if !ok {
				return httpx.ValidationError("agent.webFetchPolicy.proxy must be an object")
			}
			normalizeProxyField(proxy, "url")
			if err := validateAgentWebFetchProxy(proxy); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("agent.webFetchPolicy." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAgentWebFetchPatternList(value any, path string) error {
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(path + " must be an array")
	}
	for i, item := range items {
		rule, ok := item.(map[string]any)
		itemPath := path + "[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(itemPath + " must be an object")
		}
		if _, ok := rule["pattern"].(string); !ok {
			return httpx.ValidationError(itemPath + ".pattern must be a string")
		}
		if err := settingsOptionalBoolField(rule, "enabled", itemPath); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentWebFetchProxy(proxy map[string]any) error {
	for key, value := range proxy {
		switch key {
		case "mode":
			mode := stringValue(value)
			if mode != "direct" && mode != "system" && mode != "custom" {
				return httpx.ValidationError("agent.webFetchPolicy.proxy.mode must be direct, system, or custom")
			}
		case "url":
			proxyURL, ok := value.(string)
			if !ok {
				return httpx.ValidationError("agent.webFetchPolicy.proxy.url must be a string")
			}
			if proxyURL != "" {
				parsed, err := url.Parse(proxyURL)
				if err != nil || parsed.Scheme == "" || parsed.Host == "" || !validProxyScheme(parsed.Scheme) {
					return httpx.ValidationError("agent.webFetchPolicy.proxy.url must be an http, https, socks4, socks5, or socks5h URL")
				}
			}
		default:
			return httpx.ValidationError("agent.webFetchPolicy.proxy." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAgentPolicySettings(agentPatch map[string]any) error {
	if err := validateAgentPathPolicyRules(agentPatch, "whitelistDirs", true); err != nil {
		return err
	}
	if err := validateAgentPathPolicyRules(agentPatch, "blacklistDirs", false); err != nil {
		return err
	}
	if err := validateAgentCommandPolicyRules(agentPatch, "commandWhitelist", false); err != nil {
		return err
	}
	return validateAgentCommandPolicyRules(agentPatch, "commandBlacklist", true)
}

func validateAgentPathPolicyRules(agentPatch map[string]any, key string, whitelist bool) error {
	value, ok := agentPatch[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("agent." + key + " must be an array")
	}
	if len(items) > 50 {
		return httpx.ValidationError("agent." + key + " must contain at most 50 items")
	}
	for i, item := range items {
		rule, ok := item.(map[string]any)
		path := "agent." + key + "[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := settingsRequiredTrimmedString(rule, "path", path, 1, 4096); err != nil {
			return err
		}
		if whitelist {
			if err := settingsOptionalEnumField(rule, "accessLevel", path, []string{"readOnly", "readWrite", "full"}); err != nil {
				return err
			}
		} else {
			if err := settingsOptionalEnumField(rule, "denyLevel", path, []string{"denyWrite", "denyAll"}); err != nil {
				return err
			}
		}
		if err := settingsOptionalBoolField(rule, "enabled", path); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentCommandPolicyRules(agentPatch map[string]any, key string, includeDenyPrompt bool) error {
	value, ok := agentPatch[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("agent." + key + " must be an array")
	}
	if len(items) > 50 {
		return httpx.ValidationError("agent." + key + " must contain at most 50 items")
	}
	for i, item := range items {
		rule, ok := item.(map[string]any)
		path := "agent." + key + "[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := settingsRequiredTrimmedString(rule, "pattern", path, 1, 200); err != nil {
			return err
		}
		if includeDenyPrompt {
			if err := settingsOptionalStringMaxField(rule, "denyPrompt", path, 2000); err != nil {
				return err
			}
		}
		if err := settingsOptionalBoolField(rule, "enabled", path); err != nil {
			return err
		}
	}
	return nil
}

func settingsRequiredTrimmedString(item map[string]any, key string, path string, minLen int, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	text = strings.TrimSpace(text)
	if httpx.JSStringLength(text) < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if httpx.JSStringLength(text) > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	// TS uses z.string().trim() for these fields, which stores the trimmed value.
	// Write it back so persisted settings match TS exactly.
	item[key] = text
	return nil
}

func settingsOptionalStringMaxField(item map[string]any, key string, path string, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	if httpx.JSStringLength(text) > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func validateAgentContextThresholds(thresholds map[string]any) error {
	for tier, minValue := range map[string]float64{"standard": 50, "large": 10} {
		value, ok := thresholds[tier]
		if !ok {
			continue
		}
		cfg, ok := value.(map[string]any)
		if !ok {
			return httpx.ValidationError("agent.contextThresholds." + tier + " must be an object")
		}
		for _, key := range []string{"pruneStart", "compactStart"} {
			if value, ok := cfg[key]; ok {
				// TS contextThresholds use z.number() (NOT .int()), so fractional
				// values like 95.5 are valid. Use the float range validator rather
				// than the integer one to avoid rejecting decimals the frontend's
				// NumberInput can produce.
				if err := validateSettingsFloatRange(value, "agent.contextThresholds."+tier+"."+key, minValue, 100); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateChaptersSettingsPatch(patch map[string]any) error {
	rawChapters, exists := patch["chapters"]
	if !exists {
		return nil
	}
	chaptersPatch, ok := rawChapters.(map[string]any)
	if !ok {
		return httpx.ValidationError("chapters must be an object")
	}
	for key, value := range chaptersPatch {
		switch key {
		case "maxActiveWorktrees":
			if err := validateSettingsIntRange(value, "chapters.maxActiveWorktrees", 1, int(^uint(0)>>1)); err != nil {
				return err
			}
		case "maxActiveContainers", "worktreeSizeWarningMb", "dormantAfterMinutes":
			if err := validateSettingsIntRange(value, "chapters."+key, 0, int(^uint(0)>>1)); err != nil {
				return err
			}
		case "autoSaveOnDormant":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("chapters.autoSaveOnDormant must be boolean")
			}
		default:
			return httpx.ValidationError("chapters." + key + " cannot be updated")
		}
	}
	return nil
}

func validateEditorSettingsPatch(patch map[string]any) error {
	rawEditor, exists := patch["editor"]
	if !exists {
		return nil
	}
	editorPatch, ok := rawEditor.(map[string]any)
	if !ok {
		return httpx.ValidationError("editor must be an object")
	}
	for key, value := range editorPatch {
		switch key {
		case "type":
			editorType := stringValue(value)
			if editorType != "vscode" && editorType != "cursor" && editorType != "windsurf" && editorType != "zed" {
				return httpx.ValidationError("editor.type must be one of vscode, cursor, windsurf, or zed")
			}
		default:
			return httpx.ValidationError("editor." + key + " cannot be updated")
		}
	}
	return nil
}

func validateCodexSettingsPatch(patch map[string]any) error {
	rawCodex, exists := patch["codex"]
	if !exists {
		return nil
	}
	codexPatch, ok := rawCodex.(map[string]any)
	if !ok {
		return httpx.ValidationError("codex must be an object")
	}
	for key, value := range codexPatch {
		switch key {
		case "proxy":
			proxy, ok := value.(string)
			if !ok {
				return httpx.ValidationError("codex.proxy must be a string")
			}
			// Mirror TS proxyUrlSchema (z.preprocess(normalizeProxyUrl,
			// z.string().optional())): normalize only, NO scheme validation. TS
			// rejects null/non-string (z.string()) but accepts any normalized
			// string — including non-http schemes like "ftp://". Go previously
			// rejected non-http/socks schemes here, which (a) diverged from TS and
			// (b) was inconsistent with Go's own customApi/anthropic proxy handling
			// (normalizeProxyField, no scheme check) that uses the same TS schema.
			codexPatch["proxy"] = normalizeProxyURL(proxy)
		case "loadBalancingMode":
			mode := stringValue(value)
			if mode != "priority" && mode != "balanced" && mode != "tier-balanced" {
				return httpx.ValidationError("codex.loadBalancingMode must be priority, balanced, or tier-balanced")
			}
		case "tierOrder":
			items, ok := value.([]any)
			if !ok {
				return httpx.ValidationError("codex.tierOrder must be an array")
			}
			if len(items) > 6 {
				return httpx.ValidationError("codex.tierOrder must contain at most 6 items")
			}
			for i, item := range items {
				tier, ok := item.(string)
				if !ok || !isCodexTier(tier) {
					return httpx.ValidationError("codex.tierOrder[" + strconv.Itoa(i) + "] is not supported")
				}
			}
		case "defaultReasoningEffort":
			if value == nil {
				continue
			}
			effort := stringValue(value)
			if effort != "none" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" {
				return httpx.ValidationError("codex.defaultReasoningEffort is not supported")
			}
		case "useWebSearch", "useImageGeneration":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("codex." + key + " must be a boolean")
			}
		default:
			return httpx.ValidationError("codex." + key + " cannot be updated")
		}
	}
	return nil
}

func validateRoutinesSettingsPatch(patch map[string]any) error {
	rawRoutines, exists := patch["routines"]
	if !exists {
		return nil
	}
	routinesPatch, ok := rawRoutines.(map[string]any)
	if !ok {
		return httpx.ValidationError("routines must be an object")
	}
	for key := range routinesPatch {
		switch key {
		case "disabledRoutines", "enabledRoutines":
			if err := settingsOptionalStringArrayField(routinesPatch, key, "routines"); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("routines." + key + " cannot be updated")
		}
	}
	return nil
}

func validProxyScheme(scheme string) bool {
	return scheme == "http" || scheme == "https" || scheme == "socks4" || scheme == "socks5" || scheme == "socks5h"
}

func normalizeProxyURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if hasProxyProtocolPrefix(trimmed) {
		return trimmed
	}
	return "http://" + trimmed
}

// hasProxyProtocolPrefix mirrors the TypeScript PROXY_PROTOCOL_RE
// (/^[a-z][a-z0-9+.-]*:\/\//i): a scheme token at the START — a letter followed
// by letters/digits/`+`/`.`/`-` — immediately followed by "://". A plain
// strings.Contains("://") check diverges, treating e.g. "host://weird" or
// "1abc://x" as already-schemed when TS would prefix "http://".
func hasProxyProtocolPrefix(value string) bool {
	i := 0
	n := len(value)
	if i >= n || !isASCIILetter(value[i]) {
		return false
	}
	i++
	for i < n {
		c := value[i]
		if isASCIILetter(c) || (c >= '0' && c <= '9') || c == '+' || c == '.' || c == '-' {
			i++
			continue
		}
		break
	}
	return strings.HasPrefix(value[i:], "://")
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func normalizeProxyField(item map[string]any, key string) {
	if value, ok := item[key].(string); ok {
		item[key] = normalizeProxyURL(value)
	}
}

func isCodexTier(tier string) bool {
	switch tier {
	case "free", "plus", "team", "prolite", "pro", "other":
		return true
	default:
		return false
	}
}

func validateContainersSettingsPatch(patch map[string]any) error {
	rawContainers, exists := patch["containers"]
	if !exists {
		return nil
	}
	containersPatch, ok := rawContainers.(map[string]any)
	if !ok {
		return httpx.ValidationError("containers must be an object")
	}
	for key, value := range containersPatch {
		switch key {
		case "portRangeStart", "portRangeEnd":
			port, ok := patchIntValue(value)
			if !ok || port < 1 || port > 65535 {
				return httpx.ValidationError("containers." + key + " must be an integer between 1 and 65535")
			}
		case "proxy":
			proxyPatch, ok := value.(map[string]any)
			if !ok {
				return httpx.ValidationError("containers.proxy must be an object")
			}
			for proxyKey, proxyValue := range proxyPatch {
				switch proxyKey {
				case "enabled":
					if _, ok := proxyValue.(bool); !ok {
						return httpx.ValidationError("containers.proxy.enabled must be boolean")
					}
				case "port":
					port, ok := patchIntValue(proxyValue)
					if !ok || port < 1 || port > 65535 {
						return httpx.ValidationError("containers.proxy.port must be an integer between 1 and 65535")
					}
				default:
					return httpx.ValidationError("containers.proxy." + proxyKey + " cannot be updated")
				}
			}
		default:
			return httpx.ValidationError("containers." + key + " cannot be updated")
		}
	}
	return nil
}

func validateAuthSettingsPatch(patch map[string]any) error {
	rawAuth, exists := patch["auth"]
	if !exists {
		return nil
	}
	authPatch, ok := rawAuth.(map[string]any)
	if !ok {
		return httpx.ValidationError("auth must be an object")
	}
	for key, value := range authPatch {
		switch key {
		case "jwtSecret":
			return httpx.ValidationError("auth.jwtSecret cannot be updated")
		case "registrationOpen":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("auth.registrationOpen must be boolean")
			}
		default:
			return httpx.ValidationError("auth." + key + " cannot be updated")
		}
	}
	return nil
}

func validateUpdateSettingsPatch(patch map[string]any) error {
	rawUpdate, exists := patch["update"]
	if !exists {
		return nil
	}
	updatePatch, ok := rawUpdate.(map[string]any)
	if !ok {
		return httpx.ValidationError("update must be an object")
	}
	for key, value := range updatePatch {
		switch key {
		case "serverUrl":
			serverURL := stringValue(value)
			parsed, err := url.Parse(serverURL)
			if serverURL == "" || err != nil || parsed.Scheme == "" || parsed.Host == "" {
				return httpx.ValidationError("update.serverUrl must be a valid URL")
			}
		case "product":
			if stringValue(value) == "" {
				return httpx.ValidationError("update.product must be a non-empty string")
			}
		case "channel":
			channel := stringValue(value)
			if channel != "stable" && channel != "beta" {
				return httpx.ValidationError("update.channel must be stable or beta")
			}
		case "checkIntervalMinutes":
			if err := validateSettingsIntRange(value, "update.checkIntervalMinutes", 0, int(^uint(0)>>1)); err != nil {
				return err
			}
		case "autoDownload":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("update.autoDownload must be boolean")
			}
		default:
			return httpx.ValidationError("update." + key + " cannot be updated")
		}
	}
	return nil
}

func validateSharesSettingsPatch(patch map[string]any) error {
	rawShares, exists := patch["shares"]
	if !exists {
		return nil
	}
	sharesPatch, ok := rawShares.(map[string]any)
	if !ok {
		return httpx.ValidationError("shares must be an object")
	}
	for key, value := range sharesPatch {
		switch key {
		case "defaultExpiryHours", "maxFileSizeMb":
			if err := validateSettingsIntRange(value, "shares."+key, 1, int(^uint(0)>>1)); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("shares." + key + " cannot be updated")
		}
	}
	return nil
}

func validateVNetSettingsPatch(patch map[string]any) error {
	rawVNet, exists := patch["vnet"]
	if !exists {
		return nil
	}
	vnetPatch, ok := rawVNet.(map[string]any)
	if !ok {
		return httpx.ValidationError("vnet must be an object")
	}
	for key, value := range vnetPatch {
		switch key {
		case "enabled", "allowAnonymousRelay":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("vnet." + key + " must be boolean")
			}
		case "relayToken":
			if _, ok := value.(string); !ok {
				return httpx.ValidationError("vnet.relayToken must be a string")
			}
		case "maxPeersPerNetwork":
			if err := validateSettingsIntRange(value, "vnet.maxPeersPerNetwork", 1, 1024); err != nil {
				return err
			}
		case "maxMessageBytes":
			if err := validateSettingsIntRange(value, "vnet.maxMessageBytes", 1024, 64*1024*1024); err != nil {
				return err
			}
		case "udp":
			udpPatch, ok := value.(map[string]any)
			if !ok {
				return httpx.ValidationError("vnet.udp must be an object")
			}
			if err := validateVNetUDPSettingsPatch(udpPatch); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("vnet." + key + " cannot be updated")
		}
	}
	return nil
}

func validateVNetUDPSettingsPatch(udpPatch map[string]any) error {
	for key, value := range udpPatch {
		switch key {
		case "enabled":
			if _, ok := value.(bool); !ok {
				return httpx.ValidationError("vnet.udp.enabled must be boolean")
			}
		case "host":
			host := strings.TrimSpace(stringValue(value))
			if host == "" || httpx.JSStringLength(host) > 255 {
				return httpx.ValidationError("vnet.udp.host must be a non-empty string up to 255 characters")
			}
		case "port":
			if err := validateSettingsIntRange(value, "vnet.udp.port", 0, 65535); err != nil {
				return err
			}
		default:
			return httpx.ValidationError("vnet.udp." + key + " cannot be updated")
		}
	}
	return nil
}

func validateSettingsIntRange(value any, path string, minValue int, maxValue int) error {
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) || number < float64(minValue) || number > float64(maxValue) {
		return httpx.ValidationError(path + " must be an integer between " + strconv.Itoa(minValue) + " and " + strconv.Itoa(maxValue))
	}
	return nil
}

// validateSettingsFloatRange mirrors a TS z.number().min(a).max(b) WITHOUT
// .int(): fractional values are allowed, so it must NOT reject non-integers the
// way validateSettingsIntRange does. Used for contextThresholds pruneStart/
// compactStart, which TS defines as z.number() (not z.number().int()).
func validateSettingsFloatRange(value any, path string, minValue float64, maxValue float64) error {
	number, ok := value.(float64)
	if !ok || number < minValue || number > maxValue {
		return httpx.ValidationError(path + " must be a number between " + strconv.FormatFloat(minValue, 'f', -1, 64) + " and " + strconv.FormatFloat(maxValue, 'f', -1, 64))
	}
	return nil
}

type providerPatchSchema struct {
	requiredStrings map[string]int
	optionalStrings []string
	optionalBools   []string
	optionalEnums   map[string][]string
	nullableEnums   map[string][]string
	optionalIntsMin map[string]int
	optionalArrays  []string
}

var providerPatchSchemas = map[string]providerPatchSchema{
	"customApiProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "apiKey": 0, "baseUrl": 0, "defaultModel": 0, "protocol": 1},
		optionalStrings: []string{"proxy", "codexAccountId"},
		optionalBools:   []string{"tlsRejectUnauthorized", "codexWebSocket", "codexWebSearch", "codexImageGeneration", "disabled"},

		optionalEnums:   map[string][]string{"protocol": {"anthropic-official", "anthropic-compatible", "codex-native", "responses-compatible", "completions-compatible"}},
		nullableEnums:   map[string][]string{"defaultReasoningEffort": {"none", "low", "medium", "high", "max"}},
		optionalIntsMin: map[string]int{"defaultContextWindow": 1},
	},
	"openaiProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "apiKey": 0, "baseUrl": 0, "defaultModel": 0},
		optionalStrings: []string{"codexAccountId"},
		optionalBools:   []string{"responsesApi", "codexWebSocket", "codexWebSearch", "codexImageGeneration", "disabled"},
		optionalEnums:   map[string][]string{"apiMode": {"responses", "completions", "codex"}},
		optionalIntsMin: map[string]int{"defaultContextWindow": 1},
	},
	"anthropicProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "apiKey": 0, "baseUrl": 0, "defaultModel": 0},
		optionalStrings: []string{"proxy"},
		optionalBools:   []string{"tlsRejectUnauthorized", "officialApi", "disabled"},
		nullableEnums:   map[string][]string{"defaultReasoningEffort": {"none", "low", "medium", "high", "max"}},
	},
	"nkpProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "apiKey": 0, "baseUrl": 0, "defaultModel": 0},
		optionalBools:   []string{"disabled"},
	},
	"nugProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "apiKey": 0, "baseUrl": 0, "defaultModel": 0},
		optionalStrings: []string{"nugUsername", "nugUserId", "oauthClientId", "oauthClientSecret", "oauthDeviceId", "oauthCallbackUrl"},
		optionalBools:   []string{"disabled"},
	},
	"clineProviders": {
		requiredStrings: map[string]int{"id": 1, "name": 0, "prefix": 1, "baseUrl": 0, "defaultModel": 0},
		optionalStrings: []string{"accessToken"},
		optionalBools:   []string{"disabled"},
		optionalIntsMin: map[string]int{"defaultContextWindow": 1},
		optionalArrays:  []string{"enabledModels"},
	},
}

func validateProviderSettingsPatch(patch map[string]any) error {
	for field, schema := range providerPatchSchemas {
		value, ok := patch[field]
		if !ok {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return httpx.ValidationError(field + " must be an array")
		}
		for i, item := range items {
			provider, ok := item.(map[string]any)
			path := field + "[" + strconv.Itoa(i) + "]"
			if !ok {
				return httpx.ValidationError(path + " must be an object")
			}
			if err := validateProviderPatchItem(provider, path, schema); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProviderPatchItem(provider map[string]any, path string, schema providerPatchSchema) error {
	for key, minLen := range schema.requiredStrings {
		if _, err := settingsRequiredStringField(provider, key, path, minLen); err != nil {
			return err
		}
	}
	for _, key := range schema.optionalStrings {
		if err := settingsOptionalStringField(provider, key, path); err != nil {
			return err
		}
		if key == "proxy" {
			normalizeProxyField(provider, key)
		}
	}
	for _, key := range schema.optionalBools {
		if err := settingsOptionalBoolField(provider, key, path); err != nil {
			return err
		}
	}
	for key, allowed := range schema.optionalEnums {
		if err := settingsOptionalEnumField(provider, key, path, allowed); err != nil {
			return err
		}
	}
	for key, allowed := range schema.nullableEnums {
		if err := settingsOptionalNullableEnumField(provider, key, path, allowed); err != nil {
			return err
		}
	}
	for key, minValue := range schema.optionalIntsMin {
		if err := settingsOptionalIntMinField(provider, key, path, minValue); err != nil {
			return err
		}
	}
	for _, key := range schema.optionalArrays {
		if err := settingsOptionalStringArrayField(provider, key, path); err != nil {
			return err
		}
	}
	return nil
}

func settingsRequiredStringField(item map[string]any, key string, path string, minLen int) (string, error) {
	value, ok := item[key]
	if !ok {
		return "", httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return "", httpx.ValidationError(path + "." + key + " must be a string")
	}
	if len(text) < minLen {
		return "", httpx.ValidationError(path + "." + key + " must not be empty")
	}
	return text, nil
}

func settingsOptionalStringField(item map[string]any, key string, path string) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	if _, ok := value.(string); !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	return nil
}

func settingsOptionalBoolField(item map[string]any, key string, path string) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return httpx.ValidationError(path + "." + key + " must be boolean")
	}
	return nil
}

func settingsOptionalEnumField(item map[string]any, key string, path string, allowed []string) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	return settingsEnumValue(value, key, path, allowed)
}

func settingsOptionalNullableEnumField(item map[string]any, key string, path string, allowed []string) error {
	value, ok := item[key]
	if !ok || value == nil {
		return nil
	}
	return settingsEnumValue(value, key, path, allowed)
}

func settingsEnumValue(value any, key string, path string, allowed []string) error {
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	for _, allowedValue := range allowed {
		if text == allowedValue {
			return nil
		}
	}
	return httpx.ValidationError(path + "." + key + " is not supported")
}

func settingsOptionalIntMinField(item map[string]any, key string, path string, minValue int) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) || number < float64(minValue) {
		return httpx.ValidationError(path + "." + key + " must be an integer >= " + strconv.Itoa(minValue))
	}
	item[key] = int(number)
	return nil
}

func settingsOptionalStringArrayField(item map[string]any, key string, path string) error {
	return settingsStringArrayField(item, key, path, -1)
}

func settingsStringArrayField(item map[string]any, key string, path string, maxItems int) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be an array")
	}
	if maxItems >= 0 && len(items) > maxItems {
		return httpx.ValidationError(path + "." + key + " must contain at most " + strconv.Itoa(maxItems) + " items")
	}
	for i, item := range items {
		if _, ok := item.(string); !ok {
			return httpx.ValidationError(path + "." + key + "[" + strconv.Itoa(i) + "] must be a string")
		}
	}
	return nil
}

func (h *Handler) applyProviderRemovalSideEffects(previous, next map[string]any) (map[string]any, error) {
	removedByField := map[string][]string{
		"customApiProviders": removedProviderIDs(previous, next, "customApiProviders"),
		"openaiProviders":    removedProviderIDs(previous, next, "openaiProviders"),
		"anthropicProviders": removedProviderIDs(previous, next, "anthropicProviders"),
		"nkpProviders":       removedProviderIDs(previous, next, "nkpProviders"),
		"nugProviders":       removedProviderIDs(previous, next, "nugProviders"),
		"clineProviders":     removedProviderIDs(previous, next, "clineProviders"),
	}
	customOpenAI := uniqueStrings(append(append([]string{}, removedByField["customApiProviders"]...), customApiProvidersLeavingFamily(previous, next, "openai")...))
	customAnthropic := uniqueStrings(append(append([]string{}, removedByField["customApiProviders"]...), customApiProvidersLeavingFamily(previous, next, "anthropic")...))
	_ = purgeProviderModelCache("openai", uniqueStrings(append(customOpenAI, removedByField["openaiProviders"]...)))
	_ = purgeProviderModelCache("anthropic", uniqueStrings(append(customAnthropic, removedByField["anthropicProviders"]...)))
	_ = purgeProviderModelCache("nkp", removedByField["nkpProviders"])
	_ = purgeProviderModelCache("nug", removedByField["nugProviders"])
	_ = purgeProviderModelCache("cline", removedByField["clineProviders"])

	removedPrefixes := map[string]bool{}
	for field, ids := range removedByField {
		if len(ids) == 0 {
			continue
		}
		idSet := map[string]bool{}
		for _, idValue := range ids {
			idSet[idValue] = true
		}
		for _, item := range anySlice(previous[field]) {
			provider, ok := item.(map[string]any)
			if !ok || !idSet[stringValue(provider["id"])] {
				continue
			}
			if prefix := strings.TrimSpace(stringValue(provider["prefix"])); prefix != "" {
				removedPrefixes[prefix] = true
			}
		}
	}
	if len(removedPrefixes) == 0 {
		return next, nil
	}
	agent, ok := next["agent"].(map[string]any)
	if !ok {
		return next, nil
	}
	cleanedAgent, changed := purgeStaleAgentModelRefs(agent, removedPrefixes)
	if !changed || h.Store == nil {
		return next, nil
	}
	next["agent"] = cleanedAgent
	return h.Store.Replace(next)
}

func removedProviderIDs(previous, next map[string]any, field string) []string {
	nextIDs := map[string]bool{}
	for _, item := range anySlice(next[field]) {
		provider, ok := item.(map[string]any)
		if ok {
			nextIDs[stringValue(provider["id"])] = true
		}
	}
	removed := []string{}
	for _, item := range anySlice(previous[field]) {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		providerID := stringValue(provider["id"])
		if providerID != "" && !nextIDs[providerID] {
			removed = append(removed, providerID)
		}
	}
	return removed
}

func customApiProvidersLeavingFamily(previous, next map[string]any, family string) []string {
	nextByID := map[string]map[string]any{}
	for _, item := range anySlice(next["customApiProviders"]) {
		provider, ok := item.(map[string]any)
		if ok {
			nextByID[stringValue(provider["id"])] = provider
		}
	}
	ids := []string{}
	for _, item := range anySlice(previous["customApiProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		nextProvider := nextByID[stringValue(provider["id"])]
		if nextProvider == nil {
			continue
		}
		if providerProtocolInFamily(provider, family) && !providerProtocolInFamily(nextProvider, family) {
			ids = append(ids, stringValue(provider["id"]))
		}
	}
	return ids
}

func providerProtocolInFamily(provider map[string]any, family string) bool {
	protocol := strings.ToLower(strings.TrimSpace(stringValue(provider["protocol"])))
	if protocol == "" {
		protocol = strings.ToLower(strings.TrimSpace(stringValue(provider["apiMode"])))
	}
	if family == "openai" {
		return protocol == "openai" || strings.Contains(protocol, "openai") || protocol == "responses"
	}
	return protocol == "anthropic" || strings.Contains(protocol, "anthropic") || protocol == "claude"
}

func purgeProviderModelCache(kind string, ids []string) error {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil
	}
	cache := loadProviderModelCache(kind)
	changed := false
	for _, idValue := range ids {
		if _, ok := cache[idValue]; ok {
			delete(cache, idValue)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	fileName := kind + "-models-providers.json"
	if kind == "cline" {
		fileName = "cline-models.json"
	}
	path := filepath.Join(dataDir(), fileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func purgeStaleAgentModelRefs(agent map[string]any, stalePrefixes map[string]bool) (map[string]any, bool) {
	cleaned := cloneMap(agent)
	changed := false
	isStale := func(value string) bool {
		prefix, _, ok := strings.Cut(value, ":")
		return ok && prefix != "" && stalePrefixes[prefix]
	}
	if isStale(stringValue(cleaned["summaryModel"])) {
		cleaned["summaryModel"] = ""
		changed = true
	}
	if subagent, ok := cleaned["subagentModels"].(map[string]any); ok {
		if _, ok := subagent["general"]; ok {
			delete(subagent, "general")
			changed = true
		}
		for _, key := range []string{"explore", "plan"} {
			if isStale(stringValue(subagent[key])) {
				subagent[key] = ""
				changed = true
			}
		}
	}
	if hidden, ok := cleaned["hiddenModels"].([]any); ok {
		kept := make([]any, 0, len(hidden))
		for _, item := range hidden {
			if isStale(stringValue(item)) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		cleaned["hiddenModels"] = kept
	}
	if windows, ok := cleaned["modelContextWindows"].(map[string]any); ok {
		kept := map[string]any{}
		for key, value := range windows {
			if isStale(key) {
				changed = true
				continue
			}
			kept[key] = value
		}
		cleaned["modelContextWindows"] = kept
	}
	if custom, ok := cleaned["customModels"].([]any); ok {
		kept := make([]any, 0, len(custom))
		for _, item := range custom {
			model, ok := item.(map[string]any)
			if ok && isStale(stringValue(model["value"])) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		cleaned["customModels"] = kept
	}
	return cleaned, changed
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch typed := v.(type) {
		case map[string]any:
			out[k] = cloneMap(typed)
		case []any:
			items := make([]any, len(typed))
			for i, item := range typed {
				if child, ok := item.(map[string]any); ok {
					items[i] = cloneMap(child)
				} else {
					items[i] = item
				}
			}
			out[k] = items
		default:
			out[k] = v
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func preserveMaskedSecrets(current, patch map[string]any) error {
	if server, ok := patch["server"].(map[string]any); ok {
		if tls, ok := server["tls"].(map[string]any); ok {
			oldTLS, _ := nestedMap(current, "server", "tls")
			passphrase := stringValue(tls["passphrase"])
			if passphrase == "" || strings.HasPrefix(passphrase, "*") {
				if old := stringValue(oldTLS["passphrase"]); old != "" {
					tls["passphrase"] = old
				} else {
					delete(tls, "passphrase")
				}
			}
		}
	}
	if vnet, ok := patch["vnet"].(map[string]any); ok {
		if token := stringValue(vnet["relayToken"]); strings.HasPrefix(token, "*") {
			oldVNet, _ := current["vnet"].(map[string]any)
			vnet["relayToken"] = stringValue(oldVNet["relayToken"])
		}
	}
	preserveProviderSecret(current, patch, "customApiProviders", "apiKey")
	preserveProviderSecret(current, patch, "openaiProviders", "apiKey")
	preserveProviderSecret(current, patch, "anthropicProviders", "apiKey")
	preserveProviderSecret(current, patch, "nkpProviders", "apiKey")
	preserveProviderSecret(current, patch, "nugProviders", "apiKey")
	preserveProviderSecret(current, patch, "nugProviders", "oauthClientSecret")
	preserveProviderSecret(current, patch, "clineProviders", "accessToken")
	return nil
}

func preserveProviderSecret(current, patch map[string]any, field, secretKey string) {
	incoming, ok := patch[field].([]any)
	if !ok {
		return
	}
	oldByID := map[string]map[string]any{}
	for _, item := range anySlice(current[field]) {
		provider, ok := item.(map[string]any)
		if ok {
			oldByID[stringValue(provider["id"])] = provider
		}
	}
	for _, item := range incoming {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		secret := stringValue(provider[secretKey])
		if secret == "" || !strings.HasPrefix(secret, "*") {
			continue
		}
		if old := oldByID[stringValue(provider["id"])]; old != nil {
			provider[secretKey] = stringValue(old[secretKey])
		} else {
			provider[secretKey] = ""
		}
	}
}

func syncLegacyProvidersFromCustomPatch(patch map[string]any) {
	providers, ok := patch["customApiProviders"].([]any)
	if !ok {
		return
	}
	patch["openaiProviders"] = customApiProvidersToOpenAI(providers)
	patch["anthropicProviders"] = customApiProvidersToAnthropic(providers)
}

func syncCustomProvidersFromLegacyPatch(current, patch map[string]any) {
	if _, hasCustomProviders := patch["customApiProviders"]; hasCustomProviders {
		return
	}
	_, hasOpenAIProviders := patch["openaiProviders"]
	_, hasAnthropicProviders := patch["anthropicProviders"]
	if !hasOpenAIProviders && !hasAnthropicProviders {
		return
	}
	openaiProviders := anySlice(current["openaiProviders"])
	if incoming, ok := patch["openaiProviders"].([]any); ok {
		openaiProviders = incoming
	}
	anthropicProviders := anySlice(current["anthropicProviders"])
	if incoming, ok := patch["anthropicProviders"].([]any); ok {
		anthropicProviders = incoming
	}
	patch["customApiProviders"] = deriveCustomApiProvidersFromLegacy(openaiProviders, anthropicProviders)
}

func deriveCustomApiProvidersFromLegacy(openaiProviders, anthropicProviders []any) []any {
	byID := map[string]map[string]any{}
	order := []string{}
	for _, item := range openaiProviders {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		converted := openAIProviderToCustomApi(provider)
		providerID := stringValue(converted["id"])
		if _, exists := byID[providerID]; !exists {
			order = append(order, providerID)
		}
		byID[providerID] = converted
	}
	for _, item := range anthropicProviders {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		converted := anthropicProviderToCustomApi(provider)
		providerID := stringValue(converted["id"])
		if existing, exists := byID[providerID]; exists {
			merged := cloneMap(existing)
			for key, value := range converted {
				merged[key] = value
			}
			if value, ok := existing["codexAccountId"]; ok {
				merged["codexAccountId"] = value
			}
			if value, ok := existing["codexWebSocket"]; ok {
				merged["codexWebSocket"] = value
			}
			if value, ok := existing["codexWebSearch"]; ok {
				merged["codexWebSearch"] = value
			}
			if value, ok := existing["codexImageGeneration"]; ok {
				merged["codexImageGeneration"] = value
			}
			byID[providerID] = merged
			continue
		}
		order = append(order, providerID)
		byID[providerID] = converted
	}
	out := make([]any, 0, len(order))
	for _, providerID := range order {
		out = append(out, byID[providerID])
	}
	return out
}

func openAIProviderToCustomApi(provider map[string]any) map[string]any {
	converted := map[string]any{
		"id":           stringValue(provider["id"]),
		"name":         stringValue(provider["name"]),
		"prefix":       stringValue(provider["prefix"]),
		"apiKey":       stringValue(provider["apiKey"]),
		"baseUrl":      stringValue(provider["baseUrl"]),
		"defaultModel": stringValue(provider["defaultModel"]),
		"protocol":     customApiProtocolFromOpenAI(provider),
	}
	copyProviderFieldIfPresent(converted, provider, "disabled")
	copyProviderFieldIfPresent(converted, provider, "defaultContextWindow")
	copyProviderFieldIfPresent(converted, provider, "codexAccountId")
	copyProviderFieldIfPresent(converted, provider, "codexWebSocket")
	copyProviderFieldIfPresent(converted, provider, "codexWebSearch")
	copyProviderFieldIfPresent(converted, provider, "codexImageGeneration")
	return converted
}

func anthropicProviderToCustomApi(provider map[string]any) map[string]any {
	converted := map[string]any{
		"id":           stringValue(provider["id"]),
		"name":         stringValue(provider["name"]),
		"prefix":       stringValue(provider["prefix"]),
		"apiKey":       stringValue(provider["apiKey"]),
		"baseUrl":      stringValue(provider["baseUrl"]),
		"defaultModel": stringValue(provider["defaultModel"]),
		"protocol":     customApiProtocolFromAnthropic(boolValue(provider["officialApi"])),
	}
	copyProviderFieldIfPresent(converted, provider, "disabled")
	copyProviderFieldIfPresent(converted, provider, "defaultContextWindow")
	copyProviderFieldIfPresent(converted, provider, "defaultReasoningEffort")
	copyProviderFieldIfPresent(converted, provider, "proxy")
	copyProviderFieldIfPresent(converted, provider, "tlsRejectUnauthorized")
	return converted
}

func customApiProtocolFromOpenAI(provider map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(stringValue(provider["apiMode"]))) {
	case "codex":
		return "codex-native"
	case "completions":
		return "completions-compatible"
	case "responses":
		return "responses-compatible"
	default:
		if responsesAPI, ok := provider["responsesApi"].(bool); ok && !responsesAPI {
			return "completions-compatible"
		}
		return "responses-compatible"
	}
}

func customApiProtocolFromAnthropic(officialAPI bool) string {
	if officialAPI {
		return "anthropic-official"
	}
	return "anthropic-compatible"
}

func customApiProvidersToOpenAI(providers []any) []any {
	out := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		apiMode := customApiProtocolToOpenAIApiMode(stringValue(provider["protocol"]))
		if apiMode == "" {
			continue
		}
		converted := map[string]any{
			"id":           stringValue(provider["id"]),
			"name":         stringValue(provider["name"]),
			"prefix":       stringValue(provider["prefix"]),
			"apiKey":       stringValue(provider["apiKey"]),
			"baseUrl":      stringValue(provider["baseUrl"]),
			"defaultModel": stringValue(provider["defaultModel"]),
			"apiMode":      apiMode,
		}
		copyProviderFieldIfPresent(converted, provider, "disabled")
		copyProviderFieldIfPresent(converted, provider, "defaultContextWindow")
		copyProviderFieldIfPresent(converted, provider, "codexAccountId")
		copyProviderFieldIfPresent(converted, provider, "codexWebSocket")
		copyProviderFieldIfPresent(converted, provider, "codexWebSearch")
		copyProviderFieldIfPresent(converted, provider, "codexImageGeneration")
		out = append(out, converted)
	}
	return out
}

func customApiProvidersToAnthropic(providers []any) []any {
	out := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(stringValue(provider["protocol"])))
		if protocol != "anthropic-official" && protocol != "anthropic-compatible" {
			continue
		}
		converted := map[string]any{
			"id":           stringValue(provider["id"]),
			"name":         stringValue(provider["name"]),
			"prefix":       stringValue(provider["prefix"]),
			"apiKey":       stringValue(provider["apiKey"]),
			"baseUrl":      stringValue(provider["baseUrl"]),
			"defaultModel": stringValue(provider["defaultModel"]),
			"officialApi":  protocol == "anthropic-official",
		}
		copyProviderFieldIfPresent(converted, provider, "disabled")
		copyProviderFieldIfPresent(converted, provider, "defaultContextWindow")
		copyProviderFieldIfPresent(converted, provider, "proxy")
		copyProviderFieldIfPresent(converted, provider, "tlsRejectUnauthorized")
		if value, ok := provider["defaultReasoningEffort"]; ok && value != nil {
			converted["defaultReasoningEffort"] = value
		}
		out = append(out, converted)
	}
	return out
}

func customApiProtocolToOpenAIApiMode(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "codex-native":
		return "codex"
	case "responses-compatible":
		return "responses"
	case "completions-compatible":
		return "completions"
	default:
		return ""
	}
}

func copyProviderFieldIfPresent(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func validateProviderPrefixes(current, patch map[string]any) error {
	type providerRef struct{ prefix, source string }
	refs := []providerRef{}
	appendProviderRefs := func(field string, providers []any) {
		for _, item := range providers {
			provider, ok := item.(map[string]any)
			if !ok {
				continue
			}
			prefix := strings.TrimSpace(stringValue(provider["prefix"]))
			if prefix == "" {
				continue
			}
			refs = append(refs, providerRef{prefix: prefix, source: firstString(stringValue(provider["name"]), stringValue(provider["id"]), field)})
		}
	}
	if customProviders, ok := patch["customApiProviders"].([]any); ok {
		appendProviderRefs("customApiProviders", customProviders)
	} else {
		for _, field := range []string{"openaiProviders", "anthropicProviders"} {
			providers := anySlice(current[field])
			if incoming, ok := patch[field].([]any); ok {
				providers = incoming
			}
			appendProviderRefs(field, providers)
		}
	}
	for _, field := range []string{"nkpProviders", "nugProviders", "clineProviders"} {
		providers := anySlice(current[field])
		if incoming, ok := patch[field].([]any); ok {
			providers = incoming
		}
		appendProviderRefs(field, providers)
	}
	seen := map[string]string{}
	for _, ref := range refs {
		if ref.prefix == "kiro" || ref.prefix == "codex" {
			return httpx.ValidationError("Provider prefix \"" + ref.prefix + "\" is reserved")
		}
		if existing := seen[ref.prefix]; existing != "" {
			return httpx.ValidationError("Duplicate provider prefix \"" + ref.prefix + "\" found in " + existing + " and " + ref.source)
		}
		seen[ref.prefix] = ref.source
	}
	return nil
}

func validateTLSFiles(patch map[string]any) error {
	server, _ := patch["server"].(map[string]any)
	tls, _ := server["tls"].(map[string]any)
	if !boolValue(tls["enabled"]) {
		return nil
	}
	certFile := stringValue(tls["certFile"])
	keyFile := stringValue(tls["keyFile"])
	if certFile == "" || !fileExists(certFile) {
		return httpx.ValidationError("TLS certificate file not found: " + firstString(certFile, "(empty)"))
	}
	if keyFile == "" || !fileExists(keyFile) {
		return httpx.ValidationError("TLS private key file not found: " + firstString(keyFile, "(empty)"))
	}
	if caFile := stringValue(tls["caFile"]); caFile != "" && !fileExists(caFile) {
		return httpx.ValidationError("TLS CA certificate file not found: " + caFile)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func nestedMap(raw map[string]any, keys ...string) (map[string]any, bool) {
	var current any = raw
	for _, key := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return map[string]any{}, false
		}
		current = m[key]
	}
	m, ok := current.(map[string]any)
	if !ok {
		return map[string]any{}, false
	}
	return m, true
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func serverAddressState(raw map[string]any) (string, int, bool) {
	server, _ := raw["server"].(map[string]any)
	host := firstString(strings.TrimSpace(stringValue(server["host"])), "localhost")
	port := intSetting(server["port"], 7778)
	tls, _ := server["tls"].(map[string]any)
	return host, port, boolValue(tls["enabled"])
}

func containerProxyState(raw map[string]any) (bool, int) {
	containers, _ := raw["containers"].(map[string]any)
	proxy, _ := containers["proxy"].(map[string]any)
	return boolValue(proxy["enabled"]), intSetting(proxy["port"], 7780)
}

func mergeSettingsPatch(dst, patch map[string]any) {
	for key, value := range patch {
		if child, ok := value.(map[string]any); ok {
			if existing, ok := dst[key].(map[string]any); ok {
				mergeSettingsPatch(existing, child)
				continue
			}
			dst[key] = cloneMap(child)
			continue
		}
		dst[key] = value
	}
}

func serverURL(host string, port int, tlsEnabled bool) string {
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	return scheme + "://" + host + ":" + strconv.Itoa(port)
}

func (h *Handler) serverRestartResponse(ctx context.Context, host string, port int, tlsEnabled bool) (map[string]any, error) {
	newURL := serverURL(host, port, tlsEnabled)
	if os.Getenv("NARRAFORK_CONTRACT_SUPPRESS_RESTART") == "1" {
		return map[string]any{"serverRestarting": true, "manualRestartRequired": false, "newUrl": newURL, "restartSuppressed": true}, nil
	}
	if h == nil || h.Restart == nil {
		return map[string]any{"serverRestarting": false, "manualRestartRequired": true, "newUrl": newURL}, nil
	}
	session, err := h.Restart.Begin()
	if err != nil {
		return nil, err
	}
	execPath, err := settingsExecutablePath()
	if err != nil {
		h.Restart.Cancel()
		return nil, err
	}
	if strings.TrimSpace(execPath) == "" {
		h.Restart.Cancel()
		return nil, fmt.Errorf("resolve current executable: empty path")
	}
	pid, err := settingsStartReplacement(ctx, execPath, os.Args[1:], restart.EnvForSession(session))
	if err != nil {
		h.Restart.Cancel()
		return nil, err
	}
	return map[string]any{"serverRestarting": true, "manualRestartRequired": false, "newUrl": newURL, "handoffUrl": session.URL, "replacementPid": pid}, nil
}

func startReplacementProcess(ctx context.Context, path string, args []string, extraEnv []string) (int, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Dir, _ = os.Getwd()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

func contextThresholdTierForModel(raw map[string]any, r *http.Request) string {
	if strings.EqualFold(r.URL.Query().Get("tier"), "large") {
		return "large"
	}
	model := r.URL.Query().Get("model")
	provider := r.URL.Query().Get("provider")
	if contextWindowForModel(raw, model, provider) > 600000 {
		return "large"
	}
	return "standard"
}

func contextWindowForModel(raw map[string]any, model, provider string) int {
	model = strings.TrimSpace(model)
	provider = strings.TrimSpace(provider)
	bare := model
	if parts := strings.SplitN(model, ":", 2); len(parts) == 2 {
		if provider == "" {
			provider = parts[0]
		}
		bare = parts[1]
	}
	if agent, ok := raw["agent"].(map[string]any); ok {
		if overrides, ok := agent["modelContextWindows"].(map[string]any); ok {
			if provider != "" {
				if value := intSetting(overrides[provider+":"+bare], 0); value > 0 {
					return value
				}
			}
			if value := intSetting(overrides[model], 0); value > 0 {
				return value
			}
		}
	}
	for _, field := range []string{"openaiProviders", "anthropicProviders", "customApiProviders", "nkpProviders", "nugProviders", "clineProviders"} {
		for _, item := range anySlice(raw[field]) {
			p, ok := item.(map[string]any)
			if !ok || boolValue(p["disabled"]) {
				continue
			}
			if provider == stringValue(p["id"]) || provider == stringValue(p["prefix"]) || provider == stringValue(p["name"]) {
				if value := intSetting(p["defaultContextWindow"], 0); value > 0 {
					return value
				}
			}
		}
	}
	if strings.HasPrefix(bare, "gpt-5") || strings.HasPrefix(bare, "gpt-4.1") {
		return 272000
	}
	if strings.Contains(bare, "200k") || strings.Contains(bare, "claude") || provider == "kiro" || strings.HasPrefix(provider, "nkp") {
		return 200000
	}
	return 128000
}

func intSetting(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func stringList(value any) []string {
	raw, _ := value.([]any)
	out := []string{}
	for _, item := range raw {
		if value := strings.TrimSpace(stringValue(item)); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) contextThresholds(w http.ResponseWriter, r *http.Request) error {
	// Keep the JSON shape aligned with the TypeScript backend and frontend API client.
	// The Go compatibility backend does not yet maintain the full built-in model
	// context-window table, so it returns the configured standard thresholds with
	// the same defaults TS uses for both standard and large tiers.
	thresholds := map[string]any{"pruneStart": 95, "compactStart": 99}
	if h.Store != nil {
		raw := h.Store.Raw()
		if agent, ok := raw["agent"].(map[string]any); ok {
			if all, ok := agent["contextThresholds"].(map[string]any); ok {
				tier := contextThresholdTierForModel(raw, r)
				if cfg, ok := all[tier].(map[string]any); ok {
					thresholds["pruneStart"] = intSetting(cfg["pruneStart"], 95)
					thresholds["compactStart"] = intSetting(cfg["compactStart"], 99)
				}
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, thresholds)
	return nil
}

func (h *Handler) testModel(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Model) == "" || strings.TrimSpace(body.Prompt) == "" {
		return httpx.ValidationError("model and prompt are required")
	}
	if httpx.JSStringLength(body.Prompt) > testModelPromptMaxChars {
		return httpx.ValidationError("prompt must be at most 10000 characters")
	}
	if _, err := agentbasic.ResolveProviderAndModel(h.Store, body.Model); err != nil {
		return httpx.BadRequest(err.Error())
	}
	result, err := agentbasic.Generate(r.Context(), h.Store, body.Model, []agentbasic.Message{{Role: "user", Content: body.Prompt}}, "")
	if err != nil {
		return httpx.Error(http.StatusBadGateway, "BAD_GATEWAY", err.Error())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"text": result.Text})
	return nil
}

func (h *Handler) generateTLS(w http.ResponseWriter, r *http.Request) error {
	now := time.Now()
	expiresAt := now.AddDate(10, 0, 0)
	certPath, keyPath, err := generateSelfSignedTLSFiles(now, expiresAt)
	if err != nil {
		return err
	}

	host := "localhost"
	port := 7778
	if h.Store != nil {
		raw := h.Store.Raw()
		host, port, _ = serverAddressState(raw)
		if _, err := h.Store.Patch(map[string]any{"server": map[string]any{"tls": map[string]any{"enabled": true, "certFile": certPath, "keyFile": keyPath}}}); err != nil {
			return err
		}
	}

	restartPayload, restartErr := h.serverRestartResponse(r.Context(), host, port, true)
	if restartErr != nil {
		return restartErr
	}
	response := map[string]any{
		"certPath":  certPath,
		"keyPath":   keyPath,
		"expiresAt": expiresAt.Format(time.RFC3339),
	}
	for key, value := range restartPayload {
		response[key] = value
	}
	httpx.WriteJSON(w, http.StatusOK, response)

	return nil
}

func generateSelfSignedTLSFiles(now, expiresAt time.Time) (string, string, error) {
	tlsDir := filepath.Join(dataDir(), "tls")
	if err := os.MkdirAll(tlsDir, 0o700); err != nil {
		return "", "", err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "NarraFork",
			Organization: []string{"NarraFork"},
		},
		NotBefore:             now,
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "*.local"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", err
	}
	certPath := filepath.Join(tlsDir, "cert.pem")
	keyPath := filepath.Join(tlsDir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func (h *Handler) addRetryRule(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	hasRetryTrigger := false
	for _, key := range []string{"domain", "keyword"} {
		value, exists := body[key]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok || text == "" {
			return httpx.ValidationError("agent.customRetryRules[0]." + key + " must be a non-empty string")
		}
		hasRetryTrigger = true
	}
	if _, exists := body["statusCode"]; exists {
		if err := validateSettingsIntRange(body["statusCode"], "agent.customRetryRules[0].statusCode", 100, 599); err != nil {
			return err
		}
		hasRetryTrigger = true
	}
	if !hasRetryTrigger {
		return httpx.BadRequest("At least one of domain, statusCode, or keyword is required")
	}
	if h.Store == nil {
		return httpx.NotImplemented("settings.retryRules")
	}
	rule := map[string]any{"id": id.Short(), "enabled": true}
	for _, key := range []string{"domain", "statusCode", "keyword", "note"} {
		if value, ok := body[key]; ok {
			rule[key] = value
		}
	}
	if err := validateAgentCustomRetryRules(map[string]any{"customRetryRules": []any{rule}}); err != nil {
		return err
	}
	raw := h.Store.Raw()
	agent, _ := raw["agent"].(map[string]any)
	rules, _ := agent["customRetryRules"].([]any)
	rules = append(rules, rule)
	_, err := h.Store.Patch(map[string]any{"agent": map[string]any{"customRetryRules": rules}})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, rule)
	return nil
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
