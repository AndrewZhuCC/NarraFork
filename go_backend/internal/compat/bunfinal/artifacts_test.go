package bunfinal

import (
	"encoding/json"
	"testing"
)

func TestManifestReferencesAllBunFinalSurfaceArtifacts(t *testing.T) {
	manifest, err := ReadManifest()
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}

	artifacts := map[string]Artifact{
		"sqlite schema snapshot":      manifest.Artifacts.SQLiteSchemaSnapshot,
		"drizzle journal snapshot":    manifest.Artifacts.DrizzleJournalSnapshot,
		"canonical schema contract":   manifest.Artifacts.CanonicalSchemaContract,
		"roundtrip fixture":           manifest.Artifacts.RoundtripFixture,
		"http smoke golden":           manifest.Artifacts.HTTPSmokeGolden,
		"narrator WS event inventory": manifest.Artifacts.NarratorWSEventInventory,
		"settings roundtrip cases":    manifest.Artifacts.SettingsRoundtripCases,
		"search roundtrip cases":      manifest.Artifacts.SearchRoundtripCases,
		"FTS schema contract":         manifest.Artifacts.FTSSchemaContract,
	}
	for name, artifact := range artifacts {
		if _, err := ReadArtifact(artifact.Path); err != nil {
			t.Fatalf("read %s artifact: %v", name, err)
		}
	}
}

func TestBunFinalSurfaceArtifactsHaveMinimumCoverage(t *testing.T) {
	assertArtifactCaseCount(t, "roundtrip fixture", ReadRoundtripFixture, "rows", 7)
	assertArtifactCaseCount(t, "http smoke golden", ReadHTTPSmokeGolden, "cases", 6)
	assertArtifactCaseCount(t, "settings roundtrip cases", ReadSettingsRoundtripCases, "cases", 5)
	assertArtifactCaseCount(t, "search roundtrip cases", ReadSearchRoundtripCases, "cases", 4)
	assertArtifactCaseCount(t, "FTS schema contract tables", ReadFTSSchemaContract, "tables", 3)
	assertArtifactCaseCount(t, "FTS schema contract triggers", ReadFTSSchemaContract, "triggers", 9)

	data, err := ReadNarratorWSEventInventory()
	if err != nil {
		t.Fatalf("read narrator WS event inventory: %v", err)
	}
	var inventory struct {
		Events []struct {
			Type           string   `json:"type"`
			RequiredFields []string `json:"requiredFields"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("parse narrator WS event inventory: %v", err)
	}
	if len(inventory.Events) < 15 {
		t.Fatalf("narrator WS event inventory has %d events, want at least 15", len(inventory.Events))
	}
	seen := map[string]struct{}{}
	for _, event := range inventory.Events {
		if event.Type == "" {
			t.Fatal("narrator WS event inventory contains event with empty type")
		}
		if len(event.RequiredFields) == 0 {
			t.Fatalf("narrator WS event %q has no required fields", event.Type)
		}
		if _, duplicate := seen[event.Type]; duplicate {
			t.Fatalf("duplicate narrator WS event type %q", event.Type)
		}
		seen[event.Type] = struct{}{}
	}
}

func assertArtifactCaseCount(
	t *testing.T,
	name string,
	read func() ([]byte, error),
	field string,
	minimum int,
) {
	t.Helper()
	data, err := read()
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var artifact map[string]any
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	rawCases, ok := artifact[field].([]any)
	if !ok {
		t.Fatalf("%s missing array field %q", name, field)
	}
	if len(rawCases) < minimum {
		t.Fatalf("%s has %d cases, want at least %d", name, len(rawCases), minimum)
	}
}
