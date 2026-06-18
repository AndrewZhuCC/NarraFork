package chapters

import (
	"context"
	"testing"
)

// TestReconcileContainerStatesOnStartupMarksStaleRunning verifies the startup
// reconciliation (parity with TypeScript reconcileContainerStates): running
// container_instances rows whose Podman container is actually gone are marked
// "stopped", while genuinely running containers and rows without a container id
// are left untouched. A fake podman returns State.Running=false for the dead
// container id and true for the live one.
func TestReconcileContainerStatesOnStartupMarksStaleRunning(t *testing.T) {
	prependFakePodman(t, `#!/bin/sh
if [ "$1" = "--version" ]; then echo "podman version 5.0.0"; exit 0; fi
if [ "$1" = "inspect" ]; then
  case "$4" in
    deadcid) echo "false"; exit 0 ;;
    livecid) echo "true"; exit 0 ;;
    *) echo "false"; exit 1 ;;
  esac
fi
exit 0
`)

	database := openContainerRuntimeDB(t)
	insertContainerRuntimeProjectAndChapter(t, database, t.TempDir(), `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	// running row with a dead container -> should be marked stopped
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, created_at, updated_at) VALUES ('i-dead','c1','deadcid','web','running','now','now')`); err != nil {
		t.Fatalf("insert dead instance: %v", err)
	}
	// running row with a live container -> should stay running
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, created_at, updated_at) VALUES ('i-live','c1','livecid','api','running','now','now')`); err != nil {
		t.Fatalf("insert live instance: %v", err)
	}
	// running row without a container id -> cannot inspect, must stay untouched
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, created_at, updated_at) VALUES ('i-noid','c1','','db','running','now','now')`); err != nil {
		t.Fatalf("insert no-id instance: %v", err)
	}
	// already-stopped row -> must not be inspected or changed
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, created_at, updated_at) VALUES ('i-stopped','c1','othercid','cache','stopped','now','now')`); err != nil {
		t.Fatalf("insert stopped instance: %v", err)
	}

	svc := &Service{DB: database}
	stale, err := svc.ReconcileContainerStatesOnStartup(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stale != 1 {
		t.Fatalf("stale count = %d, want 1 (only the dead container)", stale)
	}

	statusFor := func(id string) string {
		var status string
		if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = ?`, id).Scan(&status); err != nil {
			t.Fatalf("read status %s: %v", id, err)
		}
		return status
	}
	if got := statusFor("i-dead"); got != "stopped" {
		t.Fatalf("dead instance status = %q, want stopped", got)
	}
	if got := statusFor("i-live"); got != "running" {
		t.Fatalf("live instance status = %q, want running (must not be reaped)", got)
	}
	if got := statusFor("i-noid"); got != "running" {
		t.Fatalf("no-id instance status = %q, want running (cannot inspect, leave untouched)", got)
	}
	if got := statusFor("i-stopped"); got != "stopped" {
		t.Fatalf("already-stopped instance status = %q, want stopped (unchanged)", got)
	}
}

// TestReconcileContainerStatesOnStartupNoPodmanIsNoop ensures a missing Podman
// runtime never erases recorded container state (no-op), so a transient runtime
// outage does not flip running rows to stopped.
func TestReconcileContainerStatesOnStartupNoPodmanIsNoop(t *testing.T) {
	// Point PATH at an empty dir so podman is not found.
	t.Setenv("PATH", t.TempDir())

	database := openContainerRuntimeDB(t)
	insertContainerRuntimeProjectAndChapter(t, database, t.TempDir(), `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, created_at, updated_at) VALUES ('i1','c1','cid','web','running','now','now')`); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	svc := &Service{DB: database}
	stale, err := svc.ReconcileContainerStatesOnStartup(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if stale != 0 {
		t.Fatalf("stale count = %d, want 0 (podman unavailable -> no-op)", stale)
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'i1'`).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "running" {
		t.Fatalf("status = %q, want running (must not change without podman)", status)
	}
}
