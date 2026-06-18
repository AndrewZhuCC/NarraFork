package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"narrafork-go/internal/httpx"
)

// resolveCapabilityDotPath walks a "a.b.c" dot-path through the nested health
// capabilities object and reports whether every segment resolves to a present
// key (the final value may be any JSON type, including false/null — presence is
// what the contract requires).
func resolveCapabilityDotPath(root map[string]any, path string) bool {
	parts := strings.Split(path, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		v, ok := m[p]
		if !ok {
			return false
		}
		cur = v
	}
	return true
}

// TestPhase0ContractMatrixRequiredCapabilityKeysResolveInHealthPayload enforces
// phase0-contract-matrix.json phaseExitCriteria #1 ("The Go health payload
// exposes all Phase 0 required capability keys"): every capability dot-path
// declared in apiRouteGroups[].requiredCapabilityKeys AND in
// wsEventGroups[].capabilityKey/capabilityKeys must resolve against the live
// GET /api/health capabilities object. Without this guard the matrix could drift
// from the actual payload (e.g. a key documented under content.* while the code
// and frontend read it at top level), leaving the exit criterion unverified.
func TestPhase0ContractMatrixRequiredCapabilityKeysResolveInHealthPayload(t *testing.T) {
	matrixPath := filepath.Join("..", "..", "phase0-contract-matrix.json")
	raw, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read contract matrix: %v", err)
	}
	var matrix struct {
		APIRouteGroups []struct {
			Group                  string   `json:"group"`
			RequiredCapabilityKeys []string `json:"requiredCapabilityKeys"`
		} `json:"apiRouteGroups"`
		WSEventGroups []struct {
			Group          string   `json:"group"`
			CapabilityKey  string   `json:"capabilityKey"`
			CapabilityKeys []string `json:"capabilityKeys"`
		} `json:"wsEventGroups"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode contract matrix: %v", err)
	}

	// group-by key so a failure points at the owning group. Both HTTP route
	// groups (requiredCapabilityKeys) and WS event groups (capabilityKey/
	// capabilityKeys) declare health capability dot-paths that must resolve.
	keyOwner := map[string]string{}
	addKey := func(key, group string) {
		if key == "" {
			return
		}
		// Some groups carry a human-readable composite capabilityKey label
		// (e.g. "terminal + terminal.replacementContract") that is not a single
		// resolvable dot-path; the precise paths live in capabilityKeys. Skip
		// composite labels (whitespace or "+") and only enforce real dot-paths.
		if strings.ContainsAny(key, " +") {
			return
		}
		if _, exists := keyOwner[key]; !exists {
			keyOwner[key] = group
		}
	}
	for _, g := range matrix.APIRouteGroups {
		for _, k := range g.RequiredCapabilityKeys {
			addKey(k, g.Group)
		}
	}
	for _, g := range matrix.WSEventGroups {
		addKey(g.CapabilityKey, g.Group)
		for _, k := range g.CapabilityKeys {
			addKey(k, g.Group)
		}
	}
	if len(keyOwner) == 0 {
		t.Fatal("contract matrix declared no capability keys; guard would be vacuous")
	}

	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}

	var missing []string
	for key, group := range keyOwner {
		if !resolveCapabilityDotPath(caps, key) {
			missing = append(missing, key+"  [group="+group+"]")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d/%d matrix capability keys do not resolve in the health payload:\n%s",
			len(missing), len(keyOwner), strings.Join(missing, "\n"))
	}
}

// walkFallbackReasonInvariant descends the health capabilities tree and records
// the dot-paths of every object node that advertises a disabled/degraded state
// (fallback:true or supported:false) but exposes neither a static "reason"
// string nor the "reasonErrorMessage":true convention flag (which signals the
// human-readable reason is delivered in the route-level error payload instead of
// statically in health metadata).
func walkFallbackReasonInvariant(node any, path string, violations *[]string) {
	switch v := node.(type) {
	case map[string]any:
		fb, hasFB := v["fallback"]
		sup, hasSup := v["supported"]
		advertised := (hasFB && fb == true) || (hasSup && sup == false)
		if advertised {
			_, hasReason := v["reason"]
			rem, hasREM := v["reasonErrorMessage"]
			if !hasReason && !(hasREM && rem == true) {
				*violations = append(*violations, path)
			}
		}
		for k, child := range v {
			walkFallbackReasonInvariant(child, path+"."+k, violations)
		}
	case []any:
		for i, child := range v {
			walkFallbackReasonInvariant(child, fmt.Sprintf("%s[%d]", path, i), violations)
		}
	}
}

// TestPhase0HealthFallbackNodesAlwaysExposeReason enforces the matrix
// fallbackShapeScope ("health capability metadata must at minimum expose reason
// when fallback or unsupported is advertised"). Every capabilities node that
// sets fallback:true or supported:false must carry either a static "reason" or
// the "reasonErrorMessage":true convention flag. Without this guard a future
// capability could advertise a degraded state with no discoverable reason,
// silently violating the Phase 0 fallback-shape exit criterion.
func TestPhase0HealthFallbackNodesAlwaysExposeReason(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}

	var violations []string
	walkFallbackReasonInvariant(caps, "capabilities", &violations)
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("%d capability node(s) advertise fallback:true/supported:false without reason or reasonErrorMessage:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestPhase0FallbackShapeMatchesMatrixDefinition binds the canonical disabled-
// feature payload (httpx.FeatureDisabledPayloadWithCode, the single source used
// by every WriteFeatureDisabled* route helper) to the matrix-declared
// fallbackShape. phaseExitCriteria #2 requires disabled mutations to "return a
// stable fallback payload before side effects"; the matrix freezes that payload
// shape (ok/code/feature/supported/fallback/reason/error/message). Without this
// guard the matrix fallbackShape and the real payload could silently diverge
// (a field added to one but not the other), leaving the contract unverified.
func TestPhase0FallbackShapeMatchesMatrixDefinition(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "phase0-contract-matrix.json"))
	if err != nil {
		t.Fatalf("read contract matrix: %v", err)
	}
	var matrix struct {
		FallbackShape map[string]any `json:"fallbackShape"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode contract matrix: %v", err)
	}
	if len(matrix.FallbackShape) == 0 {
		t.Fatal("matrix fallbackShape is empty; guard would be vacuous")
	}

	const feature = "Provider authentication"
	payload := httpx.FeatureDisabledPayloadWithCode(feature, "FEATURE_DISABLED", "")

	// Every matrix-declared field must be present in the real payload.
	var missing []string
	for key := range matrix.FallbackShape {
		if _, ok := payload[key]; !ok {
			missing = append(missing, key)
		}
	}
	// And the payload must not introduce undocumented fields beyond the matrix.
	var extra []string
	for key := range payload {
		if _, ok := matrix.FallbackShape[key]; !ok {
			extra = append(extra, key)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		t.Fatalf("fallback payload keys diverge from matrix fallbackShape: missing=%v extra=%v", missing, extra)
	}

	// Stable literal values the matrix pins (non-"string" placeholders).
	if payload["ok"] != false {
		t.Fatalf("fallback ok should be false, got %#v", payload["ok"])
	}
	if payload["supported"] != false {
		t.Fatalf("fallback supported should be false, got %#v", payload["supported"])
	}
	if payload["fallback"] != true {
		t.Fatalf("fallback fallback should be true, got %#v", payload["fallback"])
	}
	if payload["code"] != "FEATURE_DISABLED" {
		t.Fatalf("fallback code should be FEATURE_DISABLED, got %#v", payload["code"])
	}
	if payload["feature"] != feature {
		t.Fatalf("fallback feature should be stable, got %#v", payload["feature"])
	}
	// reason/error/message must all be the same human-readable string.
	reason, _ := payload["reason"].(string)
	if reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("fallback reason/error/message must be a shared non-empty string, got %#v", payload)
	}
}

// healthStringSlice extracts a []string from a nested health capabilities path.
func healthStringSlice(t *testing.T, caps map[string]any, path ...string) []string {
	t.Helper()
	var cur any = caps
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("capabilities path %v: %q is not an object", path, p)
		}
		cur = m[p]
	}
	arr, ok := cur.([]any)
	if !ok {
		t.Fatalf("capabilities path %v is not an array, got %#v", path, cur)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("capabilities path %v has non-string element %#v", path, v)
		}
		out = append(out, s)
	}
	return out
}

// TestPhase0WSEventListsMatchMatrixPhaseEvents locks the two independent copies
// of the narrator WS event-name lists: the matrix wsEventGroups
// phase2RequiredEvents/phase4FollowupEvents and the live health payload
// narrator.wsEvents.p0.events / p1.events. These are hardcoded in two separate
// files (phase0-contract-matrix.json and runtime_domain_capabilities.go); without
// this guard an edit to one list would silently drift from the other, leaving the
// advertised WS contract inconsistent. Order is significant (both are ordered
// contract lists), so the comparison is order-sensitive.
func TestPhase0WSEventListsMatchMatrixPhaseEvents(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "phase0-contract-matrix.json"))
	if err != nil {
		t.Fatalf("read contract matrix: %v", err)
	}
	var matrix struct {
		WSEventGroups []struct {
			Group                string   `json:"group"`
			Phase2RequiredEvents []string `json:"phase2RequiredEvents"`
			Phase4FollowupEvents []string `json:"phase4FollowupEvents"`
		} `json:"wsEventGroups"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode contract matrix: %v", err)
	}
	var matrixP2, matrixP4 []string
	for _, g := range matrix.WSEventGroups {
		if len(g.Phase2RequiredEvents) > 0 {
			matrixP2 = g.Phase2RequiredEvents
		}
		if len(g.Phase4FollowupEvents) > 0 {
			matrixP4 = g.Phase4FollowupEvents
		}
	}
	if len(matrixP2) == 0 || len(matrixP4) == 0 {
		t.Fatal("matrix phase2/phase4 event lists are empty; guard would be vacuous")
	}

	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}

	healthP0 := healthStringSlice(t, caps, "narrator", "wsEvents", "p0", "events")
	healthP1 := healthStringSlice(t, caps, "narrator", "wsEvents", "p1", "events")

	if !reflect.DeepEqual(matrixP2, healthP0) {
		t.Fatalf("matrix phase2RequiredEvents != health narrator.wsEvents.p0.events\nmatrix=%v\nhealth=%v", matrixP2, healthP0)
	}
	if !reflect.DeepEqual(matrixP4, healthP1) {
		t.Fatalf("matrix phase4FollowupEvents != health narrator.wsEvents.p1.events\nmatrix=%v\nhealth=%v", matrixP4, healthP1)
	}
}

// TestPhase0TerminalWSListsMatchMatrix locks the terminal-ws message/event name
// lists, which (like the narrator WS phase lists, see
// TestPhase0WSEventListsMatchMatrixPhaseEvents) are hardcoded twice: the matrix
// wsEventGroups terminal-ws clientMessages/serverEvents/subscribeReplayEvents and
// the live health payload terminal.ws.{clientMessages,serverEvents,
// subscribeReplayEvents}. Without this guard an edit to one copy would silently
// diverge from the other. Order is significant.
func TestPhase0TerminalWSListsMatchMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "phase0-contract-matrix.json"))
	if err != nil {
		t.Fatalf("read contract matrix: %v", err)
	}
	var matrix struct {
		WSEventGroups []struct {
			Group                 string   `json:"group"`
			ClientMessages        []string `json:"clientMessages"`
			ServerEvents          []string `json:"serverEvents"`
			SubscribeReplayEvents []string `json:"subscribeReplayEvents"`
		} `json:"wsEventGroups"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode contract matrix: %v", err)
	}
	var clientMessages, serverEvents, subscribeReplay []string
	for _, g := range matrix.WSEventGroups {
		if g.Group == "terminal-ws" {
			clientMessages = g.ClientMessages
			serverEvents = g.ServerEvents
			subscribeReplay = g.SubscribeReplayEvents
		}
	}
	if len(clientMessages) == 0 || len(serverEvents) == 0 || len(subscribeReplay) == 0 {
		t.Fatal("matrix terminal-ws lists are empty; guard would be vacuous")
	}

	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}

	cases := []struct {
		name   string
		matrix []string
		health []string
	}{
		{"clientMessages", clientMessages, healthStringSlice(t, caps, "terminal", "ws", "clientMessages")},
		{"serverEvents", serverEvents, healthStringSlice(t, caps, "terminal", "ws", "serverEvents")},
		{"subscribeReplayEvents", subscribeReplay, healthStringSlice(t, caps, "terminal", "ws", "subscribeReplayEvents")},
	}
	for _, c := range cases {
		if !reflect.DeepEqual(c.matrix, c.health) {
			t.Fatalf("matrix terminal-ws %s != health terminal.ws.%s\nmatrix=%v\nhealth=%v", c.name, c.name, c.matrix, c.health)
		}
	}
}
