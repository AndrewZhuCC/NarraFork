package agentbasic

import "testing"

func findToolSpec(specs []toolSpec, name string) (toolSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return toolSpec{}, false
}

func TestExitPlanModeInlineToggle(t *testing.T) {
	// Inline allowed (default): ExitPlanMode keeps the plan parameter.
	allowed := resolveToolSpecs(ToolInventoryOptions{Scope: ToolScopeNormal})
	spec, ok := findToolSpec(allowed, "ExitPlanMode")
	if !ok {
		t.Fatalf("ExitPlanMode missing from normal inventory")
	}
	props, _ := spec.Parameters["properties"].(map[string]any)
	if _, hasPlan := props["plan"]; !hasPlan {
		t.Fatalf("expected inline plan param when inline is allowed: %#v", props)
	}
	if _, hasAllowed := props["allowedPrompts"]; !hasAllowed {
		t.Fatalf("expected allowedPrompts param to remain: %#v", props)
	}

	// Inline disabled: ExitPlanMode drops the plan parameter and switches description.
	disabled := resolveToolSpecs(ToolInventoryOptions{Scope: ToolScopeNormal, DisableInlinePlan: true})
	spec2, ok := findToolSpec(disabled, "ExitPlanMode")
	if !ok {
		t.Fatalf("ExitPlanMode missing from inline-disabled inventory")
	}
	props2, _ := spec2.Parameters["properties"].(map[string]any)
	if _, hasPlan := props2["plan"]; hasPlan {
		t.Fatalf("inline plan param should be removed when inline is disabled: %#v", props2)
	}
	if _, hasAllowed := props2["allowedPrompts"]; !hasAllowed {
		t.Fatalf("allowedPrompts param should remain when inline is disabled: %#v", props2)
	}
	if spec2.Description == spec.Description {
		t.Fatalf("expected description to change when inline is disabled")
	}

	// The shared toolSpecs() source must not be mutated by the rewrite.
	again := resolveToolSpecs(ToolInventoryOptions{Scope: ToolScopeNormal})
	specAgain, _ := findToolSpec(again, "ExitPlanMode")
	propsAgain, _ := specAgain.Parameters["properties"].(map[string]any)
	if _, hasPlan := propsAgain["plan"]; !hasPlan {
		t.Fatalf("inline-disabled rewrite leaked into shared spec source: %#v", propsAgain)
	}
}
