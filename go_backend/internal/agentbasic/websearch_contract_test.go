package agentbasic

import (
	"testing"
)

// TestWebSearchCapabilityContractDocumentsBackendDivergence pins the WebSearch
// capability contract that documents the honest backend divergence: Go uses a
// standalone DuckDuckGo Instant Answer query, while TypeScript routes WebSearch
// through a provider-backed MCP search chain (kiro -> nug -> nkp). Previously
// WebSearch fell through to the generic default contract with no divergence note.
func TestWebSearchCapabilityContractDocumentsBackendDivergence(t *testing.T) {
	metadata := ToolInventoryMetadataForOptions(ToolInventoryOptions{Scope: ToolScopeNormal})
	ws := metadataForTool(t, metadata, "WebSearch")
	assertContractFields(t, ws)

	if ws["parity"] != "partial" || ws["runtimeCompatible"] != "partial" {
		t.Fatalf("WebSearch should be partial parity: %#v", ws)
	}
	if ws["searchBackend"] != "duckduckgo-instant-answer" {
		t.Fatalf("WebSearch should document the duckduckgo backend: %#v", ws)
	}
	if ws["providerMcpSearch"] != false {
		t.Fatalf("WebSearch should mark provider MCP search as unavailable: %#v", ws)
	}
	if ws["fallbackBehavior"] != "standalone_duckduckgo" {
		t.Fatalf("WebSearch fallbackBehavior should be standalone_duckduckgo: %#v", ws)
	}
	if !stringSliceHas(ws["knownDegraded"], "provider_mcp_search") {
		t.Fatalf("WebSearch should mark provider_mcp_search as degraded: %#v", ws)
	}
	reason, _ := ws["reason"].(string)
	if reason == "" {
		t.Fatalf("WebSearch contract should carry a divergence reason: %#v", ws)
	}
}
