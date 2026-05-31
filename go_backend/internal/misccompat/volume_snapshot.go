package misccompat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

const (
	volumeSnapshotCommandTimeout     = 5 * time.Minute
	volumeSnapshotCleanupTimeout     = 10 * time.Second
	volumeSnapshotCommandOutputLimit = 64 * 1024
)

var (
	volumeSnapshotMaxBytes int64 = 1 << 30 // 1 GiB hard cap for a single snapshot archive.
	volumeSnapshotBaseDir        = ""
)

type volumeSnapshotRecord struct {
	ID              string
	ProjectID       string
	Name            string
	Description     sql.NullString
	SourceChapterID sql.NullString
	ServiceName     string
	ContainerPath   string
	SizeBytes       sql.NullInt64
	CreatedBy       sql.NullString
	CreatedAt       string
	UpdatedAt       string
}

type volumeLimitedWriter struct {
	w       io.Writer
	written int64
	limit   int64
}

var errVolumeSnapshotTooLarge = errors.New("volume snapshot archive exceeds size limit")

func (w *volumeLimitedWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return w.w.Write(p)
	}
	if w.written+int64(len(p)) > w.limit {
		remaining := w.limit - w.written
		if remaining > 0 {
			_, _ = w.w.Write(p[:remaining])
			w.written += remaining
		}
		return 0, errVolumeSnapshotTooLarge
	}
	n, err := w.w.Write(p)
	w.written += int64(n)
	return n, err
}

func podmanSnapshotAvailable() bool {
	_, err := exec.LookPath("podman")
	return err == nil
}

func volumeSnapshotFallbackPayload(code, message string) map[string]any {
	return map[string]any{
		"ok":        false,
		"success":   false,
		"supported": false,
		"fallback":  true,
		"code":      code,
		"reason":    message,
		"error":     message,
		"message":   message,
	}
}

func writeVolumeSnapshotFallback(w http.ResponseWriter, code, message string) {
	httpx.WriteJSON(w, http.StatusNotImplemented, volumeSnapshotFallbackPayload(code, message))
}

func defaultVolumeSnapshotRoot() (string, error) {
	if strings.TrimSpace(volumeSnapshotBaseDir) != "" {
		return volumeSnapshotBaseDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".narrafork", "snapshots"), nil
}

func validateSnapshotID(value, label string) error {
	if value == "" || len(value) > 200 {
		return fmt.Errorf("invalid %s", label)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid %s", label)
	}
	return nil
}

func volumeSnapshotPath(projectID, snapshotID string) (string, error) {
	if err := validateSnapshotID(projectID, "project id"); err != nil {
		return "", err
	}
	if err := validateSnapshotID(snapshotID, "snapshot id"); err != nil {
		return "", err
	}
	root, err := defaultVolumeSnapshotRoot()
	if err != nil {
		return "", err
	}
	base, err := filepath.Abs(filepath.Join(root, projectID))
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(base, snapshotID+".tar.gz"))
	if err != nil {
		return "", err
	}
	if candidate != filepath.Join(base, filepath.Base(candidate)) || !strings.HasPrefix(candidate, base+string(filepath.Separator)) {
		return "", errors.New("snapshot path escapes snapshot directory")
	}
	return candidate, nil
}

func ensureVolumeSnapshotDir(projectID string) (string, error) {
	if err := validateSnapshotID(projectID, "project id"); err != nil {
		return "", err
	}
	root, err := defaultVolumeSnapshotRoot()
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(filepath.Join(root, projectID))
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if dir != rootAbs && !strings.HasPrefix(dir, rootAbs+string(filepath.Separator)) {
		return "", errors.New("snapshot directory escapes snapshot root")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func validateContainerSnapshotPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", httpx.BadRequest("containerPath is required")
	}
	if len(value) > 1000 {
		return "", httpx.BadRequest("containerPath is too long")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", httpx.BadRequest("containerPath contains an invalid NUL byte")
	}
	if !strings.HasPrefix(value, "/") {
		return "", httpx.BadRequest("containerPath must be absolute")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", httpx.BadRequest("containerPath must not contain '..'")
		}
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." {
		return "", httpx.BadRequest("containerPath is invalid")
	}
	return cleaned, nil
}

func runVolumeSnapshotCommand(ctx context.Context, timeout time.Duration, stdin io.Reader, stdout io.Writer, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = volumeSnapshotCommandTimeout
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.Stdin = stdin
	if stdout != nil {
		cmd.Stdout = stdout
	}
	stderr := &limitedStringWriter{limit: volumeSnapshotCommandOutputLimit}
	cmd.Stderr = stderr
	err := cmd.Run()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return stderr.String(), fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		return stderr.String(), err
	}
	return stderr.String(), nil
}

type limitedStringWriter struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func (w *limitedStringWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		w.limit = volumeSnapshotCommandOutputLimit
	}
	if w.builder.Len() < w.limit {
		remaining := w.limit - w.builder.Len()
		if len(p) <= remaining {
			_, _ = w.builder.Write(p)
		} else {
			_, _ = w.builder.Write(p[:remaining])
			w.truncated = true
		}
	} else {
		w.truncated = true
	}
	return len(p), nil
}

func (w *limitedStringWriter) String() string {
	out := w.builder.String()
	if w.truncated {
		out += "\n[truncated]"
	}
	return out
}

func commandFailure(prefix, stderr string, err error) error {
	message := strings.TrimSpace(stderr)
	if message == "" && err != nil {
		message = err.Error()
	}
	if message == "" {
		message = "command failed"
	}
	return httpx.Error(http.StatusBadGateway, "PODMAN_FAILED", prefix+": "+message)
}

func (h *Handler) resolveSnapshotContainerID(ctx context.Context, chapterID, serviceName string) (string, error) {
	chapterID = strings.TrimSpace(chapterID)
	serviceName = strings.TrimSpace(serviceName)
	if chapterID == "" {
		return "", httpx.BadRequest("chapterId is required")
	}
	if serviceName == "" {
		return "", httpx.BadRequest("serviceName is required")
	}
	var containerID sql.NullString
	err := h.DB.QueryRowContext(ctx, `SELECT container_id FROM container_instances WHERE chapter_id = ? AND service_name = ? AND status = 'running' ORDER BY updated_at DESC LIMIT 1`, chapterID, serviceName).Scan(&containerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", httpx.BadRequest("No running container found for service \"" + serviceName + "\" in this chapter")
		}
		return "", err
	}
	if !containerID.Valid || strings.TrimSpace(containerID.String) == "" {
		return "", httpx.BadRequest("Running container record is missing a container id")
	}
	return strings.TrimSpace(containerID.String), nil
}

func (h *Handler) getVolumeSnapshotRecord(ctx context.Context, snapshotID string) (*volumeSnapshotRecord, error) {
	row := h.DB.QueryRowContext(ctx, `SELECT id,project_id,name,description,source_chapter_id,service_name,container_path,size_bytes,created_by,created_at,updated_at FROM volume_snapshots WHERE id=?`, snapshotID)
	var snap volumeSnapshotRecord
	if err := row.Scan(&snap.ID, &snap.ProjectID, &snap.Name, &snap.Description, &snap.SourceChapterID, &snap.ServiceName, &snap.ContainerPath, &snap.SizeBytes, &snap.CreatedBy, &snap.CreatedAt, &snap.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, httpx.NotFound("Volume snapshot")
		}
		return nil, err
	}
	return &snap, nil
}

func (h *Handler) ensureChapterInProject(ctx context.Context, chapterID, projectID string) error {
	var exists int
	err := h.DB.QueryRowContext(ctx, `SELECT 1 FROM chapters WHERE id = ? AND project_id = ?`, chapterID, projectID).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("Chapter")
		}
		return err
	}
	return nil
}

func (h *Handler) createVolumeSnapshotArchive(ctx context.Context, projectID, chapterID, serviceName, containerPath, name string, description any, createdBy any) (string, error) {
	if err := h.ensureChapterInProject(ctx, chapterID, projectID); err != nil {
		return "", err
	}
	cleanPath, err := validateContainerSnapshotPath(containerPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", httpx.BadRequest("name is required")
	}
	containerID, err := h.resolveSnapshotContainerID(ctx, chapterID, serviceName)
	if err != nil {
		return "", err
	}
	snapshotID := id.New()
	dir, err := ensureVolumeSnapshotDir(projectID)
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(dir, snapshotID+".tar.gz")
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	limited := &volumeLimitedWriter{w: file, limit: volumeSnapshotMaxBytes}
	stderr, runErr := runVolumeSnapshotCommand(ctx, volumeSnapshotCommandTimeout, nil, limited, "podman", "exec", containerID, "tar", "czf", "-", "-C", cleanPath, ".")
	closeErr := file.Close()
	if runErr != nil || closeErr != nil {
		_ = os.Remove(archivePath)
		if errors.Is(runErr, errVolumeSnapshotTooLarge) || errors.Is(closeErr, errVolumeSnapshotTooLarge) {
			return "", httpx.Error(http.StatusRequestEntityTooLarge, "SNAPSHOT_TOO_LARGE", fmt.Sprintf("Snapshot archive exceeds %d bytes", volumeSnapshotMaxBytes))
		}
		if runErr != nil {
			return "", commandFailure("Failed to create snapshot archive", stderr, runErr)
		}
		return "", closeErr
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		_ = os.Remove(archivePath)
		return "", err
	}
	if volumeSnapshotMaxBytes > 0 && info.Size() > volumeSnapshotMaxBytes {
		_ = os.Remove(archivePath)
		return "", httpx.Error(http.StatusRequestEntityTooLarge, "SNAPSHOT_TOO_LARGE", fmt.Sprintf("Snapshot archive exceeds %d bytes", volumeSnapshotMaxBytes))
	}
	now := db.NowISO()
	_, err = h.DB.ExecContext(ctx, `INSERT INTO volume_snapshots (id,project_id,name,description,source_chapter_id,service_name,container_path,size_bytes,created_by,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, snapshotID, projectID, name, description, chapterID, serviceName, cleanPath, info.Size(), createdBy, now, now)
	if err != nil {
		_ = os.Remove(archivePath)
		return "", err
	}
	return snapshotID, nil
}

func (h *Handler) applyVolumeSnapshotArchive(ctx context.Context, snapshotID, targetChapterID string, appliedBy any) error {
	snap, err := h.getVolumeSnapshotRecord(ctx, snapshotID)
	if err != nil {
		return err
	}
	if err := h.ensureChapterInProject(ctx, targetChapterID, snap.ProjectID); err != nil {
		return err
	}
	cleanPath, err := validateContainerSnapshotPath(snap.ContainerPath)
	if err != nil {
		return err
	}
	archivePath, err := volumeSnapshotPath(snap.ProjectID, snap.ID)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return httpx.BadRequest("Snapshot archive file is missing")
		}
		return err
	}
	if volumeSnapshotMaxBytes > 0 && info.Size() > volumeSnapshotMaxBytes {
		return httpx.Error(http.StatusRequestEntityTooLarge, "SNAPSHOT_TOO_LARGE", fmt.Sprintf("Snapshot archive exceeds %d bytes", volumeSnapshotMaxBytes))
	}
	containerID, err := h.resolveSnapshotContainerID(ctx, targetChapterID, snap.ServiceName)
	if err != nil {
		return err
	}
	if stderr, err := runVolumeSnapshotCommand(ctx, volumeSnapshotCleanupTimeout, nil, nil, "podman", "exec", containerID, "mkdir", "-p", cleanPath); err != nil {
		return commandFailure("Failed to create target path in container", stderr, err)
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	if stderr, err := runVolumeSnapshotCommand(ctx, volumeSnapshotCommandTimeout, file, nil, "podman", "exec", "-i", containerID, "tar", "xzf", "-", "-C", cleanPath); err != nil {
		return commandFailure("Failed to apply snapshot archive", stderr, err)
	}
	_, err = h.DB.ExecContext(ctx, `INSERT INTO volume_snapshot_applications (id,snapshot_id,chapter_id,applied_at,applied_by) VALUES (?,?,?,?,?)`, id.New(), snapshotID, targetChapterID, db.NowISO(), appliedBy)
	return err
}
