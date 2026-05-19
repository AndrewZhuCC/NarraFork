package settings

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type ContainerProxyRuntime interface {
	EnsureContainerProxy(ctx context.Context, enabled bool, port int) error
}

type Handler struct {
	Store        *config.Store
	ProxyRuntime ContainerProxyRuntime
}

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
		next["serverRestarting"] = true
		next["newUrl"] = serverURL(newHost, newPort, newTLSEnabled)
	}
	sanitizeSettingsForResponse(next)
	httpx.WriteJSON(w, http.StatusOK, next)
	return nil
}

func augmentProviderModelSettings(raw map[string]any) {
	raw["kiroModels"] = loadKiroModelsForSettings(raw)
	openaiCache := loadProviderModelCache("openai")
	raw["openaiModels"] = aggregateSettingsModels(raw, "openaiProviders", "openai", openaiCache)
	raw["openaiModelsGrouped"] = groupedSettingsModels(raw, "openaiProviders", "openai", openaiCache)
	raw["anthropicModelsGrouped"] = groupedSettingsModels(raw, "anthropicProviders", "anthropic", loadProviderModelCache("anthropic"))
	raw["nkpModelsGrouped"] = groupedSettingsModels(raw, "nkpProviders", "nkp", loadProviderModelCache("nkp"))
	raw["nugModelsGrouped"] = groupedSettingsModels(raw, "nugProviders", "nug", loadProviderModelCache("nug"))
	raw["clineModelsGrouped"] = clineEnabledModelsGrouped(raw, loadProviderModelCache("cline"))
	raw["customApiQuotas"] = loadObjectCache("custom-api-quotas.json")
	raw["nkpQuotas"] = loadQuotaObjectCache("nkp-quotas.json", "quotaBalance", "quotaTotalGranted")
	raw["nugQuotas"] = loadQuotaObjectCache("nug-quotas.json", "balance", "totalGranted")
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
	if len(value) <= 4 {
		return strings.Repeat("*", len(value))
	}
	return "********" + value[len(value)-4:]
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
	if value, ok := agent["questionReflectionTimeoutMs"]; ok {
		agent["questionReflectionTimeoutMs"] = clampNumberValue(value, 10000, 3600000, 300000)
	}
	return patch
}

func clampNumberValue(value any, minValue, maxValue, fallback int) float64 {
	parsed := intSetting(value, fallback)
	if parsed < minValue {
		parsed = minValue
	}
	if parsed > maxValue {
		parsed = maxValue
	}
	return float64(parsed)
}

func prepareSettingsPatch(current, patch map[string]any) error {
	if err := validateAuthSettingsPatch(patch); err != nil {
		return err
	}
	if err := preserveMaskedSecrets(current, patch); err != nil {
		return err
	}
	if err := validateProviderPrefixes(current, patch); err != nil {
		return err
	}
	if err := validateTLSFiles(patch); err != nil {
		return err
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
		for _, key := range []string{"explore", "plan", "general"} {
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

func validateProviderPrefixes(current, patch map[string]any) error {
	type providerRef struct{ prefix, source string }
	refs := []providerRef{}
	for _, field := range []string{"customApiProviders", "nkpProviders", "nugProviders", "clineProviders"} {
		providers := anySlice(current[field])
		if incoming, ok := patch[field].([]any); ok {
			providers = incoming
		}
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
	seen := map[string]string{}
	for _, ref := range refs {
		if ref.prefix == "kiro" || ref.prefix == "codex" {
			return httpx.BadRequest("Provider prefix \"" + ref.prefix + "\" is reserved")
		}
		if existing := seen[ref.prefix]; existing != "" {
			return httpx.BadRequest("Duplicate provider prefix \"" + ref.prefix + "\" found in " + existing + " and " + ref.source)
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
		return httpx.BadRequest("TLS certificate file not found: " + firstString(certFile, "(empty)"))
	}
	if keyFile == "" || !fileExists(keyFile) {
		return httpx.BadRequest("TLS private key file not found: " + firstString(keyFile, "(empty)"))
	}
	if caFile := stringValue(tls["caFile"]); caFile != "" && !fileExists(caFile) {
		return httpx.BadRequest("TLS CA certificate file not found: " + caFile)
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
		return httpx.BadRequest("model and prompt are required")
	}
	result, err := agentbasic.Generate(r.Context(), h.Store, body.Model, []agentbasic.Message{{Role: "user", Content: body.Prompt}}, "")
	if err != nil {
		return err
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"certPath":         certPath,
		"keyPath":          keyPath,
		"expiresAt":        expiresAt.Format(time.RFC3339),
		"newUrl":           serverURL(host, port, true),
		"serverRestarting": true,
	})
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
	if body["domain"] == nil && body["statusCode"] == nil && body["keyword"] == nil {
		return httpx.BadRequest("At least one of domain, statusCode, or keyword is required")
	}
	ruleID := id.New()
	body["id"] = ruleID
	raw := h.Store.Raw()
	rules, _ := raw["retryRules"].([]any)
	rules = append(rules, body)
	_, err := h.Store.Patch(map[string]any{"retryRules": rules})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": ruleID})
	return nil
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
