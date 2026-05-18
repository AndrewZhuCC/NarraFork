package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var forbiddenMainSchemaSQL = regexp.MustCompile(`(?is)\b(?:CREATE\s+(?:TEMP(?:ORARY)?\s+)?(?:TABLE|INDEX|UNIQUE\s+INDEX|VIRTUAL\s+TABLE|TRIGGER)|ALTER\s+TABLE|DROP\s+(?:TABLE|INDEX|TRIGGER)|PRAGMA\s+user_version)\b|\bINSERT\s+INTO\b[\s\S]*\bVALUES\s*\(\s*['"]rebuild['"]\s*\)|\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+__drizzle_migrations\b|-->\s*statement-breakpoint`)

var allowedProjectBackupSchemaFunctions = map[string]struct{}{
	"ensureProjectBackupSchema":  {},
	"ensureProjectBackupColumns": {},
}

const allowedGoMigrationJournalFunction = "createGoMigrationJournal"

const allowedGoMigrationJournalCreateSQL = `CREATE TABLE IF NOT EXISTS __narrafork_go_migration_journal (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		contract_version INTEGER NOT NULL,
		expected_count INTEGER NOT NULL,
		expected_last_tag TEXT NOT NULL,
		expected_last_when INTEGER NOT NULL,
		expected_journal_hash TEXT NOT NULL,
		canonical_schema_hash TEXT NOT NULL,
		status TEXT NOT NULL,
		details_json TEXT NOT NULL,
		verified_at TEXT NOT NULL
	)`

type schemaMutationViolation struct {
	path string
	line int
	sql  string
}

func TestGoBackendDoesNotMutateMainDatabaseSchema(t *testing.T) {
	root := filepath.Clean(filepath.Join(packageDir(t), "..", ".."))
	violations, err := findMainSchemaMutationViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		rel, _ := filepath.Rel(root, violation.path)
		t.Errorf("forbidden main DB schema mutation SQL in %s:%d: %q", rel, violation.line, violation.sql)
	}
}

func TestSchemaMutationGuardDocumentsAllowedExceptions(t *testing.T) {
	allowedBackupFile := filepath.Clean("/repo/internal/projects/service.go")
	allowedMigrationFile := filepath.Clean("/repo/internal/db/migration_compat.go")
	testCases := []struct {
		name      string
		path      string
		source    string
		wantCount int
	}{
		{
			name: "blocks direct production create table",
			path: "/repo/internal/db/schema.go",
			source: `package db
func ensure(db executor) {
	_, _ = db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY)")
}`,
			wantCount: 1,
		},
		{
			name: "blocks production create index",
			path: "/repo/internal/db/schema.go",
			source: `package db
func ensure(db executor) {
	_, _ = db.Exec("CREATE INDEX idx_projects_name ON projects(name)")
}`,
			wantCount: 1,
		},
		{
			name: "blocks split production alter table",
			path: "/repo/internal/db/schema.go",
			source: `package db
func ensure(db executor, table string) {
	stmt := "ALTER " + "TABLE " + table + " ADD COLUMN name TEXT"
	_, _ = db.Exec(stmt)
}`,
			wantCount: 1,
		},
		{
			name: "blocks production drop table",
			path: "/repo/internal/db/schema.go",
			source: `package db
func ensure(db executor) {
	_, _ = db.Exec("DROP TABLE IF EXISTS chapters_fts")
}`,
			wantCount: 1,
		},
		{
			name: "blocks production fts rebuild",
			path: "/repo/internal/db/fts.go",
			source: `package db
func rebuild(db executor) {
	_, _ = db.Exec("INSERT INTO chapters_fts(chapters_fts) VALUES ('rebuild')")
}`,
			wantCount: 1,
		},
		{
			name: "blocks production writes to Drizzle migrations journal",
			path: "/repo/internal/db/migrate.go",
			source: `package db
func stamp(db executor) {
	_, _ = db.Exec("INSERT INTO __drizzle_migrations (hash, created_at) VALUES ('x', 1)")
}`,
			wantCount: 1,
		},
		{
			name: "blocks embedded Drizzle migration SQL content",
			path: "/repo/internal/db/migrations.go",
			source: `package db
const migrationSQL = "CREATE TABLE projects (id TEXT PRIMARY KEY); --> statement-breakpoint ALTER TABLE projects ADD COLUMN name TEXT"
`,
			wantCount: 3,
		},
		{
			name: "allows test memory schema fixtures",
			path: "/repo/internal/db/schema_guard_test.go",
			source: `package db
func TestFixture(t *testing.T) {
	_, _ = sqlDB.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY)")
}`,
			wantCount: 0,
		},
		{
			name: "allows project backup db schema maintenance only in documented functions",
			path: allowedBackupFile,
			source: `package projects
func ensureProjectBackupSchema(db executor) {
	_, _ = db.Exec("CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY)")
}
func ensureProjectBackupColumns(db executor, table string) {
	stmt := "ALTER " + "TABLE " + table + " ADD COLUMN name TEXT"
	_, _ = db.Exec(stmt)
}`,
			wantCount: 0,
		},
		{
			name:      "allows exact Go migration journal table in documented function",
			path:      allowedMigrationFile,
			source:    "package db\nfunc createGoMigrationJournal(db executor) {\n\t_, _ = db.Exec(" + strconv.Quote(allowedGoMigrationJournalCreateSQL) + ")\n}",
			wantCount: 0,
		},
		{
			name:      "blocks Go migration journal table outside documented function",
			path:      allowedMigrationFile,
			source:    "package db\nfunc createSomethingElse(db executor) {\n\t_, _ = db.Exec(" + strconv.Quote(allowedGoMigrationJournalCreateSQL) + ")\n}",
			wantCount: 1,
		},
		{
			name: "blocks other table in Go migration journal function",
			path: allowedMigrationFile,
			source: `package db
func createGoMigrationJournal(db executor) {
	_, _ = db.Exec("CREATE TABLE IF NOT EXISTS projects (id TEXT PRIMARY KEY)")
}`,
			wantCount: 1,
		},
		{
			name: "blocks project backup file mutations outside documented functions",
			path: allowedBackupFile,
			source: `package projects
func migrateMainDB(db executor) {
	_, _ = db.Exec("CREATE TABLE main_only (id TEXT PRIMARY KEY)")
}`,
			wantCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := schemaMutationViolationsInSource(
				tc.path,
				[]byte(tc.source),
				allowedBackupFile,
				allowedMigrationFile,
			)

			if err != nil {
				t.Fatalf("scan source: %v", err)
			}
			if len(violations) != tc.wantCount {
				t.Fatalf("expected %d violations, got %d: %#v", tc.wantCount, len(violations), violations)
			}
		})
	}
}

func findMainSchemaMutationViolations(root string) ([]schemaMutationViolation, error) {
	allowedBackupFile := filepath.Join(root, "internal", "projects", "service.go")
	allowedMigrationFile := filepath.Join(root, "internal", "db", "migration_compat.go")
	violations := []schemaMutationViolation{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "narrafork-go-check" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fileViolations, err := schemaMutationViolationsInSource(
			path,
			data,
			allowedBackupFile,
			allowedMigrationFile,
		)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	return violations, err
}

func schemaMutationViolationsInSource(
	path string,
	source []byte,
	allowedBackupFile string,
	allowedMigrationFile string,
) ([]schemaMutationViolation, error) {
	if strings.HasSuffix(path, "_test.go") {
		return nil, nil
	}

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, source, 0)
	if err != nil {
		return nil, err
	}

	allowedRanges := map[string][]sourceRange{}
	if samePath(path, allowedBackupFile) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, allowed := allowedProjectBackupSchemaFunctions[fn.Name.Name]; allowed {
				allowedRanges[fn.Name.Name] = append(allowedRanges[fn.Name.Name], sourceRange{start: fn.Body.Pos(), end: fn.Body.End()})
			}
		}
	}
	journalAllowedRanges := map[string][]sourceRange{}
	if samePath(path, allowedMigrationFile) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if fn.Name.Name == allowedGoMigrationJournalFunction {
				journalAllowedRanges[fn.Name.Name] = append(journalAllowedRanges[fn.Name.Name], sourceRange{start: fn.Body.Pos(), end: fn.Body.End()})
			}
		}
	}

	violations := []schemaMutationViolation{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch expr := node.(type) {
		case *ast.BinaryExpr:
			if expr.Op != token.ADD {
				return true
			}
			value, ok := stringExpressionValue(expr)
			if !ok {
				return false
			}
			violations = appendSchemaMutationViolations(
				violations,
				fileSet,
				path,
				expr.Pos(),
				value,
				allowedRanges,
				journalAllowedRanges,
			)
			return false
		case *ast.BasicLit:
			if expr.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(expr.Value)
			if err != nil {
				return true
			}
			violations = appendSchemaMutationViolations(
				violations,
				fileSet,
				path,
				expr.Pos(),
				value,
				allowedRanges,
				journalAllowedRanges,
			)
		}
		return true
	})
	return violations, nil
}

func appendSchemaMutationViolations(
	violations []schemaMutationViolation,
	fileSet *token.FileSet,
	path string,
	pos token.Pos,
	value string,
	allowedRanges map[string][]sourceRange,
	journalAllowedRanges map[string][]sourceRange,
) []schemaMutationViolation {
	if mutationAllowed(pos, allowedRanges) {
		return violations
	}
	journalAllowed := mutationAllowed(pos, journalAllowedRanges) && isAllowedGoMigrationJournalSQL(value)
	for _, match := range forbiddenMainSchemaSQL.FindAllString(value, -1) {
		if journalAllowed {
			continue
		}
		violations = append(violations, schemaMutationViolation{
			path: path,
			line: fileSet.Position(pos).Line,
			sql:  normalizeSQLFragment(match),
		})
	}
	return violations
}

func isAllowedGoMigrationJournalSQL(value string) bool {
	return normalizeSQLFragment(value) == normalizeSQLFragment(allowedGoMigrationJournalCreateSQL)
}

func stringExpressionValue(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(value.Value)
		if err != nil {
			return "", false
		}
		return unquoted, true
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := stringExpressionValue(value.X)
		right, rightOK := stringExpressionValue(value.Y)
		if !leftOK && !rightOK {
			return "", false
		}
		return left + " " + right, true
	default:
		return " ", true
	}
}

type sourceRange struct {
	start token.Pos
	end   token.Pos
}

func mutationAllowed(pos token.Pos, allowedRanges map[string][]sourceRange) bool {
	for _, ranges := range allowedRanges {
		for _, candidate := range ranges {
			if pos >= candidate.start && pos <= candidate.end {
				return true
			}
		}
	}
	return false
}

func normalizeSQLFragment(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func packageDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}

func samePath(a, b string) bool {
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aAbs) == filepath.Clean(bAbs)
}
