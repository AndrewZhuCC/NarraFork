package systemroutes

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"narrafork-go/internal/httpx"
	"narrafork-go/internal/restart"
)

const preparedUpdatePrefix = ".narrafork-update-"

var updateExecutablePath = os.Executable
var updateStartReplacement = startReplacementProcess

func (h *Handler) updateApply(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpxDecodeUpdateApplyBody(r, &body); err != nil {
		return err
	}
	version := strings.TrimSpace(stringMap(body, "version", r.URL.Query().Get("version")))
	payload := h.applyDownloadedUpdate(r.Context(), version)
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func httpxDecodeUpdateApplyBody(r *http.Request, body *map[string]any) error {
	if body == nil {
		return nil
	}
	return httpx.DecodeOptionalLooseJSON(r, body)
}

func (h *Handler) applyDownloadedUpdate(ctx context.Context, version string) map[string]any {
	if h == nil || h.Restart == nil {
		return updateApplyUnavailable("go_backend can download artifacts for operators, but restart handoff is not configured.")
	}
	execPath, err := updateExecutablePath()
	if err != nil || strings.TrimSpace(execPath) == "" {
		return updateApplyFailure("Current executable path is unavailable", "", false)
	}
	if info, err := os.Stat(execPath); err != nil || info.IsDir() {
		return updateApplyFailure("Current executable is not a regular file", "", false)
	}

	dir, err := updateVersionDir(version)
	if err != nil {
		return updateApplyFailure(err.Error(), "", true)
	}
	artifact, metadata, err := selectUpdateArtifact(version, dir)
	if err != nil {
		return updateApplyFailure(err.Error(), "", true)
	}
	if err := verifyUpdateArtifact(dir, artifact); err != nil {
		return updateApplyFailure(err.Error(), artifact.Path, true)
	}
	preparedPath, err := prepareUpdateBinary(execPath, artifact.Path, metadata.Version)
	if err != nil {
		return updateApplyFailure(err.Error(), artifact.Path, true)
	}
	session, err := h.Restart.Begin()
	if err != nil {
		return updateApplyFailure("Failed to prepare graceful restart handoff: "+err.Error(), preparedPath, true)
	}
	pid, err := updateStartReplacement(ctx, preparedPath, os.Args[1:], restart.EnvForSession(session))
	if err != nil {
		h.Restart.Cancel()
		return updateApplyFailure("Failed to start replacement server: "+err.Error(), preparedPath, true)
	}
	return map[string]any{
		"success":             true,
		"serverRestarting":    true,
		"restarting":          true,
		"ready":               true,
		"placed":              true,
		"canAutoRestart":      true,
		"selfUpdateAvailable": true,
		"manualOnly":          false,
		"instructions":        updateAutomaticInstructions(preparedPath),
		"message":             "Replacement NarraFork process started; the new process will request graceful shutdown of the old process.",
		"version":             metadata.Version,
		"newBinaryPath":       preparedPath,
		"replacementPid":      pid,
		"handoffUrl":          session.URL,
	}
}

func (h *Handler) canApplySelfUpdate() bool {
	if h == nil || h.Restart == nil {
		return false
	}
	execPath, err := updateExecutablePath()
	if err != nil || strings.TrimSpace(execPath) == "" {
		return false
	}
	info, err := os.Stat(execPath)
	return err == nil && !info.IsDir() && info.Mode().IsRegular()
}

func updateApplyUnavailable(message string) map[string]any {
	return map[string]any{"success": false, "serverRestarting": false, "restarting": false, "canAutoRestart": false, "selfUpdateAvailable": false, "manualOnly": true, "message": message, "instructions": updateManualInstructions("")}
}

func updateApplyFailure(message, updatePath string, canAuto bool) map[string]any {
	payload := map[string]any{"success": false, "serverRestarting": false, "restarting": false, "canAutoRestart": false, "selfUpdateAvailable": canAuto, "manualOnly": !canAuto, "error": message, "message": message}
	if updatePath != "" {
		payload["updatePath"] = updatePath
	}
	payload["instructions"] = updateManualInstructions(updatePath)
	return payload
}

func selectUpdateArtifact(version, dir string) (updateDownloadedFileInfo, updateDownloadMetadata, error) {
	metadata, ok := readUpdateDownloadMetadata(dir)
	if !ok {
		return updateDownloadedFileInfo{}, metadata, errors.New("No verified prepared update metadata found")
	}
	if version != "" && metadata.Version != version {
		return updateDownloadedFileInfo{}, metadata, fmt.Errorf("Downloaded update version %q does not match requested version %q", metadata.Version, version)
	}
	if len(metadata.Files) == 0 {
		return updateDownloadedFileInfo{}, metadata, errors.New("Downloaded update metadata contains no files")
	}
	if len(metadata.Files) == 1 {
		return metadata.Files[0], metadata, nil
	}
	matches := []updateDownloadedFileInfo{}
	for _, file := range metadata.Files {
		name := strings.ToLower(file.Name)
		path := strings.ToLower(filepath.Base(file.Path))
		if strings.Contains(name, runtime.GOOS) && strings.Contains(name, runtime.GOARCH) || strings.Contains(path, runtime.GOOS) && strings.Contains(path, runtime.GOARCH) {
			matches = append(matches, file)
		}
	}
	if len(matches) == 1 {
		return matches[0], metadata, nil
	}
	return updateDownloadedFileInfo{}, metadata, errors.New("Multiple update artifacts are present and no unique artifact matches this platform")
}

func verifyUpdateArtifact(dir string, file updateDownloadedFileInfo) error {
	cleanDir := filepath.Clean(dir)
	cleanPath := filepath.Clean(file.Path)
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) && rel == cleanPath {
		return errors.New("Update artifact path is outside the verified update directory")
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return errors.New("Update artifact is not a regular file")
	}
	if file.Size > 0 && info.Size() != file.Size {
		return fmt.Errorf("Update artifact size mismatch: expected %d bytes, got %d", file.Size, info.Size())
	}
	if strings.TrimSpace(file.SHA512) == "" {
		return nil
	}
	actualHex, actualBase64, err := sha512ForFile(cleanPath)
	if err != nil {
		return err
	}
	if !updateSHA512Matches(file.SHA512, actualHex, actualBase64) {
		return errors.New("SHA512 mismatch for prepared update artifact")
	}
	return nil
}

func prepareUpdateBinary(currentExec, artifactPath, version string) (string, error) {
	dir := filepath.Dir(currentExec)
	base := filepath.Base(currentExec)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = "narrafork-go"
	}
	versionPart := safeUpdatePathPart(firstNonEmpty(version, "pending"))
	candidate := filepath.Join(dir, stem+preparedUpdatePrefix+versionPart+ext)
	if filepath.Clean(candidate) == filepath.Clean(currentExec) {
		return "", errors.New("Prepared update destination resolves to the current executable")
	}
	if err := copyFileNoOverwrite(artifactPath, candidate); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(candidate, 0o755)
	}
	return candidate, nil
}

func copyFileNoOverwrite(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}

func sha512ForFile(path string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	hash := sha512.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", "", err
	}
	sum := hash.Sum(nil)
	return hex.EncodeToString(sum), base64.StdEncoding.EncodeToString(sum), nil
}

func safeUpdatePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "pending"
	}
	value = strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\x00", "", "..", "-").Replace(value)
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func startReplacementProcess(ctx context.Context, path string, args []string, extraEnv []string) (int, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Dir, _ = os.Getwd()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
