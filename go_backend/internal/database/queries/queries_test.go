package queries

import (
	"context"
	"testing"
	"time"

	"github.com/narrafork/narrafork/internal/database"
	"github.com/narrafork/narrafork/internal/id"
	"github.com/narrafork/narrafork/internal/model"
)

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// TestProjectCRUD_AllDialects exercises project queries on every dialect.
func TestProjectCRUD_AllDialects(t *testing.T) {
	for _, dc := range database.TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := database.OpenTestDB(t, dc)
			q := New(db)
			ctx := context.Background()

			now := nowISO()
			p := &model.Project{
				ID:        id.New(),
				Name:      "Test Project",
				Status:    model.ProjectStatusActive,
				FlowMode:  model.FlowModeClassic,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := q.Projects.Create(ctx, p); err != nil {
				t.Fatalf("create: %v", err)
			}

			got, err := q.Projects.GetByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Name != "Test Project" || got.Status != "active" {
				t.Errorf("got %+v", got)
			}

			// Update
			got.Name = "Renamed"
			got.UpdatedAt = nowISO()
			if err := q.Projects.Update(ctx, got); err != nil {
				t.Fatalf("update: %v", err)
			}
			reGot, _ := q.Projects.GetByID(ctx, p.ID)
			if reGot.Name != "Renamed" {
				t.Errorf("update failed: %q", reGot.Name)
			}

			// List
			list, err := q.Projects.List(ctx, "", 10, 0)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(list) != 1 {
				t.Errorf("expected 1 project, got %d", len(list))
			}

			// Delete
			if err := q.Projects.Delete(ctx, p.ID); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if _, err := q.Projects.GetByID(ctx, p.ID); err == nil {
				t.Error("expected not-found after delete")
			}
		})
	}
}

// TestChapterCRUD_AllDialects exercises chapter queries including the
// INTEGER-bool and REAL columns across dialects.
func TestChapterCRUD_AllDialects(t *testing.T) {
	for _, dc := range database.TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := database.OpenTestDB(t, dc)
			q := New(db)
			ctx := context.Background()

			now := nowISO()
			proj := &model.Project{
				ID: id.New(), Name: "P", Status: "active", FlowMode: "classic",
				CreatedAt: now, UpdatedAt: now,
			}
			if err := q.Projects.Create(ctx, proj); err != nil {
				t.Fatalf("create project: %v", err)
			}

			c := &model.Chapter{
				ID:          id.New(),
				ProjectID:   proj.ID,
				Title:       "Feature work",
				Status:      model.ChapterStatusActive,
				Role:        model.ChapterRoleBranch,
				Branch:      "feat/x",
				BaseBranch:  "main",
				IsRoot:      true,
				Pinned:      true,
				AxisOffset:  1.5,
				CrossOffset: -2.0,
				CommitCount: 3,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := q.Chapters.Create(ctx, c); err != nil {
				t.Fatalf("create chapter: %v", err)
			}

			got, err := q.Chapters.GetByID(ctx, c.ID)
			if err != nil {
				t.Fatalf("get chapter: %v", err)
			}
			// Verify bool round-trip (INTEGER 0/1 → bool)
			if !got.IsRoot {
				t.Error("IsRoot should be true")
			}
			if !got.Pinned {
				t.Error("Pinned should be true")
			}
			// Verify REAL round-trip
			if got.AxisOffset != 1.5 {
				t.Errorf("AxisOffset = %v, want 1.5", got.AxisOffset)
			}
			if got.CrossOffset != -2.0 {
				t.Errorf("CrossOffset = %v, want -2.0", got.CrossOffset)
			}
			if got.CommitCount != 3 {
				t.Errorf("CommitCount = %v, want 3", got.CommitCount)
			}
			// Nullable REAL should stay nil
			if got.PanelWidth != nil {
				t.Errorf("PanelWidth should be nil, got %v", *got.PanelWidth)
			}

			// ListByProject
			list, err := q.Chapters.ListByProject(ctx, proj.ID, "", 10, 0)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(list) != 1 {
				t.Errorf("expected 1 chapter, got %d", len(list))
			}

			// UpdateStatus
			if err := q.Chapters.UpdateStatus(ctx, c.ID, model.ChapterStatusDormant, nowISO()); err != nil {
				t.Fatalf("update status: %v", err)
			}
			reGot, _ := q.Chapters.GetByID(ctx, c.ID)
			if reGot.Status != model.ChapterStatusDormant {
				t.Errorf("status = %q, want dormant", reGot.Status)
			}
		})
	}
}
