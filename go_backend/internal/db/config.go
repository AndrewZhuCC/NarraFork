package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Engine string

const (
	EngineSQLite   Engine = "sqlite"
	EnginePostgres Engine = "postgres"
)

type SQLiteBootstrapMode string

const (
	SQLiteBootstrapModeBunFinalCompatible SQLiteBootstrapMode = "bun-final-compatible"
	SQLiteBootstrapModeReadOnly           SQLiteBootstrapMode = "read-only-ts-owned"
)

const (
	defaultSQLiteFileName          = "narrafork.db"
	defaultPostgresMaxOpenConns    = 10
	defaultPostgresMaxIdleConns    = 5
	defaultPostgresConnMaxLifetime = 30 * time.Minute
)

type ConfigInput struct {
	Driver                 string
	SQLitePath             string
	PostgresDSN            string
	PostgresSchema         string
	SQLiteBootstrapMode    string
	MaxOpenConns           int
	MaxIdleConns           int
	ConnMaxLifetimeSeconds int
	VerifyCanonical        *bool
	TSOwnedSchema          *bool
}

type Config struct {
	Engine              Engine
	SQLitePath          string
	PostgresDSN         string
	PostgresSchema      string
	SQLiteBootstrapMode SQLiteBootstrapMode
	MaxOpenConns        int
	MaxIdleConns        int
	ConnMaxLifetime     time.Duration
	VerifyCanonical     bool
	TSOwnedSchema       bool
}

func NewConfig(input ConfigInput) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolve home directory: %w", err)
	}
	cfg := defaultConfig(home)
	cfg.applyInput(input)
	cfg.applyEnv()
	return cfg.normalized()
}

func defaultConfig(home string) Config {
	return Config{
		Engine:              EngineSQLite,
		SQLitePath:          filepath.Join(home, ".narrafork", defaultSQLiteFileName),
		SQLiteBootstrapMode: SQLiteBootstrapModeBunFinalCompatible,
		MaxOpenConns:        1,
		MaxIdleConns:        1,
		VerifyCanonical:     true,
		TSOwnedSchema:       true,
	}
}

func (c *Config) applyInput(input ConfigInput) {
	if strings.TrimSpace(input.Driver) != "" {
		c.Engine = Engine(strings.TrimSpace(input.Driver))
	}
	if strings.TrimSpace(input.SQLitePath) != "" {
		c.SQLitePath = strings.TrimSpace(input.SQLitePath)
	}
	if strings.TrimSpace(input.PostgresDSN) != "" {
		c.PostgresDSN = strings.TrimSpace(input.PostgresDSN)
	}
	if strings.TrimSpace(input.PostgresSchema) != "" {
		c.PostgresSchema = strings.TrimSpace(input.PostgresSchema)
	}
	if strings.TrimSpace(input.SQLiteBootstrapMode) != "" {
		c.SQLiteBootstrapMode = SQLiteBootstrapMode(strings.TrimSpace(input.SQLiteBootstrapMode))
	}
	if input.MaxOpenConns > 0 {
		c.MaxOpenConns = input.MaxOpenConns
	}
	if input.MaxIdleConns > 0 {
		c.MaxIdleConns = input.MaxIdleConns
	}
	if input.ConnMaxLifetimeSeconds > 0 {
		c.ConnMaxLifetime = time.Duration(input.ConnMaxLifetimeSeconds) * time.Second
	}
	if input.VerifyCanonical != nil {
		c.VerifyCanonical = *input.VerifyCanonical
	}
	if input.TSOwnedSchema != nil {
		c.TSOwnedSchema = *input.TSOwnedSchema
	}
	if c.Engine == "" {
		c.Engine = EngineSQLite
	}
}

func (c *Config) applyEnv() {
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_DB_DRIVER")); value != "" {
		c.Engine = Engine(value)
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_SQLITE_PATH")); value != "" {
		c.SQLitePath = value
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_SQLITE_BOOTSTRAP_MODE")); value != "" {
		c.SQLiteBootstrapMode = SQLiteBootstrapMode(value)
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_DATABASE_URL")); value != "" {
		c.PostgresDSN = value
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_POSTGRES_DSN")); value != "" {
		c.PostgresDSN = value
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_POSTGRES_SCHEMA")); value != "" {
		c.PostgresSchema = value
	}
	if value, ok := envInt("NARRAFORK_DB_MAX_OPEN_CONNS"); ok {
		c.MaxOpenConns = value
	}
	if value, ok := envInt("NARRAFORK_DB_MAX_IDLE_CONNS"); ok {
		c.MaxIdleConns = value
	}
	if value, ok := envInt("NARRAFORK_DB_CONN_MAX_LIFETIME_SECONDS"); ok {
		c.ConnMaxLifetime = time.Duration(value) * time.Second
	}
	if value, ok := envBool("NARRAFORK_DB_VERIFY_CANONICAL"); ok {
		c.VerifyCanonical = value
	}
	if value, ok := envBool("NARRAFORK_DB_TS_OWNED_SCHEMA"); ok {
		c.TSOwnedSchema = value
	}
}

func (c Config) normalized() (Config, error) {
	switch c.Engine {
	case "", EngineSQLite:
		c.Engine = EngineSQLite
		if strings.TrimSpace(c.SQLitePath) == "" {
			return Config{}, fmt.Errorf("sqlite database path is required")
		}
		if !filepath.IsAbs(c.SQLitePath) {
			abs, err := filepath.Abs(c.SQLitePath)
			if err != nil {
				return Config{}, fmt.Errorf("resolve sqlite database path: %w", err)
			}
			c.SQLitePath = abs
		}
		if c.MaxOpenConns <= 0 {
			c.MaxOpenConns = 1
		}
		if c.MaxIdleConns <= 0 {
			c.MaxIdleConns = 1
		}
		if c.SQLiteBootstrapMode == "" {
			c.SQLiteBootstrapMode = SQLiteBootstrapModeBunFinalCompatible
		}
		switch c.SQLiteBootstrapMode {
		case SQLiteBootstrapModeBunFinalCompatible, SQLiteBootstrapModeReadOnly:
		default:
			return Config{}, fmt.Errorf("unsupported sqlite bootstrap mode %q", c.SQLiteBootstrapMode)
		}
		c.ConnMaxLifetime = 0
	case EnginePostgres:
		c.PostgresSchema = strings.TrimSpace(c.PostgresSchema)
		if strings.TrimSpace(c.PostgresDSN) == "" {
			return Config{}, fmt.Errorf("postgres DSN is required when database driver is postgres")
		}
		if c.MaxOpenConns <= 0 {
			c.MaxOpenConns = defaultPostgresMaxOpenConns
		}
		if c.MaxIdleConns <= 0 {
			c.MaxIdleConns = defaultPostgresMaxIdleConns
		}
		if c.ConnMaxLifetime <= 0 {
			c.ConnMaxLifetime = defaultPostgresConnMaxLifetime
		}
		if c.TSOwnedSchema {
			// PostgreSQL is intentionally Go-owned; TS-owned canonical checks only apply to SQLite.
			c.TSOwnedSchema = false
		}
		c.VerifyCanonical = false
		c.SQLiteBootstrapMode = ""
	default:
		return Config{}, fmt.Errorf("unsupported database driver %q", c.Engine)
	}
	return c, nil
}

func (c Config) DataSourceName() string {
	if c.Engine == EnginePostgres {
		return postgresDataSourceName(c.PostgresDSN, c.PostgresSchema)
	}
	return c.SQLitePath
}

func (c Config) DisplayName() string {
	if c.Engine == EnginePostgres {
		return "postgres"
	}
	return c.SQLitePath
}

func envInt(name string) (int, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func Bool(value bool) *bool {
	return &value
}

func envBool(name string) (bool, bool) {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return false, false
	}
	switch value {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}
