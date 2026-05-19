package main

import (
	"os"
	"strings"
	"testing"

	"narrafork-go/internal/config"
)

func TestSQLiteToPostgresCutoverFlags(t *testing.T) {
	if err := validateSQLiteToPostgresCutoverFlags(false, true, true); err != nil {
		t.Fatalf("valid cutover flags returned error: %v", err)
	}
	if err := validateSQLiteToPostgresCutoverFlags(true, true, true); err == nil || !strings.Contains(err.Error(), "--dry-run") {
		t.Fatalf("expected dry-run cutover error, got %v", err)
	}
	if err := validateSQLiteToPostgresCutoverFlags(false, false, true); err == nil || !strings.Contains(err.Error(), "--verify") {
		t.Fatalf("expected verify cutover error, got %v", err)
	}
}

func TestWriteSQLiteToPostgresCutoverSettingsBacksUpAndPatches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	backupPath, err := writeSQLiteToPostgresCutoverSettings(settings, "/tmp/source.db", "postgres://example/db", "narrafork_app")
	if err != nil {
		t.Fatalf("write cutover settings: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected settings backup at %s: %v", backupPath, err)
	}
	core := settings.Core()
	if core.Database.Driver != "postgres" || core.Database.PostgresDSN != "postgres://example/db" || core.Database.PostgresSchema != "narrafork_app" || core.Database.SQLitePath != "/tmp/source.db" {
		t.Fatalf("unexpected cutover database settings: %#v", core.Database)
	}
	raw := settings.Raw()
	database, ok := raw["database"].(map[string]any)
	if !ok {
		t.Fatalf("settings raw missing database map: %#v", raw)
	}
	migrated, ok := database["migratedFromSqlite"].(map[string]any)
	if !ok || migrated["path"] != "/tmp/source.db" || migrated["verifiedAt"] == "" {
		t.Fatalf("unexpected migratedFromSqlite settings: %#v", database["migratedFromSqlite"])
	}
}

func TestSQLiteToPostgresCutoverPatch(t *testing.T) {
	patch := sqliteToPostgresCutoverPatch("/tmp/source.db", "postgres://example/db", " narrafork_app ")
	database, ok := patch["database"].(map[string]any)
	if !ok {
		t.Fatalf("patch missing database map: %#v", patch)
	}
	if database["driver"] != "postgres" || database["postgresDsn"] != "postgres://example/db" || database["postgresSchema"] != "narrafork_app" || database["sqlitePath"] != "/tmp/source.db" {
		t.Fatalf("unexpected database patch: %#v", database)
	}
	migrated, ok := database["migratedFromSqlite"].(map[string]any)
	if !ok || migrated["path"] != "/tmp/source.db" || migrated["verifiedAt"] == "" {
		t.Fatalf("unexpected migratedFromSqlite patch: %#v", database["migratedFromSqlite"])
	}
}

func TestTLSListenConfig(t *testing.T) {
	var core config.Core
	core.Server.TLS.Enabled = true
	core.Server.TLS.CertFile = " /tmp/cert.pem "
	core.Server.TLS.KeyFile = " /tmp/key.pem "

	enabled, certFile, keyFile := tlsListenConfig(core)
	if !enabled {
		t.Fatal("expected TLS to be enabled when enabled flag and cert/key files are present")
	}
	if certFile != "/tmp/cert.pem" || keyFile != "/tmp/key.pem" {
		t.Fatalf("expected trimmed cert/key paths, got cert=%q key=%q", certFile, keyFile)
	}
}

func TestTLSListenConfigRequiresEnabledAndFiles(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*config.Core)
	}{
		{
			name: "disabled with files",
			configure: func(core *config.Core) {
				core.Server.TLS.CertFile = "/tmp/cert.pem"
				core.Server.TLS.KeyFile = "/tmp/key.pem"
			},
		},
		{
			name: "missing cert",
			configure: func(core *config.Core) {
				core.Server.TLS.Enabled = true
				core.Server.TLS.KeyFile = "/tmp/key.pem"
			},
		},
		{
			name: "missing key",
			configure: func(core *config.Core) {
				core.Server.TLS.Enabled = true
				core.Server.TLS.CertFile = "/tmp/cert.pem"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var core config.Core
			tc.configure(&core)
			enabled, _, _ := tlsListenConfig(core)
			if enabled {
				t.Fatal("expected TLS to be disabled")
			}
		})
	}
}
