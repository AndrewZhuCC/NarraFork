package agentbasic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/config"
)

const maxProviderJSONFileBytes = 512 * 1024

const followDefaultModelSentinel = "__default__"
const aggregationModelPrefix = "__agg__:"

func ResolveProviderAndModel(settings *config.Store, model string) (ProviderResolution, error) {
	requestedProvider, requestedModel := splitModel(model)
	provider, err := resolveProvider(settings, model)
	if err != nil {
		return ProviderResolution{}, err
	}
	adapter := adapterForProvider(provider)
	return ProviderResolution{
		RequestedProvider: requestedProvider,
		RequestedModel:    requestedModel,
		Provider:          provider.Prefix,
		Model:             provider.Model,
		Config:            provider,
		Adapter:           adapter,
	}, nil
}

func resolveProvider(settings *config.Store, model string) (Provider, error) {
	raw := map[string]any{}
	if settings != nil {
		raw = settings.Raw()
	}
	return resolveProviderFromRaw(raw, model)
}

func resolveEffectiveModelFromRaw(raw map[string]any, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || isFollowDefaultModelValue(model) {
		return resolveConfiguredDefaultModelFromRaw(raw)
	}
	if aggID, pinnedModel, ok := parseAggregationModelValue(model); ok {
		if pinnedModel != "" {
			if sanitized := sanitizeResolvedModelCandidate(pinnedModel); sanitized != "" {
				return sanitized
			}
			return resolveConfiguredDefaultModelFromRaw(raw)
		}
		if resolved := sanitizeResolvedModelCandidate(resolveAggregationFromRaw(raw, aggID)); resolved != "" {
			return resolved
		}
		return resolveConfiguredDefaultModelFromRaw(raw)
	}
	return model
}

func resolveConfiguredDefaultModelFromRaw(raw map[string]any) string {
	agent, _ := raw["agent"].(map[string]any)
	configured := strings.TrimSpace(stringValue(agent["defaultModel"]))
	if configured == "" || isFollowDefaultModelValue(configured) {
		return config.DefaultAgentModel
	}
	if aggID, pinnedModel, ok := parseAggregationModelValue(configured); ok {
		if pinnedModel != "" {
			if sanitized := sanitizeResolvedModelCandidate(pinnedModel); sanitized != "" {
				return sanitized
			}
			return config.DefaultAgentModel
		}
		if resolved := sanitizeResolvedModelCandidate(resolveAggregationFromRaw(raw, aggID)); resolved != "" {
			return resolved
		}
		return config.DefaultAgentModel
	}
	return configured
}

func isFollowDefaultModelValue(model string) bool {
	model = strings.TrimSpace(model)
	if model == followDefaultModelSentinel {
		return true
	}
	prefix, bare := splitModel(model)
	return prefix != "" && strings.TrimSpace(bare) == followDefaultModelSentinel
}

func sanitizeResolvedModelCandidate(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if isFollowDefaultModelValue(model) {
		return config.DefaultAgentModel
	}
	return model
}

func parseAggregationModelValue(model string) (string, string, bool) {
	model = strings.TrimSpace(model)
	if !strings.HasPrefix(model, aggregationModelPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(model, aggregationModelPrefix)
	if idx := strings.Index(rest, ":"); idx >= 0 {
		return rest[:idx], strings.TrimSpace(rest[idx+1:]), true
	}
	return rest, "", true
}

func resolveAggregationFromRaw(raw map[string]any, aggID string) string {
	aggID = strings.TrimSpace(aggID)
	if aggID == "" {
		return ""
	}
	agent, _ := raw["agent"].(map[string]any)
	for _, item := range readArray(agent["modelAggregations"]) {
		agg, ok := item.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(agg["id"])) != aggID {
			continue
		}
		for _, model := range readArray(agg["models"]) {
			if candidate := strings.TrimSpace(stringValue(model)); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func resolveProviderFromRaw(raw map[string]any, model string) (Provider, error) {
	effectiveModel := resolveEffectiveModelFromRaw(raw, model)
	provider, err := resolveProviderExact(raw, effectiveModel)
	if err == nil {
		return applyProviderRuntimeDefaults(raw, provider), nil
	}
	prefix, _ := splitModel(effectiveModel)
	if !isGoRuntimeUnsupportedProvider(prefix) {
		return Provider{}, err
	}
	if !providerFallbackAllowed(raw) {
		return Provider{}, fmt.Errorf("provider %q is not configured or supported by go_backend runtime; automatic fallback is disabled to avoid running a different provider/model: %w", prefix, err)
	}
	for _, candidate := range fallbackProviderCandidates(raw) {
		provider, fallbackErr := resolveProviderExact(raw, candidate)
		if fallbackErr == nil {
			return applyProviderRuntimeDefaults(raw, provider), nil
		}
	}
	return Provider{}, fmt.Errorf("provider %q is not supported by go_backend runtime yet and no OpenAI/Anthropic-compatible fallback is configured: %w", prefix, err)
}

func applyProviderRuntimeDefaults(raw map[string]any, provider Provider) Provider {
	if strings.TrimSpace(provider.DefaultReasoningEffort) == "" && (provider.Kind == "codex" || strings.EqualFold(provider.APIMode, "codex")) {
		provider.DefaultReasoningEffort = settingsNestedString(raw, "codex", "defaultReasoningEffort")
	}
	if strings.TrimSpace(provider.DefaultReasoningEffort) == "" {
		provider.DefaultReasoningEffort = settingsNestedString(raw, "agent", "defaultReasoningEffort")
	}
	provider.DefaultReasoningEffort = normalizeReasoningEffort(provider.DefaultReasoningEffort)
	return provider
}

func settingsNestedString(raw map[string]any, section string, key string) string {
	if raw == nil {
		return ""
	}
	sectionMap, _ := raw[section].(map[string]any)
	return stringValue(sectionMap[key])
}

func optionalBoolValue(values map[string]any, key string) *bool {
	if _, ok := values[key]; !ok {
		return nil
	}
	value := boolValue(values[key])
	return &value
}

func resolveProviderExact(raw map[string]any, model string) (Provider, error) {
	prefix, bareModel := splitModel(model)
	if prefix == "kiro" {
		if provider, ok := resolveKiroProvider(raw, bareModel); ok {
			return provider, nil
		}
	}
	if prefix == "codex" {
		if provider, ok := resolveCodexProvider(raw, bareModel); ok {
			return provider, nil
		}
	}
	for _, item := range readArray(raw["anthropicProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		pfx := stringValue(provider["prefix"])
		if pfx == "" {
			pfx = "anthropic"
		}
		if pfx != prefix {
			continue
		}
		if bareModel == "" {
			bareModel = stringValue(provider["defaultModel"])
		}
		apiKey := stringValue(provider["apiKey"])
		if apiKey == "" {
			continue
		}
		baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}
		return Provider{Prefix: pfx, Kind: "anthropic", APIKey: apiKey, BaseURL: baseURL, Model: bareModel, OfficialAPI: boolValue(provider["officialApi"]), DefaultReasoningEffort: stringValue(provider["defaultReasoningEffort"]), ProxyURL: stringValue(provider["proxy"]), TLSRejectUnauthorized: optionalBoolValue(provider, "tlsRejectUnauthorized")}, nil
	}
	for _, item := range readArray(raw["customApiProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(stringValue(provider["protocol"])))
		if !strings.Contains(protocol, "anthropic") {
			continue
		}
		pfx := stringValue(provider["prefix"])
		if pfx == "" {
			pfx = "anthropic"
		}
		if pfx != prefix {
			continue
		}
		if bareModel == "" {
			bareModel = stringValue(provider["defaultModel"])
		}
		apiKey := stringValue(provider["apiKey"])
		if apiKey == "" {
			continue
		}
		baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}
		return Provider{Prefix: pfx, Kind: "anthropic", APIKey: apiKey, BaseURL: baseURL, Model: bareModel, OfficialAPI: strings.Contains(protocol, "official"), DefaultReasoningEffort: stringValue(provider["defaultReasoningEffort"]), ProxyURL: stringValue(provider["proxy"]), TLSRejectUnauthorized: optionalBoolValue(provider, "tlsRejectUnauthorized")}, nil
	}
	for _, spec := range []struct{ Key, DefaultPrefix, Kind string }{{"nkpProviders", "nkp", "nkp"}, {"nugProviders", "nug", "nug"}, {"clineProviders", "cline", "cline"}} {
		for _, item := range readArray(raw[spec.Key]) {
			provider, ok := item.(map[string]any)
			if !ok || boolValue(provider["disabled"]) {
				continue
			}
			pfx := stringValue(provider["prefix"])
			if pfx == "" {
				pfx = spec.DefaultPrefix
			}
			if pfx != prefix {
				continue
			}
			if bareModel == "" {
				bareModel = stringValue(provider["defaultModel"])
				if bareModel == "" {
					if models := readArray(provider["enabledModels"]); len(models) > 0 {
						bareModel = stringValue(models[0])
					}
				}
			}
			apiKey := firstNonEmpty(stringValue(provider["apiKey"]), stringValue(provider["accessToken"]), stringValue(provider["token"]))
			if apiKey == "" && spec.Kind == "cline" {
				if credential, ok := firstClineCredentialFromDisk(); ok {
					apiKey = stringValue(credential["accessToken"])
				}
			}
			if apiKey == "" || bareModel == "" {
				continue
			}
			baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
			if baseURL == "" {
				continue
			}
			apiMode := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(provider["channelType"]), stringValue(provider["apiMode"]))))
			if spec.Kind == "nug" {
				if channel, routedModel := nugModelMeta(provider, bareModel); channel != "" {
					apiMode = channel
					if routedModel != "" {
						bareModel = routedModel
					}
				} else if channel, routedModel := resolveRemoteNugModelMeta(baseURL, apiKey, bareModel); channel != "" {
					apiMode = channel
					if routedModel != "" {
						bareModel = routedModel
					}
				}
			}
			return Provider{Prefix: pfx, Kind: spec.Kind, APIKey: apiKey, BaseURL: baseURL, Model: bareModel, APIMode: apiMode}, nil
		}
	}
	for _, key := range []string{"openaiProviders", "customApiProviders"} {
		for _, item := range readArray(raw[key]) {
			provider, ok := item.(map[string]any)
			if !ok || boolValue(provider["disabled"]) {
				continue
			}
			pfx := stringValue(provider["prefix"])
			protocol := stringValue(provider["protocol"])
			if pfx == "" {
				pfx = "openai"
			}
			if pfx != prefix {
				continue
			}
			if key == "customApiProviders" && !isOpenAICompatibleCustomProtocol(protocol) {
				continue
			}
			apiMode := strings.ToLower(strings.TrimSpace(stringValue(provider["apiMode"])))
			kind := "openai"
			if key == "customApiProviders" {
				apiMode = customOpenAIAPIMode(protocol)
				if apiMode == "codex" {
					kind = "codex"
				}
			}
			if boolValue(provider["responsesApi"]) && apiMode == "" {
				apiMode = "responses"
			}
			if apiMode == "codex" {
				kind = "codex"
			}
			if bareModel == "" {
				bareModel = stringValue(provider["defaultModel"])
			}
			apiKey := stringValue(provider["apiKey"])
			if apiKey == "" {
				continue
			}
			baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
			if baseURL == "" {
				baseURL = "https://api.openai.com/v1"
			}
			accountID := ""
			var useWebSocket *bool
			if kind == "codex" {
				accountID = firstNonEmpty(stringValue(provider["codexAccountId"]), stringValue(provider["accountId"]))
				value := boolValue(provider["codexWebSocket"])
				useWebSocket = &value
			}
			return Provider{Prefix: pfx, Kind: kind, APIKey: apiKey, BaseURL: baseURL, Model: bareModel, APIMode: apiMode, AccountID: accountID, UseWebSocket: useWebSocket}, nil
		}
	}
	if prefix == "anthropic" {
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey != "" {
			if bareModel == "" {
				bareModel = "claude-3-5-haiku-latest"
			}
			baseURL := strings.TrimRight(os.Getenv("ANTHROPIC_BASE_URL"), "/")
			if baseURL == "" {
				baseURL = "https://api.anthropic.com/v1"
			}
			return Provider{Prefix: prefix, Kind: "anthropic", APIKey: apiKey, BaseURL: baseURL, Model: bareModel}, nil
		}
	}
	if prefix == "openai" {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey != "" {
			if bareModel == "" {
				bareModel = "gpt-4o-mini"
			}
			baseURL := strings.TrimRight(os.Getenv("OPENAI_BASE_URL"), "/")
			if baseURL == "" {
				baseURL = "https://api.openai.com/v1"
			}
			return Provider{Prefix: prefix, Kind: "openai", APIKey: apiKey, BaseURL: baseURL, Model: bareModel}, nil
		}
	}
	return Provider{}, fmt.Errorf("no provider configured for model %q", model)
}

var nugRemoteMetaCache sync.Map

type nugRemoteMetaEntry struct {
	FetchedAt time.Time
	Models    []map[string]any
	Health    map[string]bool
}

func resolveRemoteNugModelMeta(baseURL, apiKey, model string) (string, string) {
	baseURL = strings.TrimRight(baseURL, "/")
	cacheKey := baseURL + "|" + apiKey
	if cached, ok := nugRemoteMetaCache.Load(cacheKey); ok {
		entry := cached.(nugRemoteMetaEntry)
		if time.Since(entry.FetchedAt) < 5*time.Minute {
			return nugModelMetaFromListWithHealth(entry.Models, model, entry.Health)
		}
	}
	models, err := fetchNugRemoteModels(baseURL, apiKey)
	if err != nil || len(models) == 0 {
		return "", ""
	}
	health := fetchNugChannelHealth(baseURL, apiKey)
	nugRemoteMetaCache.Store(cacheKey, nugRemoteMetaEntry{FetchedAt: time.Now(), Models: models, Health: health})
	return nugModelMetaFromListWithHealth(models, model, health)
}

func fetchNugRemoteModels(baseURL, apiKey string) ([]map[string]any, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	endpoints := []string{baseURL + "/v1/models", baseURL + "/models"}
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		res, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
		_ = res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			continue
		}
		models := parseNugModelsPayload(body)
		if len(models) > 0 {
			return models, nil
		}
	}
	return nil, fmt.Errorf("no NUG models returned")
}

func fetchNugChannelHealth(baseURL, apiKey string) map[string]bool {
	client := &http.Client{Timeout: 2 * time.Second}
	for _, endpoint := range []string{baseURL + "/v1/channels/health", baseURL + "/channels/health"} {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		res, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024*1024))
		_ = res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			continue
		}
		if health := parseNugHealthPayload(body); len(health) > 0 {
			return health
		}
	}
	return nil
}

func parseNugHealthPayload(body []byte) map[string]bool {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	health := map[string]bool{}
	collectNugHealth(root, health)
	return health
}

func collectNugHealth(value any, health map[string]bool) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectNugHealth(item, health)
		}
	case map[string]any:
		channel := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(typed["channelType"]), stringValue(typed["channel"]), stringValue(typed["type"]), stringValue(typed["id"]), stringValue(typed["name"]))))
		if channel != "" {
			health[channel] = !boolValue(typed["disabled"]) && firstNonEmpty(stringValue(typed["status"]), "healthy") != "down" && firstNonEmpty(stringValue(typed["status"]), "healthy") != "unhealthy"
		}
		for _, key := range []string{"data", "channels", "items", "results"} {
			if child, ok := typed[key]; ok {
				collectNugHealth(child, health)
			}
		}
	}
}

func parseNugModelsPayload(body []byte) []map[string]any {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	return collectNugModelMaps(root)
}

func collectNugModelMaps(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			out = append(out, collectNugModelMaps(item)...)
		}
		return out
	case map[string]any:
		if looksLikeNugModel(typed) {
			return []map[string]any{typed}
		}
		for _, key := range []string{"data", "models", "items", "results"} {
			if child, ok := typed[key]; ok {
				if models := collectNugModelMaps(child); len(models) > 0 {
					return models
				}
			}
		}
	}
	return nil
}

func looksLikeNugModel(value map[string]any) bool {
	return firstNonEmpty(stringValue(value["id"]), stringValue(value["model"]), stringValue(value["modelId"]), stringValue(value["name"])) != "" &&
		firstNonEmpty(stringValue(value["channelType"]), stringValue(value["channel"]), stringValue(value["apiMode"]), stringValue(value["routedModel"])) != ""
}

func nugModelMetaFromList(models []map[string]any, model string) (string, string) {
	return nugModelMetaFromListWithHealth(models, model, nil)
}

func nugModelMetaFromListWithHealth(models []map[string]any, model string, health map[string]bool) (string, string) {
	fallbackChannel := ""
	fallbackRouted := ""
	for _, meta := range models {
		idValue := firstNonEmpty(stringValue(meta["id"]), stringValue(meta["model"]), stringValue(meta["modelId"]), stringValue(meta["name"]))
		if model == "" || idValue == model {
			channel := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(meta["channelType"]), stringValue(meta["channel"]), stringValue(meta["apiMode"]))))
			routed := strings.TrimSpace(firstNonEmpty(stringValue(meta["routedModel"]), stringValue(meta["targetModel"]), stringValue(meta["upstreamModel"])))
			if channel == "" {
				continue
			}
			if fallbackChannel == "" {
				fallbackChannel, fallbackRouted = channel, routed
			}
			if healthy, ok := health[channel]; !ok || healthy {
				return channel, routed
			}
		}
	}
	return fallbackChannel, fallbackRouted
}

func nugModelMeta(provider map[string]any, model string) (string, string) {
	model = strings.TrimSpace(model)
	readMeta := func(meta map[string]any) (string, string) {
		channel := strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(meta["channelType"]), stringValue(meta["channel"]), stringValue(meta["apiMode"]))))
		routed := strings.TrimSpace(firstNonEmpty(stringValue(meta["routedModel"]), stringValue(meta["model"]), stringValue(meta["modelId"])))
		return channel, routed
	}
	if metaByModel, ok := provider["modelMeta"].(map[string]any); ok && model != "" {
		if raw, ok := metaByModel[model].(map[string]any); ok {
			return readMeta(raw)
		}
	}
	for _, item := range readArray(provider["models"]) {
		meta, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idValue := firstNonEmpty(stringValue(meta["id"]), stringValue(meta["model"]), stringValue(meta["modelId"]), stringValue(meta["name"]))
		if model == "" || idValue == model {
			return readMeta(meta)
		}
	}
	for _, item := range readArray(provider["enabledModels"]) {
		meta, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idValue := firstNonEmpty(stringValue(meta["id"]), stringValue(meta["model"]), stringValue(meta["modelId"]), stringValue(meta["name"]))
		if model == "" || idValue == model {
			return readMeta(meta)
		}
	}
	return "", ""
}

func resolveCodexProvider(raw map[string]any, bareModel string) (Provider, bool) {
	codex, _ := raw["codex"].(map[string]any)
	codexProxy := stringValue(codex["proxy"])
	for _, item := range readArray(raw["codexProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		if bareModel == "" {
			bareModel = firstNonEmpty(stringValue(provider["defaultModel"]), "gpt-5.3-codex")
		}
		apiKey := firstNonEmpty(stringValue(provider["apiKey"]), stringValue(provider["accessToken"]), stringValue(provider["token"]))
		if apiKey == "" {
			continue
		}
		baseURL := strings.TrimRight(firstNonEmpty(stringValue(provider["baseUrl"]), "https://chatgpt.com/backend-api/codex"), "/")
		return Provider{Prefix: "codex", Kind: "codex", APIKey: apiKey, BaseURL: baseURL, Model: bareModel, APIMode: "codex", AccountID: stringValue(provider["accountId"]), ProxyURL: codexProxy}, true
	}
	if bareModel == "" {
		bareModel = firstNonEmpty(stringValue(codex["defaultModel"]), "gpt-5.3-codex")
	}
	if codex != nil {
		apiKey := firstNonEmpty(stringValue(codex["apiKey"]), stringValue(codex["accessToken"]), stringValue(codex["token"]))
		if apiKey != "" {
			baseURL := strings.TrimRight(firstNonEmpty(stringValue(codex["baseUrl"]), "https://chatgpt.com/backend-api/codex"), "/")
			return Provider{Prefix: "codex", Kind: "codex", APIKey: apiKey, BaseURL: baseURL, Model: bareModel, APIMode: "codex", AccountID: stringValue(codex["accountId"]), LoadBalancingMode: stringValue(codex["loadBalancingMode"]), UseWebSocket: codexUseWebSocketOption(codex), ProxyURL: codexProxy}, true
		}
	}
	if credential, ok := firstCodexCredentialFromDiskWithOptions(codex); ok {
		baseURL := "https://chatgpt.com/backend-api/codex"
		if codex != nil {
			baseURL = strings.TrimRight(firstNonEmpty(stringValue(codex["baseUrl"]), baseURL), "/")
		}
		return Provider{Prefix: "codex", Kind: "codex", APIKey: stringValue(credential["accessToken"]), BaseURL: baseURL, Model: bareModel, APIMode: "codex", AccountID: stringValue(credential["accountId"]), CredentialID: stringValue(credential["id"]), CredentialPool: codexCredentialPoolFromDiskWithOptions(baseURL, bareModel, codex), LoadBalancingMode: stringValue(codex["loadBalancingMode"]), UseWebSocket: codexUseWebSocketOption(codex), ProxyURL: codexProxy}, true
	}
	return Provider{}, false
}

func firstClineCredentialFromDisk() (map[string]any, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, false
	}
	path := filepath.Join(home, ".narrafork", "cline-credentials.json")
	var credential map[string]any
	if !readProviderJSONFile(path, &credential) {
		return nil, false
	}
	if strings.TrimSpace(stringValue(credential["accessToken"])) == "" {
		return nil, false
	}
	if clineCredentialFresh(credential) {
		return credential, true
	}
	if refreshed, err := refreshClineCredential(credential); err == nil && refreshed != nil {
		_ = writeProviderJSON(path, refreshed)
		return refreshed, true
	}
	return credential, true
}

func refreshClineCredentialFromDisk() (map[string]any, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, false
	}
	path := filepath.Join(home, ".narrafork", "cline-credentials.json")
	var credential map[string]any
	if !readProviderJSONFile(path, &credential) {
		return nil, false
	}
	refreshed, err := refreshClineCredential(credential)
	if err != nil || refreshed == nil {
		return nil, false
	}
	_ = writeProviderJSON(path, refreshed)
	return refreshed, true
}

func clineCredentialFresh(credential map[string]any) bool {
	expiresAt := intValue(credential["expiresAt"], 0)
	return expiresAt == 0 || int64(expiresAt) >= time.Now().Add(5*time.Minute).Unix()
}

func refreshClineCredential(credential map[string]any) (map[string]any, error) {
	refreshToken := strings.TrimSpace(stringValue(credential["refreshToken"]))
	if refreshToken == "" {
		return nil, nil
	}
	payload, _ := json.Marshal(map[string]any{"refreshToken": refreshToken, "grantType": "refresh_token"})
	baseURL := strings.TrimRight(firstNonEmpty(os.Getenv("CLINE_API_BASE_URL"), "https://api.cline.bot"), "/")
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Post(baseURL+"/api/v1/auth/refresh", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 200000))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("cline token refresh failed: %s %s", res.Status, string(body))
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	nested, _ := decoded["data"].(map[string]any)
	if !boolValue(decoded["success"]) || stringValue(nested["accessToken"]) == "" {
		return nil, fmt.Errorf("cline token refresh returned invalid data")
	}
	updated := map[string]any{}
	for key, value := range credential {
		updated[key] = value
	}
	updated["accessToken"] = stringValue(nested["accessToken"])
	if nextRefresh := stringValue(nested["refreshToken"]); nextRefresh != "" {
		updated["refreshToken"] = nextRefresh
	}
	if expiresAt := stringValue(nested["expiresAt"]); expiresAt != "" {
		if parsed, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			updated["expiresAt"] = float64(parsed.Unix())
		}
	}
	if updated["expiresAt"] == nil {
		updated["expiresAt"] = float64(time.Now().Add(time.Hour).Unix())
	}
	return updated, nil
}

func codexCredentialPoolFromDisk(baseURL string, model string) []Provider {
	return codexCredentialPoolFromDiskWithOptions(baseURL, model, nil)
}

func codexUseWebSocketOption(codex map[string]any) *bool {
	if codex == nil {
		return nil
	}
	if _, ok := codex["useWebSocket"]; !ok {
		return nil
	}
	value := boolValue(codex["useWebSocket"])
	return &value
}

func codexCredentialPoolFromDiskWithOptions(baseURL string, model string, codex map[string]any) []Provider {
	entries, ok := codexCredentialEntriesFromDisk()
	if !ok {
		return nil
	}
	entries = sortCodexCredentials(entries, codex)
	pool := []Provider{}
	mode := stringValue(codex["loadBalancingMode"])
	codexProxy := stringValue(codex["proxy"])
	for _, entry := range entries {
		if boolValue(entry["disabled"]) || strings.TrimSpace(stringValue(entry["accessToken"])) == "" || codexAccessTokenOnlyExpired(entry) {
			continue
		}
		pool = append(pool, Provider{Prefix: "codex", Kind: "codex", APIKey: stringValue(entry["accessToken"]), BaseURL: baseURL, Model: model, APIMode: "codex", AccountID: stringValue(entry["accountId"]), CredentialID: stringValue(entry["id"]), LoadBalancingMode: mode, UseWebSocket: codexUseWebSocketOption(codex), ProxyURL: codexProxy})
	}
	return pool
}

func codexCredentialEntriesFromDisk() ([]map[string]any, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, false
	}
	path := filepath.Join(home, ".narrafork", "codex-credentials.json")
	var entries []map[string]any
	if !readProviderJSONFile(path, &entries) {
		return nil, false
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return intValue(entries[i]["priority"], i) < intValue(entries[j]["priority"], j)
	})
	return entries, true
}

func firstCodexCredentialFromDisk() (map[string]any, bool) {
	return firstCodexCredentialFromDiskWithOptions(nil)
}

func firstCodexCredentialFromDiskWithOptions(codex map[string]any) (map[string]any, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, false
	}
	path := filepath.Join(home, ".narrafork", "codex-credentials.json")
	entries, ok := codexCredentialEntriesFromDisk()
	if !ok {
		return nil, false
	}
	entries = sortCodexCredentials(entries, codex)
	updated := false
	for i, entry := range entries {
		if boolValue(entry["disabled"]) {
			continue
		}
		if !codexCredentialFresh(entry) {
			if strings.TrimSpace(stringValue(entry["refreshToken"])) == "" {
				continue
			}
			if refreshed, err := refreshCodexCredential(entry); err == nil && refreshed != nil {
				entries[i] = refreshed
				entry = refreshed
				updated = true
			}
		}
		if strings.TrimSpace(stringValue(entry["accessToken"])) != "" {
			if updated {
				_ = writeProviderJSON(path, entries)
			}
			return entry, true
		}
	}
	if updated {
		_ = writeProviderJSON(path, entries)
	}
	return nil, false
}

func sortCodexCredentials(entries []map[string]any, codex map[string]any) []map[string]any {
	out := append([]map[string]any(nil), entries...)
	mode := strings.ToLower(strings.TrimSpace(stringValue(codex["loadBalancingMode"])))
	tierOrder := []string{}
	for _, item := range readArray(codex["tierOrder"]) {
		if value := strings.ToLower(strings.TrimSpace(stringValue(item))); value != "" {
			tierOrder = append(tierOrder, value)
		}
	}
	if len(tierOrder) == 0 {
		tierOrder = []string{"pro", "prolite", "plus", "team", "free"}
	}
	tierRank := func(entry map[string]any) int {
		tier := ""
		if usage, ok := entry["usage"].(map[string]any); ok {
			tier = strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(usage["plan_type"]), stringValue(usage["planTier"]), stringValue(usage["tier"]))))
		}
		if tier == "" {
			tier = strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(entry["planTier"]), stringValue(entry["tier"]), stringValue(entry["accountTier"]))))
		}
		for index, configured := range tierOrder {
			if tier == configured {
				return index
			}
		}
		return len(tierOrder) + intValue(entry["priority"], 0)
	}
	usageRank := func(entry map[string]any) int {
		if usage, ok := entry["usage"].(map[string]any); ok {
			return intValue(firstNonEmptyAny(usage["usedPercent"], usage["usagePercent"], usage["percent"]), 0)
		}
		return intValue(firstNonEmptyAny(entry["usedPercent"], entry["usagePercent"]), 0)
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch mode {
		case "tier-balanced", "tiered_sticky":
			if tierRank(out[i]) != tierRank(out[j]) {
				return tierRank(out[i]) < tierRank(out[j])
			}
			if usageRank(out[i]) != usageRank(out[j]) {
				return usageRank(out[i]) < usageRank(out[j])
			}
		case "balanced":
			if usageRank(out[i]) != usageRank(out[j]) {
				return usageRank(out[i]) < usageRank(out[j])
			}
		}
		return intValue(out[i]["priority"], i) < intValue(out[j]["priority"], j)
	})
	return out
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return typed
			}
		case nil:
			continue
		default:
			return value
		}
	}
	return nil
}

func codexCredentialFresh(entry map[string]any) bool {
	expiresAt := intValue(entry["expiresAt"], 0)
	return expiresAt == 0 || int64(expiresAt) >= time.Now().Add(30*time.Second).UnixMilli()
}

func codexAccessTokenOnlyExpired(entry map[string]any) bool {
	if strings.TrimSpace(stringValue(entry["accessToken"])) == "" || strings.TrimSpace(stringValue(entry["refreshToken"])) != "" {
		return false
	}
	expiresAt := intValue(entry["expiresAt"], 0)
	return expiresAt > 0 && int64(expiresAt) < time.Now().Add(30*time.Second).UnixMilli()
}

func refreshCodexCredential(entry map[string]any) (map[string]any, error) {
	refreshToken := strings.TrimSpace(stringValue(entry["refreshToken"]))
	if refreshToken == "" {
		return nil, nil
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", "app_EMoamEEZ73f0CkXaXp7hrann")
	issuer := strings.TrimRight(firstNonEmpty(os.Getenv("CODEX_ISSUER_URL"), "https://auth.openai.com"), "/")
	req, err := http.NewRequest(http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 200000))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("codex token refresh failed: %s %s", res.Status, string(body))
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	if stringValue(decoded["access_token"]) == "" {
		return nil, fmt.Errorf("codex token refresh returned no access_token")
	}
	updated := map[string]any{}
	for key, value := range entry {
		updated[key] = value
	}
	updated["accessToken"] = stringValue(decoded["access_token"])
	if nextRefresh := stringValue(decoded["refresh_token"]); nextRefresh != "" {
		updated["refreshToken"] = nextRefresh
	}
	if expiresIn, ok := decoded["expires_in"].(float64); ok && expiresIn > 0 {
		updated["expiresAt"] = float64(time.Now().UnixMilli()) + expiresIn*1000
	}
	return updated, nil
}

func markCodexCredentialRuntimeFailure(credentialID string, err error) {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil || home == "" || credentialID == "" || err == nil {
		return
	}
	path := filepath.Join(home, ".narrafork", "codex-credentials.json")
	entries, ok := codexCredentialEntriesFromDisk()
	if !ok {
		return
	}
	classification := classifyCodexRuntimeError(err)
	updated := false
	for _, entry := range entries {
		if stringValue(entry["id"]) != credentialID {
			continue
		}
		now := time.Now().Format(time.RFC3339)
		failureCount := intValue(entry["failureCount"], 0) + 1
		entry["failureCount"] = float64(failureCount)
		entry["lastUsedAt"] = now
		entry["lastRuntimeError"] = err.Error()
		entry["lastRuntimeErrorAt"] = now
		if classification.DisabledReason != "" {
			entry["disabled"] = true
			entry["disabledReason"] = classification.DisabledReason
			if classification.QuotaResetsAt > 0 {
				entry["quotaResetsAt"] = classification.QuotaResetsAt
			}
			unbindCodexSessionsByCredential(credentialID)
		} else if failureCount >= 3 {
			entry["disabled"] = true
			entry["disabledReason"] = "too_many_failures"
			unbindCodexSessionsByCredential(credentialID)
		}
		updated = true
		break
	}
	if updated {
		_ = writeProviderJSON(path, entries)
	}
}

func markCodexCredentialRuntimeSuccess(credentialID string) {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil || home == "" || credentialID == "" {
		return
	}
	path := filepath.Join(home, ".narrafork", "codex-credentials.json")
	entries, ok := codexCredentialEntriesFromDisk()
	if !ok {
		return
	}
	updated := false
	for _, entry := range entries {
		if stringValue(entry["id"]) != credentialID {
			continue
		}
		entry["successCount"] = float64(intValue(entry["successCount"], 0) + 1)
		entry["failureCount"] = float64(0)
		entry["lastUsedAt"] = time.Now().Format(time.RFC3339)
		delete(entry, "lastRuntimeError")
		delete(entry, "lastRuntimeErrorAt")
		updated = true
		break
	}
	if updated {
		_ = writeProviderJSON(path, entries)
	}
}

func readProviderJSONFile(path string, dst any) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxProviderJSONFileBytes+1))
	if err != nil || len(data) > maxProviderJSONFileBytes {
		return false
	}
	return json.Unmarshal(data, dst) == nil
}

func writeProviderJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func resolveKiroProvider(raw map[string]any, bareModel string) (Provider, bool) {
	for _, item := range readArray(raw["kiroProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		if bareModel == "" {
			bareModel = firstNonEmpty(stringValue(provider["defaultModel"]), "claude-sonnet-4.5")
		}
		apiKey := firstNonEmpty(stringValue(provider["apiKey"]), stringValue(provider["accessToken"]), stringValue(provider["token"]))
		baseURL := strings.TrimRight(stringValue(provider["baseUrl"]), "/")
		if apiKey == "" || baseURL == "" {
			continue
		}
		return Provider{Prefix: "kiro", Kind: "kiro", APIKey: apiKey, BaseURL: baseURL, Model: bareModel}, true
	}
	kiro, _ := raw["kiro"].(map[string]any)
	if kiro == nil {
		return Provider{}, false
	}
	if bareModel == "" {
		bareModel = firstNonEmpty(stringValue(kiro["defaultModel"]), "claude-sonnet-4.5")
	}
	apiKey := firstNonEmpty(stringValue(kiro["apiKey"]), stringValue(kiro["accessToken"]), stringValue(kiro["token"]))
	baseURL := strings.TrimRight(firstNonEmpty(stringValue(kiro["baseUrl"]), stringValue(kiro["apiBaseUrl"])), "/")
	if baseURL == "" && (stringValue(kiro["credentialsPath"]) != "" || stringValue(kiro["configPath"]) != "") {
		return Provider{Prefix: "kiro", Kind: "kiro", APIKey: apiKey, BaseURL: "kiro-plugin", Model: bareModel, KiroCredentialsPath: stringValue(kiro["credentialsPath"]), KiroConfigPath: stringValue(kiro["configPath"])}, true
	}
	if apiKey == "" || baseURL == "" {
		return Provider{}, false
	}
	return Provider{Prefix: "kiro", Kind: "kiro", APIKey: apiKey, BaseURL: baseURL, Model: bareModel}, true
}

func adapterForProvider(provider Provider) ProviderAdapter {
	switch strings.TrimSpace(provider.Kind) {
	case "anthropic":
		return &AnthropicMessagesAdapter{Provider: provider}
	case "kiro", "nkp":
		return &KiroProviderAdapter{Provider: provider}
	case "nug":
		return &NugProviderAdapter{Provider: provider}
	default:
		return &OpenAIChatCompletionsAdapter{Provider: provider}
	}
}

func isGoRuntimeUnsupportedProvider(prefix string) bool {
	switch strings.TrimSpace(prefix) {
	case "kiro", "codex":
		return true
	default:
		return false
	}
}

func providerFallbackAllowed(raw map[string]any) bool {
	agent, _ := raw["agent"].(map[string]any)
	return boolValue(agent["allowGoProviderFallback"])
}

func fallbackProviderCandidates(raw map[string]any) []string {
	candidates := []string{}
	seen := map[string]bool{}
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		candidates = append(candidates, model)
	}
	if agent, ok := raw["agent"].(map[string]any); ok {
		defaultModel := strings.TrimSpace(stringValue(agent["defaultModel"]))
		prefix, _ := splitModel(defaultModel)
		if defaultModel != "" && !isGoRuntimeUnsupportedProvider(prefix) {
			add(defaultModel)
		}
	}
	appendProviderCandidates(add, raw, "openaiProviders", "openai", false)
	appendProviderCandidates(add, raw, "anthropicProviders", "anthropic", false)
	appendProviderCandidates(add, raw, "customApiProviders", "openai", true)
	appendProviderCandidates(add, raw, "clineProviders", "cline", false)
	appendProviderCandidates(add, raw, "nkpProviders", "nkp", false)
	appendProviderCandidates(add, raw, "nugProviders", "nug", false)
	add("openai:")
	add("anthropic:")
	return candidates
}

func appendProviderCandidates(add func(string), raw map[string]any, field, defaultPrefix string, customAPI bool) {
	for _, item := range readArray(raw[field]) {
		provider, ok := item.(map[string]any)
		if !ok || boolValue(provider["disabled"]) {
			continue
		}
		if customAPI && !isOpenAICompatibleCustomProtocol(stringValue(provider["protocol"])) {
			continue
		}
		prefix := strings.TrimSpace(stringValue(provider["prefix"]))
		if prefix == "" {
			prefix = defaultPrefix
		}
		model := strings.TrimSpace(stringValue(provider["defaultModel"]))
		if model == "" {
			if models := readArray(provider["enabledModels"]); len(models) > 0 {
				model = strings.TrimSpace(stringValue(models[0]))
			}
		}
		add(prefixedModelCandidate(prefix, model))
	}
}

func prefixedModelCandidate(prefix, model string) string {
	prefix = strings.TrimSpace(prefix)
	model = strings.TrimSpace(model)
	if prefix == "" {
		prefix = "openai"
	}
	if model == "" {
		return prefix + ":"
	}
	if strings.Contains(model, ":") {
		return model
	}
	return prefix + ":" + model
}

func isOpenAICompatibleCustomProtocol(protocol string) bool {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	return protocol == "" || strings.Contains(protocol, "openai") || strings.Contains(protocol, "completions") || strings.Contains(protocol, "responses") || strings.Contains(protocol, "codex")
}

func customOpenAIAPIMode(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch {
	case strings.Contains(protocol, "responses"):
		return "responses"
	case strings.Contains(protocol, "codex"):
		return "codex"
	default:
		return "completions"
	}
}
