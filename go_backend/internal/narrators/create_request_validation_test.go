package narrators

import (
	"strings"
	"testing"
)

func strptr(s string) *string { return &s }

// TestValidateCreateRequestMatchesTSCreateNarratorSchema locks the create-narrator
// input validation against the TypeScript createNarratorSchema (server/lib/validators):
// systemPrompt max 10000, cwd min1/max4096, reasoningEffort enum (nullable),
// planReflectionAutoApproveOverride / dangerReflectionOverride enums, and the STRICT
// permissionMode enum (default/acceptEdits/bypassPermissions/readOnly/dontAsk). The
// permissionMode case is the regression guard: TS uses permissionModeSchema (no legacy
// preprocess), so legacy aliases like "allowByDefault" and unknown values must 400
// rather than being silently coerced by Service.Create's lenient normalizer.
func TestValidateCreateRequestMatchesTSCreateNarratorSchema(t *testing.T) {
	cases := []struct {
		name    string
		req     createRequest
		wantErr string // "" means expect success
	}{
		{name: "empty request ok", req: createRequest{}},
		{name: "valid permissionMode", req: createRequest{PermissionMode: "acceptEdits"}},
		{name: "all valid", req: createRequest{
			SystemPrompt:                      strptr("hi"),
			Cwd:                               strptr("/tmp/x"),
			ReasoningEffort:                   strptr("high"),
			PermissionMode:                    "readOnly",
			PlanReflectionAutoApproveOverride: "on",
			DangerReflectionOverride:          "strict",
		}},
		{name: "systemPrompt too long", req: createRequest{SystemPrompt: strptr(strings.Repeat("x", 10001))}, wantErr: "systemPrompt must be at most 10000 characters"},
		{name: "cwd empty", req: createRequest{Cwd: strptr("")}, wantErr: "cwd must not be empty"},
		{name: "cwd too long", req: createRequest{Cwd: strptr(strings.Repeat("x", 4097))}, wantErr: "cwd must be at most 4096 characters"},
		{name: "bad reasoningEffort", req: createRequest{ReasoningEffort: strptr("ultra")}, wantErr: "reasoningEffort must be one of"},
		{name: "bad planReflection override", req: createRequest{PlanReflectionAutoApproveOverride: "maybe"}, wantErr: "planReflectionAutoApproveOverride must be inherit, on, or off"},
		{name: "bad dangerReflection override", req: createRequest{DangerReflectionOverride: "paranoid"}, wantErr: "dangerReflectionOverride must be"},
		// permissionMode strict enum: unknown and legacy aliases must reject (TS uses
		// permissionModeSchema, not the legacy preprocess schema).
		{name: "unknown permissionMode", req: createRequest{PermissionMode: "bogus"}, wantErr: "permissionMode must be one of"},
		{name: "legacy permissionMode allowByDefault rejected", req: createRequest{PermissionMode: "allowByDefault"}, wantErr: "permissionMode must be one of"},
		{name: "legacy permissionMode plan rejected", req: createRequest{PermissionMode: "plan"}, wantErr: "permissionMode must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCreateRequest(tc.req)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
