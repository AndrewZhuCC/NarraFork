package narrators

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openGoalCreatorTestDB builds a minimal in-memory DB with just the tables that
// loadGoals + attachGoalCreators touch (narrator_goals, users).
func openGoalCreatorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT, objective TEXT, status TEXT, sort_order INTEGER, tokens_used INTEGER, time_used_seconds INTEGER, created_by TEXT, completed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, avatar_color TEXT, avatar_image_id TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema %q: %v", s, err)
		}
	}
	return db
}

// TestLoadGoalsAttachesCreatorMatchingTS verifies loadGoals enriches each goal
// with a "creator" field mirroring the TypeScript attachCreators
// (server/services/narrator-goal-service.ts): every goal has a creator key, set
// to the GoalCreator object {id, username, avatarColor, avatarImageId} when
// createdBy resolves to a known user, and null otherwise (unknown user or NULL
// createdBy).
func TestLoadGoalsAttachesCreatorMatchingTS(t *testing.T) {
	db := openGoalCreatorTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users (id, username, avatar_color, avatar_image_id) VALUES ('u1', 'alice', '#abcdef', 'img1'), ('u2', 'bob', NULL, NULL)`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	// g1: known user with avatar; g2: known user with null avatars;
	// g3: unknown user id; g4: NULL createdBy.
	if _, err := db.Exec(`INSERT INTO narrator_goals (id, narrator_id, objective, status, sort_order, tokens_used, time_used_seconds, created_by, completed_at, created_at, updated_at) VALUES
		('g1','n1','obj1','active',0,0,0,'u1',NULL,'2026-01-01','2026-01-01'),
		('g2','n1','obj2','pending',1,0,0,'u2',NULL,'2026-01-02','2026-01-02'),
		('g3','n1','obj3','pending',2,0,0,'ghost',NULL,'2026-01-03','2026-01-03'),
		('g4','n1','obj4','paused',3,0,0,NULL,NULL,'2026-01-04','2026-01-04')`); err != nil {
		t.Fatalf("insert goals: %v", err)
	}

	service := &Service{DB: db}
	goals, err := service.loadGoals(ctx, "n1")
	if err != nil {
		t.Fatalf("loadGoals: %v", err)
	}
	if len(goals) != 4 {
		t.Fatalf("expected 4 goals, got %d", len(goals))
	}

	// Every goal must carry a creator key (object or nil).
	for i, g := range goals {
		if _, ok := g["creator"]; !ok {
			t.Fatalf("goal[%d] missing creator key: %#v", i, g)
		}
	}

	// g1: full creator object.
	c1, ok := goals[0]["creator"].(map[string]any)
	if !ok {
		t.Fatalf("g1 creator not an object: %#v", goals[0]["creator"])
	}
	if c1["id"] != "u1" || c1["username"] != "alice" || c1["avatarColor"] != "#abcdef" || c1["avatarImageId"] != "img1" {
		t.Fatalf("g1 creator shape wrong: %#v", c1)
	}

	// g2: known user, null avatars must serialize as nil (not "").
	c2, ok := goals[1]["creator"].(map[string]any)
	if !ok {
		t.Fatalf("g2 creator not an object: %#v", goals[1]["creator"])
	}
	if c2["username"] != "bob" || c2["avatarColor"] != nil || c2["avatarImageId"] != nil {
		t.Fatalf("g2 creator should have nil avatars: %#v", c2)
	}

	// g3 (unknown user) and g4 (NULL createdBy): creator must be nil.
	if goals[2]["creator"] != nil {
		t.Fatalf("g3 (unknown user) creator should be nil, got %#v", goals[2]["creator"])
	}
	if goals[3]["creator"] != nil {
		t.Fatalf("g4 (NULL createdBy) creator should be nil, got %#v", goals[3]["creator"])
	}
}
