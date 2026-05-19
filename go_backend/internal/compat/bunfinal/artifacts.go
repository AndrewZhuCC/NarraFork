package bunfinal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ArtifactVersion = 1
	artifactRootRel = "go_backend/compat/bun-final"
	manifestRel     = artifactRootRel + "/manifest.json"
)

type Manifest struct {
	ArtifactVersion int               `json:"artifactVersion"`
	Source          string            `json:"source"`
	DrizzleLastTag  string            `json:"drizzleLastTag"`
	Artifacts       ManifestArtifacts `json:"artifacts"`
}

type ManifestArtifacts struct {
	SQLiteSchemaSnapshot     Artifact `json:"sqliteSchemaSnapshot"`
	DrizzleJournalSnapshot   Artifact `json:"drizzleJournalSnapshot"`
	CanonicalSchemaContract  Artifact `json:"canonicalSchemaContract"`
	RoundtripFixture         Artifact `json:"roundtripFixture"`
	HTTPSmokeGolden          Artifact `json:"httpSmokeGolden"`
	NarratorWSEventInventory Artifact `json:"narratorWSEventInventory"`
	SettingsRoundtripCases   Artifact `json:"settingsRoundtripCases"`
	SearchRoundtripCases     Artifact `json:"searchRoundtripCases"`
	FTSSchemaContract        Artifact `json:"ftsSchemaContract"`
}

type Artifact struct {
	Path       string `json:"path"`
	SHA256Path string `json:"sha256Path"`
	SchemaHash string `json:"schemaHash,omitempty"`
}

func RepositoryRoot() (string, error) {
	cwd, err := os.Getwd()
	if err == nil {
		root, err := LocateRepositoryRoot(cwd)
		if err == nil {
			return root, nil
		}
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate repository root: runtime caller unavailable")
	}
	return LocateRepositoryRoot(filepath.Dir(file))
}

func LocateRepositoryRoot(start string) (string, error) {
	if start == "" {
		return "", fmt.Errorf("locate repository root: empty start path")
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve repository root start path: %w", err)
	}
	for {
		manifestPath := filepath.Join(dir, filepath.FromSlash(manifestRel))
		if info, err := os.Stat(manifestPath); err == nil && !info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("locate repository root from %s: %s not found", start, manifestRel)
		}
		dir = parent
	}
}

func ArtifactRoot() (string, error) {
	root, err := RepositoryRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(artifactRootRel)), nil
}

func ReadManifest() (Manifest, error) {
	root, err := RepositoryRoot()
	if err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifestRel)))
	if err != nil {
		return Manifest{}, fmt.Errorf("read bun-final manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse bun-final manifest: %w", err)
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.ArtifactVersion != ArtifactVersion {
		return fmt.Errorf("bun-final manifest artifactVersion = %d, want %d", manifest.ArtifactVersion, ArtifactVersion)
	}
	if strings.TrimSpace(manifest.DrizzleLastTag) == "" {
		return fmt.Errorf("bun-final manifest drizzleLastTag is empty")
	}
	if strings.TrimSpace(manifest.Artifacts.SQLiteSchemaSnapshot.Path) == "" {
		return fmt.Errorf("bun-final manifest sqlite schema snapshot path is empty")
	}
	if strings.TrimSpace(manifest.Artifacts.SQLiteSchemaSnapshot.SHA256Path) == "" {
		return fmt.Errorf("bun-final manifest sqlite schema snapshot sha256Path is empty")
	}
	if strings.TrimSpace(manifest.Artifacts.DrizzleJournalSnapshot.Path) == "" {
		return fmt.Errorf("bun-final manifest drizzle journal snapshot path is empty")
	}
	if strings.TrimSpace(manifest.Artifacts.CanonicalSchemaContract.Path) == "" {
		return fmt.Errorf("bun-final manifest canonical schema contract path is empty")
	}
	for name, artifact := range map[string]Artifact{
		"roundtrip fixture":           manifest.Artifacts.RoundtripFixture,
		"http smoke golden":           manifest.Artifacts.HTTPSmokeGolden,
		"narrator WS event inventory": manifest.Artifacts.NarratorWSEventInventory,
		"settings roundtrip cases":    manifest.Artifacts.SettingsRoundtripCases,
		"search roundtrip cases":      manifest.Artifacts.SearchRoundtripCases,
		"FTS schema contract":         manifest.Artifacts.FTSSchemaContract,
	} {
		if strings.TrimSpace(artifact.Path) == "" {
			return fmt.Errorf("bun-final manifest %s path is empty", name)
		}
	}
	return nil
}

func ReadSQLiteSchemaSnapshot() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.SQLiteSchemaSnapshot.Path)
}

func ReadSQLiteSchemaSnapshotHash() (string, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return "", err
	}
	if err := ValidateManifest(manifest); err != nil {
		return "", err
	}
	data, err := ReadArtifact(manifest.Artifacts.SQLiteSchemaSnapshot.SHA256Path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func ReadDrizzleJournalSnapshot() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.DrizzleJournalSnapshot.Path)
}

func ReadCanonicalSchemaContract() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.CanonicalSchemaContract.Path)
}

func ReadRoundtripFixture() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.RoundtripFixture.Path)
}

func ReadHTTPSmokeGolden() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.HTTPSmokeGolden.Path)
}

func ReadNarratorWSEventInventory() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.NarratorWSEventInventory.Path)
}

func ReadSettingsRoundtripCases() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.SettingsRoundtripCases.Path)
}

func ReadSearchRoundtripCases() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.SearchRoundtripCases.Path)
}

func ReadFTSSchemaContract() ([]byte, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	return ReadArtifact(manifest.Artifacts.FTSSchemaContract.Path)
}

func ReadArtifact(relPath string) ([]byte, error) {
	path, err := ArtifactPath(relPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bun-final artifact %s: %w", relPath, err)
	}
	return data, nil
}

func ArtifactPath(relPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid bun-final artifact path %q", relPath)
	}
	root, err := ArtifactRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("validate bun-final artifact path %q: %w", relPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid bun-final artifact path %q", relPath)
	}
	return path, nil
}
