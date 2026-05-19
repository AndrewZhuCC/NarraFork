package misccompat

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/narrators"
)

type shareRecord struct {
	ID           string
	OriginalName string
	StoragePath  string
	Size         int64
	ExpiresAt    time.Time
	CreatedBy    string
}

type shareDiskMetadata struct {
	ID           string `json:"id"`
	OriginalName string `json:"originalName"`
	FileName     string `json:"fileName"`
	SizeBytes    int64  `json:"sizeBytes"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	CreatedBy    string `json:"createdBy,omitempty"`
}

type shareRecordStore struct {
	mu      sync.RWMutex
	records map[string]shareRecord
}

func newShareRecordStore() *shareRecordStore {
	return &shareRecordStore{records: map[string]shareRecord{}}
}

func (s *shareRecordStore) set(record shareRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.ID] = record
}

func (s *shareRecordStore) get(shareID string, now time.Time) (shareRecord, bool) {
	s.mu.Lock()
	record, ok := s.records[shareID]
	if !ok {
		s.mu.Unlock()
		return shareRecord{}, false
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
		delete(s.records, shareID)
		s.mu.Unlock()
		removeShareDisk(shareID)
		return shareRecord{}, false
	}
	s.mu.Unlock()
	return record, true
}

func (s *shareRecordStore) delete(shareID string) bool {
	s.mu.Lock()
	_, ok := s.records[shareID]
	if ok {
		delete(s.records, shareID)
	}
	s.mu.Unlock()
	if ok {
		removeShareDisk(shareID)
	}
	return ok
}

func (s *shareRecordStore) clearMemory() int {
	s.mu.Lock()
	removed := len(s.records)
	s.records = map[string]shareRecord{}
	s.mu.Unlock()
	return removed
}

func (s *shareRecordStore) len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

var shareRecords = newShareRecordStore()

const (
	maxStorageScanEntries       = 5000
	maxStorageProjectRows       = 100
	maxDatabaseSummaryTables    = 50
	maxCleanupNarratorIDs       = 500
	maxCleanupEntries           = 500
	maxCompatGitOutputBytes     = 128 * 1024
	maxShareHTMLPreviewBytes    = 2 * 1024 * 1024
	maxShareMetadataBytes       = 16 * 1024
	shareHTMLPreviewContentType = "text/html; charset=utf-8"
)

var safeShareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var sharePreviewMIMETypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".ogg":  "video/ogg",
	".pdf":  "application/pdf",
	".html": shareHTMLPreviewContentType,
	".htm":  shareHTMLPreviewContentType,
}

type Handler struct {
	DB        *sql.DB
	Git       *gitx.Service
	Chapters  *chapters.Service
	Narrators *narrators.Service
	Settings  *config.Store
}

func NewHandler(sqlDB *sql.DB, services ...any) *Handler {
	h := &Handler{DB: sqlDB}
	for _, svc := range services {
		switch v := svc.(type) {
		case *gitx.Service:
			h.Git = v
		case *chapters.Service:
			h.Chapters = v
		case *narrators.Service:
			h.Narrators = v
		case *config.Store:
			h.Settings = v
		}
	}
	return h
}

func (h *Handler) RegisterWorkspaces(r chi.Router) {
	r.Get("/", httpx.Handler(h.listWorkspaces))
	r.Post("/", httpx.Handler(h.createWorkspace))
	r.Get("/{id}", httpx.Handler(h.getWorkspace))
	r.Patch("/{id}", httpx.Handler(h.updateWorkspace))
	r.Delete("/{id}", httpx.Handler(h.deleteWorkspace))
}

func (h *Handler) RegisterHooks(r chi.Router) {
	r.Get("/", httpx.Handler(h.listHooks))
	r.Get("/all", httpx.Handler(h.listHooks))
	r.Post("/", httpx.Handler(h.createHook))
	r.Get("/{id}", httpx.Handler(h.getHook))
	r.Put("/{id}", httpx.Handler(h.updateHook))
	r.Delete("/{id}", httpx.Handler(h.deleteHook))
}

func (h *Handler) RegisterCustomSubagents(r chi.Router) {
	r.Get("/", httpx.Handler(h.listCustomSubagents))
	r.Post("/", httpx.Handler(h.createCustomSubagent))
	r.Get("/{name}", httpx.Handler(h.getCustomSubagent))
	r.Put("/{name}", httpx.Handler(h.updateCustomSubagent))
	r.Delete("/{name}", httpx.Handler(h.deleteCustomSubagent))
}

func (h *Handler) RegisterUploads(r chi.Router) {
	r.Get("/avatars/{userId}/{imageId}", httpx.Handler(h.serveAvatar))
	r.Get("/{narratorId}/{imageId}", httpx.Handler(h.serveUpload))
}

func (h *Handler) RegisterNotificationSounds(r chi.Router) {
	r.Post("/", httpx.Handler(h.uploadNotificationSound))
	r.Get("/{id}", httpx.Handler(h.serveNotificationSound))
	r.Delete("/{id}", httpx.Handler(h.deleteNotificationSound))
}

func (h *Handler) RegisterPublicShares(r chi.Router) {
	r.Get("/{shareId}", httpx.Handler(h.serveShare(false)))
	r.Get("/{shareId}/preview", httpx.Handler(h.serveShare(true)))
}

func (h *Handler) RegisterShareCreate(r chi.Router) {
	r.Post("/", httpx.Handler(h.createShare))
}

func (h *Handler) listWorkspaces(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, user_id, title, tree, created_at, updated_at FROM workspaces WHERE user_id = ? ORDER BY updated_at DESC`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) createWorkspace(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Title string `json:"title"`
		Tree  string `json:"tree"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Tree == "" {
		return httpx.BadRequest("tree is required")
	}
	if body.Title == "" {
		body.Title = "Workspace"
	}
	wsID := id.New()
	now := time.Now().UnixMilli()
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO workspaces (id,user_id,title,tree,created_at,updated_at) VALUES (?,?,?,?,?,?)`, wsID, userID, body.Title, body.Tree, now, now)
	if err != nil {
		return err
	}
	return h.getWorkspaceByID(w, r, wsID, userID, http.StatusCreated)
}
func (h *Handler) getWorkspace(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	return h.getWorkspaceByID(w, r, chi.URLParam(r, "id"), userID, http.StatusOK)
}
func (h *Handler) updateWorkspace(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	if v, ok := body["title"].(string); ok {
		sets = append(sets, "title = ?")
		args = append(args, v)
	}
	if v, ok := body["tree"].(string); ok {
		sets = append(sets, "tree = ?")
		args = append(args, v)
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, time.Now().UnixMilli(), chi.URLParam(r, "id"), userID)
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE workspaces SET `+strings.Join(sets, ",")+` WHERE id = ? AND user_id = ?`, args...); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func (h *Handler) deleteWorkspace(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	_, err = h.DB.ExecContext(r.Context(), `DELETE FROM workspaces WHERE id = ? AND user_id = ?`, chi.URLParam(r, "id"), userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func (h *Handler) getWorkspaceByID(w http.ResponseWriter, r *http.Request, wsID, userID string, status int) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,user_id,title,tree,created_at,updated_at FROM workspaces WHERE id = ? AND user_id = ?`, wsID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return httpx.NotFound("Workspace")
	}
	httpx.WriteJSON(w, status, items[0])
	return nil
}

func (h *Handler) listHooks(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,project_id,event,matcher,type,command,url,headers,prompt,model,timeout,enabled,sort_order,created_at,updated_at FROM hooks WHERE (? = '' OR project_id = ?) ORDER BY sort_order ASC, created_at DESC`, r.URL.Query().Get("projectId"), r.URL.Query().Get("projectId"))
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}
func (h *Handler) createHook(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	hookID := id.New()
	now := db.NowISO()
	headers, _ := json.Marshal(body["headers"])
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO hooks (id,project_id,event,matcher,type,command,url,headers,prompt,model,timeout,enabled,sort_order,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, hookID, nullableString(body["projectId"]), stringOr(body["event"], "PreToolUse"), stringOr(body["matcher"], ""), stringOr(body["type"], "command"), nullableString(body["command"]), nullableString(body["url"]), string(headers), nullableString(body["prompt"]), nullableString(body["model"]), intOr(body["timeout"], 30), boolInt(body["enabled"], true), intOr(body["sortOrder"], 0), now, now)
	if err != nil {
		return err
	}
	return h.getHookByID(w, r, hookID, http.StatusCreated)
}
func (h *Handler) getHook(w http.ResponseWriter, r *http.Request) error {
	return h.getHookByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) updateHook(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	_, err := h.DB.ExecContext(r.Context(), `UPDATE hooks SET event=COALESCE(?,event), matcher=COALESCE(?,matcher), type=COALESCE(?,type), command=COALESCE(?,command), url=COALESCE(?,url), timeout=COALESCE(?,timeout), enabled=COALESCE(?,enabled), sort_order=COALESCE(?,sort_order), updated_at=? WHERE id=?`, nullableString(body["event"]), nullableString(body["matcher"]), nullableString(body["type"]), nullableString(body["command"]), nullableString(body["url"]), nullableNumber(body["timeout"]), nullableBoolInt(body["enabled"]), nullableNumber(body["sortOrder"]), db.NowISO(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	return h.getHookByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) deleteHook(w http.ResponseWriter, r *http.Request) error {
	_, err := h.DB.ExecContext(r.Context(), `DELETE FROM hooks WHERE id=?`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func (h *Handler) getHookByID(w http.ResponseWriter, r *http.Request, hookID string, status int) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,project_id,event,matcher,type,command,url,headers,prompt,model,timeout,enabled,sort_order,created_at,updated_at FROM hooks WHERE id=?`, hookID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return httpx.NotFound("Hook")
	}
	httpx.WriteJSON(w, status, items[0])
	return nil
}

func (h *Handler) listCustomSubagents(w http.ResponseWriter, r *http.Request) error {
	defs, err := listCustomSubagentDefs()
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, customSubagentResponses(defs))
	return nil
}
func (h *Handler) createCustomSubagent(w http.ResponseWriter, r *http.Request) error {
	def, err := createCustomSubagentDef(r)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, customSubagentResponse(def))
	return nil
}
func (h *Handler) updateCustomSubagent(w http.ResponseWriter, r *http.Request) error {
	def, err := updateCustomSubagentDef(r, chi.URLParam(r, "name"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, customSubagentResponse(def))
	return nil
}
func (h *Handler) getCustomSubagent(w http.ResponseWriter, r *http.Request) error {
	def, err := loadCustomSubagentDef(chi.URLParam(r, "name"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, customSubagentResponse(def))
	return nil
}
func (h *Handler) deleteCustomSubagent(w http.ResponseWriter, r *http.Request) error {
	if err := deleteCustomSubagentDef(chi.URLParam(r, "name")); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) serveAvatar(w http.ResponseWriter, r *http.Request) error {
	return serveUploadFile(w, r, filepath.Join("avatars", chi.URLParam(r, "userId")), chi.URLParam(r, "imageId"))
}
func (h *Handler) serveUpload(w http.ResponseWriter, r *http.Request) error {
	return serveUploadFile(w, r, chi.URLParam(r, "narratorId"), chi.URLParam(r, "imageId"))
}
func serveUploadFile(w http.ResponseWriter, r *http.Request, dir, imageID string) error {
	root := uploadsRoot()
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(imageID) == "" {
		return httpx.NotFound("Upload")
	}
	base, err := resolveExistingPathWithinBase(root, dir)
	if err != nil {
		return httpx.NotFound("Upload")
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return httpx.NotFound("Upload")
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), imageID) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path, err := resolveExistingPathWithinBase(base, entry.Name())
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		ctype := mime.TypeByExtension(filepath.Ext(path))
		if ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		http.ServeFile(w, r, path)
		return nil
	}
	return httpx.NotFound("Upload")
}
func uploadsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "uploads")
}

func resolvePathWithinBase(base, candidate string) (string, error) {
	baseAbs, err := filepath.Abs(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("path is empty")
	}
	resolved := candidate
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(baseAbs, resolved)
	}
	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if !pathWithinBase(baseAbs, resolvedAbs) {
		return "", fmt.Errorf("path escapes base directory")
	}
	return resolvedAbs, nil
}

func resolveExistingPathWithinBase(base, candidate string) (string, error) {
	baseAbs, err := filepath.Abs(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	lexical, err := resolvePathWithinBase(baseAbs, candidate)
	if err != nil {
		return "", err
	}
	realBase, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		return "", err
	}
	realCandidate, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", err
	}
	realBaseAbs, err := filepath.Abs(realBase)
	if err != nil {
		return "", err
	}
	realCandidateAbs, err := filepath.Abs(realCandidate)
	if err != nil {
		return "", err
	}
	if !pathWithinBase(realBaseAbs, realCandidateAbs) {
		return "", fmt.Errorf("path escapes base directory")
	}
	return realCandidateAbs, nil
}

func pathWithinBase(baseAbs, targetAbs string) bool {
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

func isSensitiveSharePath(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}
	for _, subdir := range []string{".narrafork", ".ssh"} {
		if _, err := resolvePathWithinBase(filepath.Join(home, subdir), path); err == nil {
			return true
		}
	}
	return false
}

func (h *Handler) uploadNotificationSound(w http.ResponseWriter, r *http.Request) error {
	userID := optionalUserID(r)
	r.Body = http.MaxBytesReader(w, r.Body, 2*1024*1024)
	if err := r.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		return err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("missing file in form data")
	}
	defer file.Close()
	mediaType := header.Header.Get("Content-Type")
	ext := soundExt(mediaType, filepath.Ext(header.Filename))
	if ext == "" {
		return httpx.BadRequest("unsupported audio type")
	}
	soundID := id.Must(8)
	dir := filepath.Join(notificationSoundsRoot(), userID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, soundID+ext)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": soundID, "filename": soundID + ext, "mediaType": mediaType})
	return nil
}

func (h *Handler) serveNotificationSound(w http.ResponseWriter, r *http.Request) error {
	path := findSoundFile(chi.URLParam(r, "id"), "")
	if path == "" {
		return httpx.NotFound("Notification sound")
	}
	if ctype := mime.TypeByExtension(filepath.Ext(path)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
	return nil
}

func (h *Handler) deleteNotificationSound(w http.ResponseWriter, r *http.Request) error {
	userID := optionalUserID(r)
	path := findSoundFile(chi.URLParam(r, "id"), userID)
	if path == "" {
		return httpx.NotFound("Notification sound")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func notificationSoundsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "notification-sounds")
}

func soundExt(mediaType, fallback string) string {
	switch mediaType {
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	case "audio/webm":
		return ".webm"
	}
	fallback = strings.ToLower(fallback)
	if fallback == ".mp3" || fallback == ".wav" || fallback == ".ogg" || fallback == ".webm" {
		return fallback
	}
	return ""
}

func findSoundFile(soundID, userID string) string {
	if strings.Contains(soundID, "/") || strings.Contains(soundID, "\\") || strings.Contains(soundID, "..") {
		return ""
	}
	root := notificationSoundsRoot()
	searchDirs := []string{}
	if userID != "" {
		searchDirs = append(searchDirs, filepath.Join(root, userID))
	} else if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				searchDirs = append(searchDirs, filepath.Join(root, entry.Name()))
			}
		}
	}
	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), soundID) {
				return filepath.Join(dir, entry.Name())
			}
		}
	}
	return ""
}

func (h *Handler) createShare(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Path        string `json:"path"`
		ExpiryHours int    `json:"expiryHours"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	path := strings.TrimSpace(body.Path)
	if path == "" {
		return httpx.BadRequest("path is required")
	}
	if body.ExpiryHours < 0 {
		return httpx.BadRequest("expiryHours must be positive")
	}
	defaultExpiryHours, maxShareSizeBytes := h.shareLimits()
	if body.ExpiryHours == 0 {
		body.ExpiryHours = defaultExpiryHours
	}
	if body.ExpiryHours <= 0 {
		return httpx.BadRequest("expiryHours must be positive")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	lexicalPath, err := resolvePathWithinBase(cwd, path)
	if err != nil {
		return httpx.BadRequest("path must be inside the current working directory")
	}
	linkInfo, err := os.Lstat(lexicalPath)
	if err != nil || linkInfo.Mode()&os.ModeSymlink != 0 {
		return httpx.BadRequest("path must be an existing regular file")
	}
	path, err = resolveExistingPathWithinBase(cwd, path)
	if err != nil {
		return httpx.BadRequest("path must be inside the current working directory")
	}
	if isSensitiveSharePath(path) {
		return httpx.BadRequest("path is not shareable")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return httpx.BadRequest("path must be an existing regular file")
	}
	if info.Size() > maxShareSizeBytes {
		return httpx.Error(http.StatusRequestEntityTooLarge, "SHARE_FILE_TOO_LARGE", "file exceeds the maximum share size")
	}

	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	openedInfo, err := in.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.Mode().IsRegular() {
		return httpx.BadRequest("path must be an existing regular file")
	}
	if openedInfo.Size() > maxShareSizeBytes {
		return httpx.Error(http.StatusRequestEntityTooLarge, "SHARE_FILE_TOO_LARGE", "file exceeds the maximum share size")
	}

	shareID := id.Must(12)
	root := sharesRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(root, ".tmp-"+shareID+"-")
	if err != nil {
		return err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	name := filepath.Base(path)
	tmpStoragePath := filepath.Join(tmpDir, name)
	out, err := os.OpenFile(tmpStoragePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	limited := &io.LimitedReader{R: in, N: maxShareSizeBytes + 1}
	copied, copyErr := io.Copy(out, limited)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if copied > maxShareSizeBytes {
		return httpx.Error(http.StatusRequestEntityTooLarge, "SHARE_FILE_TOO_LARGE", "file exceeds the maximum share size")
	}

	dir := shareDirPath(shareID)
	if err := os.Rename(tmpDir, dir); err != nil {
		return err
	}
	cleanupTmp = false
	storagePath := filepath.Join(dir, name)
	expires := time.Now().Add(time.Duration(body.ExpiryHours) * time.Hour)
	record := shareRecord{ID: shareID, OriginalName: name, StoragePath: storagePath, Size: copied, ExpiresAt: expires, CreatedBy: userID}
	if err := writeShareMetadata(record); err != nil {
		removeShareDisk(shareID)
		return err
	}
	shareRecords.set(record)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": shareID, "url": "/api/shares/" + shareID, "previewUrl": "/api/shares/" + shareID + "/preview", "expiresAt": expires.Format(time.RFC3339)})
	return nil
}

func (h *Handler) serveShare(preview bool) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		shareID := chi.URLParam(r, "shareId")
		path, name := resolveShare(shareID)
		if path == "" {
			return httpx.NotFound("Share")
		}
		if preview {
			return serveSharePreview(w, r, path, name)
		}
		serveShareDownload(w, r, path, name)
		return nil
	}
}

func serveShareDownload(w http.ResponseWriter, r *http.Request, path, name string) {
	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", shareAttachmentDisposition(name))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, path)
}

func serveSharePreview(w http.ResponseWriter, r *http.Request, path, name string) error {
	ctype, ok := sharePreviewMIMETypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return httpx.NotFound("Preview not supported for this file type")
	}
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ctype == shareHTMLPreviewContentType {
		return serveEscapedHTMLSharePreview(w, path)
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeFile(w, r, path)
	return nil
}

func serveEscapedHTMLSharePreview(w http.ResponseWriter, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return httpx.NotFound("Share file")
	}
	if info.Size() > maxShareHTMLPreviewBytes {
		return httpx.BadRequest("HTML preview is too large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", shareHTMLPreviewContentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data: https: http:;")
	w.Header().Set("Cache-Control", "no-cache")
	_, err = w.Write([]byte(`<!doctype html><meta charset="utf-8"><pre style="white-space: pre-wrap; word-break: break-word;">` + html.EscapeString(string(raw)) + `</pre>`))
	return err
}

func shareAttachmentDisposition(name string) string {
	base := filepath.Base(name)
	asciiFallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, base)
	encoded := url.PathEscape(base)
	return `attachment; filename="` + asciiFallback + `"; filename*=UTF-8''` + encoded
}

func resolveShare(shareID string) (string, string) {
	if !isSafeShareID(shareID) {
		return "", ""
	}
	now := time.Now()
	if record, ok := shareRecords.get(shareID, now); ok {
		if info, err := os.Stat(record.StoragePath); err == nil && info.Mode().IsRegular() {
			return record.StoragePath, record.OriginalName
		}
		return "", ""
	}
	record, ok := readShareMetadata(shareID, now)
	if !ok {
		return "", ""
	}
	shareRecords.set(record)
	return record.StoragePath, record.OriginalName
}

func isSafeShareID(shareID string) bool {
	trimmed := strings.TrimSpace(shareID)
	return shareID == trimmed && safeShareIDPattern.MatchString(shareID)
}

func isSafeShareFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	return filepath.Base(name) == name
}

func writeShareMetadata(record shareRecord) error {
	if !isSafeShareID(record.ID) {
		return fmt.Errorf("invalid share id")
	}
	fileName := filepath.Base(record.StoragePath)
	if !isSafeShareFileName(fileName) || !isSafeShareFileName(record.OriginalName) {
		return fmt.Errorf("invalid share filename")
	}
	metadata := shareDiskMetadata{
		ID:           record.ID,
		OriginalName: record.OriginalName,
		FileName:     fileName,
		SizeBytes:    record.Size,
		CreatedBy:    record.CreatedBy,
	}
	if !record.ExpiresAt.IsZero() {
		metadata.ExpiresAt = record.ExpiresAt.Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	path := shareMetadataPath(record.ID)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readShareMetadata(shareID string, now time.Time) (shareRecord, bool) {
	if !isSafeShareID(shareID) {
		return shareRecord{}, false
	}
	metadataPath := shareMetadataPath(shareID)
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Size() > maxShareMetadataBytes {
		return shareRecord{}, false
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return shareRecord{}, false
	}
	var metadata shareDiskMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return shareRecord{}, false
	}
	if metadata.ID != shareID {
		return shareRecord{}, false
	}
	fileName := strings.TrimSpace(metadata.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(metadata.OriginalName)
	}
	originalName := strings.TrimSpace(metadata.OriginalName)
	if originalName == "" {
		originalName = fileName
	}
	if !isSafeShareFileName(fileName) || !isSafeShareFileName(originalName) {
		return shareRecord{}, false
	}
	expiresAt, ok := parseShareMetadataExpiry(metadata.ExpiresAt)
	if !ok {
		return shareRecord{}, false
	}
	if !expiresAt.IsZero() && !expiresAt.After(now) {
		removeShareDisk(shareID)
		return shareRecord{}, false
	}
	storagePath, ok := shareContentPathFromMetadata(shareID, fileName)
	if !ok {
		return shareRecord{}, false
	}
	return shareRecord{ID: shareID, OriginalName: originalName, StoragePath: storagePath, Size: metadata.SizeBytes, ExpiresAt: expiresAt, CreatedBy: metadata.CreatedBy}, true
}

func parseShareMetadataExpiry(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func shareContentPathFromMetadata(shareID, fileName string) (string, bool) {
	dir := shareDirPath(shareID)
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	fileInfo, err := os.Lstat(filepath.Join(dir, fileName))
	if err != nil || fileInfo.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	path, err := resolveExistingPathWithinBase(dir, fileName)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func removeShareDisk(shareID string) {
	if !isSafeShareID(shareID) {
		return
	}
	_ = os.RemoveAll(shareDirPath(shareID))
	_ = os.Remove(shareMetadataPath(shareID))
	_ = os.Remove(shareMetadataPath(shareID) + ".tmp")
}

func sharesRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "shares")
}

func shareDirPath(shareID string) string {
	return filepath.Join(sharesRoot(), shareID)
}

func shareMetadataPath(shareID string) string {
	return filepath.Join(sharesRoot(), shareID+".json")
}

func (h *Handler) shareLimits() (int, int64) {
	defaultExpiryHours := 24
	maxFileSizeMb := 4096
	if h != nil && h.Settings != nil {
		if shares, ok := h.Settings.Raw()["shares"].(map[string]any); ok {
			defaultExpiryHours = int(numericMapOr(shares, "defaultExpiryHours", float64(defaultExpiryHours)))
			maxFileSizeMb = int(numericMapOr(shares, "maxFileSizeMb", float64(maxFileSizeMb)))
		}
	}
	if defaultExpiryHours <= 0 {
		defaultExpiryHours = 24
	}
	if maxFileSizeMb <= 0 {
		maxFileSizeMb = 4096
	}
	return defaultExpiryHours, int64(maxFileSizeMb) * 1024 * 1024
}

func (h *Handler) RegisterStorage(r chi.Router) {
	r.Get("/cached", h.storageAdminOnly(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"cached": false})
	}))
	r.Get("/scan", h.storageAdminOnly(h.storageScan))
	r.Post("/cleanup", httpx.Handler(h.storageAdminOnlyFunc(h.cleanupStorage)))
	r.Post("/database/preview", httpx.Handler(h.storageAdminOnlyFunc(h.databaseCleanupPreview)))
	r.Post("/database/cleanup", httpx.Handler(h.storageAdminOnlyFunc(h.databaseCleanup)))
	r.Post("/database/vacuum", httpx.Handler(h.storageAdminOnlyFunc(h.databaseVacuum)))
}

func (h *Handler) storageAdminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			httpx.WriteError(w, httpx.Forbidden("Admin access required"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) storageAdminOnlyFunc(next httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			return httpx.Forbidden("Admin access required")
		}
		return next(w, r)
	}
}

func (h *Handler) storageScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(event string, data any) {
		payload, _ := json.Marshal(data)
		_, _ = w.Write([]byte("event:" + event + "\ndata:" + string(payload) + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	categories := []map[string]any{}
	for _, item := range []struct {
		message string
		fn      func(*http.Request) map[string]any
	}{
		{"scanning_database", h.scanDatabaseStorage},
		{"scanning_uploads", h.scanUploadsStorage},
		{"scanning_shares", h.scanSharesStorage},
		{"scanning_worktrees", h.scanWorktreesStorage},
		{"scanning_containers", h.scanContainersStorage},
	} {
		send("progress", map[string]any{"message": item.message})
		category := item.fn(r)
		categories = append(categories, category)
		send("category", category)
	}
	total := int64(0)
	for _, category := range categories {
		total += int64Number(category["sizeBytes"])
	}
	send("complete", map[string]any{"categories": categories, "totalBytes": total, "scannedAt": time.Now().UnixMilli()})
}

func (h *Handler) cleanupStorage(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Target string `json:"target"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	switch body.Target {
	case "shares":
		memoryRemoved := shareRecords.clearMemory()
		removed, freed := removeChildren(sharesRoot())
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed, "freedBytes": freed, "memoryRemoved": memoryRemoved})
	case "uploads":
		removed, freed := h.cleanupOrphanedUploads(r)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed, "freedBytes": freed})
	case "worktrees":
		storageCleanupUnsupported(w, "storage.cleanup.worktrees", "STORAGE_CLEANUP_WORKTREES_UNSUPPORTED", "Storage cleanup does not remove git worktrees in go_backend; use chapter/runtime cleanup so git state and chapter metadata stay consistent.")
	case "containers":
		storageCleanupUnsupported(w, "storage.cleanup.containers", "STORAGE_CLEANUP_CONTAINERS_UNSUPPORTED", "Podman prune is disabled from storage cleanup in go_backend; use per-chapter container controls or an external maintenance command.")
	default:
		return httpx.BadRequest("Invalid target")
	}
	return nil
}

func storageCleanupUnsupported(w http.ResponseWriter, feature string, code string, reason string) {
	featureDisabledFallback(w, feature, code, reason)
}

func featureDisabledFallback(w http.ResponseWriter, feature string, code string, reason string) {
	httpx.WriteFeatureDisabledWithCode(w, feature, code, reason)
}

func rulerFeatureDisabled(w http.ResponseWriter, feature string, reason string) {
	featureDisabledFallback(w, "ruler."+feature, "FEATURE_DISABLED", reason)
}

func (h *Handler) scanDatabaseStorage(r *http.Request) map[string]any {
	breakdown := h.databaseStorageBreakdown(r)
	size := int64Number(breakdown["mainBytes"]) + int64Number(breakdown["walBytes"]) + int64Number(breakdown["shmBytes"])
	return map[string]any{"key": "database", "sizeBytes": size, "details": breakdown}
}

func (h *Handler) scanUploadsStorage(r *http.Request) map[string]any {
	root := uploadsRoot()
	total, _, truncated := dirSizeLimited(r.Context(), root, maxStorageScanEntries)
	narratorDirs := 0
	avatarBytes := int64(0)
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if entry.Name() == "avatars" {
				avatarSize, _, avatarTruncated := dirSizeLimited(r.Context(), filepath.Join(root, entry.Name()), maxStorageScanEntries)
				avatarBytes = avatarSize
				truncated = truncated || avatarTruncated
			} else {
				narratorDirs++
			}
		}
	}
	return map[string]any{"key": "uploads", "sizeBytes": total, "details": map[string]any{"narratorDirs": narratorDirs, "avatarBytes": avatarBytes, "narratorBytes": total - avatarBytes, "truncated": truncated}}
}

func (h *Handler) scanSharesStorage(r *http.Request) map[string]any {
	root := sharesRoot()
	count := 0
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				count++
			}
		}
	}
	size, _, truncated := dirSizeLimited(r.Context(), root, maxStorageScanEntries)
	return map[string]any{"key": "shares", "sizeBytes": size, "details": map[string]any{"shareCount": count, "truncated": truncated}}
}

func (h *Handler) scanWorktreesStorage(r *http.Request) map[string]any {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT name, git_path FROM projects WHERE git_path IS NOT NULL AND git_path != '' ORDER BY updated_at DESC LIMIT ?`, maxStorageProjectRows)
	if err != nil {
		return map[string]any{"key": "worktrees", "sizeBytes": 0, "details": map[string]any{"worktreeCount": 0, "projects": []any{}}}
	}
	defer rows.Close()
	projects := []map[string]any{}
	total := int64(0)
	count := 0
	for rows.Next() {
		var name, gitPath string
		if err := rows.Scan(&name, &gitPath); err != nil {
			continue
		}
		wtDir := filepath.Join(gitPath, ".worktrees")
		size, _, truncated := dirSizeLimited(r.Context(), wtDir, maxStorageScanEntries)
		projectCount := 0
		if entries, err := os.ReadDir(wtDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					projectCount++
				}
			}
		}
		if size > 0 || projectCount > 0 {
			projects = append(projects, map[string]any{"name": name, "sizeBytes": size, "worktreeCount": projectCount, "truncated": truncated})
		}
		total += size
		count += projectCount
	}
	return map[string]any{"key": "worktrees", "sizeBytes": total, "details": map[string]any{"worktreeCount": count, "projects": projects, "projectLimit": maxStorageProjectRows}}
}

func (h *Handler) scanContainersStorage(r *http.Request) map[string]any {
	return map[string]any{"key": "containers", "sizeBytes": 0, "details": map[string]any{"available": false}}
}

func (h *Handler) databaseStorageBreakdown(r *http.Request) map[string]any {
	main, wal, shm := databaseFileSizes()
	pageSize := h.pragmaInt(r, "page_size")
	pageCount := h.pragmaInt(r, "page_count")
	freelist := h.pragmaInt(r, "freelist_count")
	freelistBytes := int64(pageSize * freelist)
	topTables, categories := h.databaseTableSummaries(r)
	return map[string]any{
		"mainBytes":         main,
		"walBytes":          wal,
		"shmBytes":          shm,
		"pageSize":          pageSize,
		"pageCount":         pageCount,
		"freelistBytes":     freelistBytes,
		"objectBytes":       int64(pageSize*pageCount) - freelistBytes,
		"scanMode":          "approximate",
		"categories":        categories,
		"topTables":         topTables,
		"cleanupCandidates": h.databaseCleanupCandidateSummary(r),
	}
}

func (h *Handler) databaseTableSummaries(r *http.Request) ([]map[string]any, []map[string]any) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name LIMIT ?`, maxDatabaseSummaryTables)
	if err != nil {
		return []map[string]any{}, []map[string]any{}
	}
	defer rows.Close()
	tables := []map[string]any{}
	categoryRows := map[string]int64{}
	categoryTables := map[string]int{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		rowCount, countErr := h.tableRowCount(r, name)
		category := databaseTableCategory(name)
		categoryRows[category] += rowCount
		categoryTables[category]++
		tableSummary := map[string]any{"name": name, "category": category, "kind": "table", "rowCount": rowCount, "approxContentBytes": 0, "diskBytes": 0, "indexBytes": 0, "totalBytes": 0}
		if countErr != nil {
			tableSummary["degraded"] = true
			tableSummary["fallback"] = true
			tableSummary["fallbacks"] = []any{"table_row_count_failed"}
			tableSummary["error"] = countErr.Error()
		}
		tables = append(tables, tableSummary)
	}
	categories := []map[string]any{}
	for category, rowCount := range categoryRows {
		categories = append(categories, map[string]any{"key": category, "tableCount": categoryTables[category], "rowCount": rowCount, "approxContentBytes": 0, "diskBytes": 0, "indexBytes": 0, "totalBytes": 0, "tableLimit": maxDatabaseSummaryTables})
	}
	return tables, categories
}

func (h *Handler) databaseCleanupCandidateSummary(r *http.Request) map[string]any {
	archived := h.previewDatabaseCleanup(r, "archivedSessions", 0, 0)
	stale := h.previewDatabaseCleanup(r, "staleSessions", 90, 0)
	dumps := h.previewDatabaseCleanup(r, "apiRequestDumps", 30, 0)
	return map[string]any{
		"archivedSessions": candidateSummaryFromPreview(archived, 0),
		"staleSessions":    candidateSummaryFromPreview(stale, 90),
		"apiRequestDumps":  candidateSummaryFromPreview(dumps, 30),
	}
}

func candidateSummaryFromPreview(preview map[string]any, retentionDays int) map[string]any {
	summary := map[string]any{"count": int64Number(preview["count"]), "approxBytes": int64Number(preview["approxBytes"]), "blockedCount": int64Number(preview["blockedCount"]), "oldestAt": preview["oldestAt"]}
	if retentionDays > 0 {
		summary["retentionDays"] = retentionDays
	}
	return summary
}

func (h *Handler) databaseCleanupPreview(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Target        string `json:"target"`
		OlderThanDays int    `json:"olderThanDays"`
		SampleLimit   int    `json:"sampleLimit"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	if body.SampleLimit <= 0 {
		body.SampleLimit = 10
	}
	if body.OlderThanDays <= 0 {
		body.OlderThanDays = defaultDatabaseRetentionDays(body.Target)
	}
	preview := h.previewDatabaseCleanup(r, body.Target, body.OlderThanDays, body.SampleLimit)
	if preview == nil {
		return httpx.BadRequest("Invalid database cleanup target")
	}
	httpx.WriteJSON(w, http.StatusOK, preview)
	return nil
}

func (h *Handler) databaseCleanup(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Target        string `json:"target"`
		OlderThanDays int    `json:"olderThanDays"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	if body.OlderThanDays <= 0 {
		body.OlderThanDays = defaultDatabaseRetentionDays(body.Target)
	}
	before, _, _ := databaseFileSizes()
	preview := h.previewDatabaseCleanup(r, body.Target, body.OlderThanDays, 10)
	if preview == nil {
		return httpx.BadRequest("Invalid database cleanup target")
	}
	changed := false
	switch body.Target {
	case "apiRequestDumps":
		cutoff := time.Now().AddDate(0, 0, -body.OlderThanDays).Format(time.RFC3339)
		res, err := h.DB.ExecContext(r.Context(), `UPDATE api_requests SET raw_dump_json = NULL WHERE raw_dump_json IS NOT NULL AND raw_dump_json != '' AND created_at < ?`, cutoff)
		if err != nil {
			return err
		}
		changed = rowsAffected(res) > 0
	case "archivedSessions", "staleSessions":
		ids, err := h.databaseCleanupNarratorIDs(r, body.Target, body.OlderThanDays)
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := h.deleteNarratorSessions(r, ids); err != nil {
				return err
			}
			changed = true
		}
	}
	after, _, _ := databaseFileSizes()
	preview["ok"] = true
	preview["beforeBytes"] = before
	preview["afterBytes"] = after
	preview["freedBytes"] = maxInt64(0, before-after)
	preview["vacuumRan"] = false
	preview["changed"] = changed
	httpx.WriteJSON(w, http.StatusOK, preview)
	return nil
}

func (h *Handler) databaseVacuum(w http.ResponseWriter, r *http.Request) error {
	main, _, _ := databaseFileSizes()
	freelistBefore := int64(h.pragmaInt(r, "freelist_count") * h.pragmaInt(r, "page_size"))
	httpx.WriteJSON(w, http.StatusConflict, map[string]any{
		"error":               "SQLite VACUUM is disabled in the request path",
		"message":             "Full database compaction must run as a background maintenance job to avoid freezing the backend.",
		"vacuumRan":           false,
		"checkpointRan":       false,
		"optimized":           false,
		"beforeBytes":         main,
		"afterBytes":          main,
		"freedBytes":          0,
		"freelistBeforeBytes": freelistBefore,
		"freelistAfterBytes":  freelistBefore,
		"durationMs":          0,
	})
	return nil
}

func (h *Handler) previewDatabaseCleanup(r *http.Request, target string, olderThanDays int, sampleLimit int) map[string]any {
	if sampleLimit < 0 {
		sampleLimit = 0
	}
	base := map[string]any{"target": target, "olderThanDays": olderThanDays, "approxBytes": int64(0), "oldestAt": nil, "counts": emptyDatabaseCleanupCounts(), "blockedCount": 0, "warningCodes": []any{}, "fallbacks": []any{}, "degraded": false, "samples": []any{}, "blocked": []any{}, "count": int64(0)}
	switch target {
	case "apiRequestDumps":
		cutoff := time.Now().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)
		var count, bytesValue int64
		var oldest sql.NullString
		if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(LENGTH(raw_dump_json)), 0), MIN(created_at) FROM api_requests WHERE raw_dump_json IS NOT NULL AND raw_dump_json != '' AND created_at < ?`, cutoff).Scan(&count, &bytesValue, &oldest); err != nil {
			markDatabaseCleanupPreviewDegraded(base, "api_request_dump_stats_failed", err)
			return base
		}
		counts := emptyDatabaseCleanupCounts()
		counts["dumpsCleared"] = count
		base["counts"] = counts
		base["count"] = count
		base["approxBytes"] = bytesValue
		base["oldestAt"] = nullSQLString(oldest)
		base["warningCodes"] = []any{"deletesUsageHistory"}
		base["samples"] = h.apiRequestDumpSamples(r, cutoff, sampleLimit)
		return base
	case "archivedSessions", "staleSessions":
		ids, err := h.databaseCleanupNarratorIDs(r, target, olderThanDays)
		if err != nil {
			markDatabaseCleanupPreviewDegraded(base, "cleanup_narrator_id_query_failed", err)
			return base
		}
		counts := emptyDatabaseCleanupCounts()
		counts["sessions"] = len(ids)
		counts["narrators"] = len(ids)
		messages, err := h.countForNarratorIDs(r, "narrator_messages", ids)
		if err != nil {
			markDatabaseCleanupPreviewDegraded(base, "cleanup_message_count_failed", err)
			return base
		}
		toolCalls, err := h.countForNarratorIDs(r, "narrator_tool_calls", ids)
		if err != nil {
			markDatabaseCleanupPreviewDegraded(base, "cleanup_tool_call_count_failed", err)
			return base
		}
		apiRequests, err := h.countForNarratorIDs(r, "api_requests", ids)
		if err != nil {
			markDatabaseCleanupPreviewDegraded(base, "cleanup_api_request_count_failed", err)
			return base
		}
		counts["messages"] = messages
		counts["toolCalls"] = toolCalls
		counts["apiRequests"] = apiRequests
		base["counts"] = counts
		base["count"] = int64(len(ids))
		base["approxBytes"] = int64(messages+toolCalls+apiRequests) * 1024
		oldestAt, err := h.oldestNarratorActivity(r, ids)
		if err != nil {
			markDatabaseCleanupPreviewDegraded(base, "cleanup_oldest_activity_failed", err)
			return base
		}
		base["oldestAt"] = oldestAt
		if len(ids) >= maxCleanupNarratorIDs {
			base["warningCodes"] = []any{"cleanupPreviewLimited"}
			base["truncated"] = true
			base["limit"] = maxCleanupNarratorIDs
		}
		base["samples"] = h.narratorCleanupSamples(r, ids, sampleLimit)
		return base
	default:
		return nil
	}
}

func emptyDatabaseCleanupCounts() map[string]any {
	return map[string]any{"sessions": 0, "narrators": 0, "descendantNarrators": 0, "messages": 0, "toolCalls": 0, "apiRequests": 0, "dumpsCleared": 0}
}

func markDatabaseCleanupPreviewDegraded(preview map[string]any, code string, err error) {
	preview["degraded"] = true
	preview["fallback"] = true
	preview["fallbacks"] = []any{code}
	preview["warningCodes"] = []any{code}
	if err != nil {
		preview["error"] = err.Error()
	}
}

func (h *Handler) databaseCleanupNarratorIDs(r *http.Request, target string, olderThanDays int) ([]string, error) {
	ids := []string{}
	var rows *sql.Rows
	var err error
	if target == "archivedSessions" {
		rows, err = h.DB.QueryContext(r.Context(), `SELECT id FROM narrators WHERE status = 'archived' ORDER BY updated_at ASC LIMIT ?`, maxCleanupNarratorIDs)
	} else {
		cutoff := time.Now().AddDate(0, 0, -olderThanDays).Format(time.RFC3339)
		rows, err = h.DB.QueryContext(r.Context(), `SELECT id FROM narrators WHERE status NOT IN ('working', 'archived') AND COALESCE(last_message_at, updated_at, created_at) < ? ORDER BY COALESCE(last_message_at, updated_at, created_at) ASC LIMIT ?`, cutoff, maxCleanupNarratorIDs)
	}
	if err != nil {
		return ids, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (h *Handler) deleteNarratorSessions(r *http.Request, ids []string) error {
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tables := []string{"narrator_message_refs", "narrator_tool_calls", "narrator_sidecars", "api_requests", "terminal_view_state", "terminal_tabs", "terminals", "narrator_buffered_messages", "narrator_goals", "narrator_file_snapshots", "narrator_patches", "narrator_whitelist_dirs", "narrator_blacklist_dirs", "narrator_whitelist_cmds", "narrator_blacklist_cmds", "gateway_session_mappings"}
	for _, narratorID := range ids {
		for _, table := range tables {
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE narrator_id = ?`, narratorID); err != nil {
				return fmt.Errorf("delete %s for narrator %s: %w", table, narratorID, err)
			}
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM narrator_messages WHERE narrator_id = ?`, narratorID); err != nil {
			return fmt.Errorf("delete narrator_messages for narrator %s: %w", narratorID, err)
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM narrators WHERE id = ?`, narratorID); err != nil {
			return fmt.Errorf("delete narrator %s: %w", narratorID, err)
		}
	}
	return tx.Commit()
}

func (h *Handler) countForNarratorIDs(r *http.Request, table string, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	total := 0
	for start := 0; start < len(ids); start += 100 {
		end := minInt(len(ids), start+100)
		chunk := ids[start:end]
		placeholders, args := sqlInPlaceholders(chunk)
		var count int
		if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM `+table+` WHERE narrator_id IN (`+placeholders+`)`, args...).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (h *Handler) oldestNarratorActivity(r *http.Request, ids []string) (any, error) {
	var oldest sql.NullString
	for _, id := range ids {
		var value sql.NullString
		if err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(last_message_at, updated_at, created_at) FROM narrators WHERE id = ?`, id).Scan(&value); err != nil {
			return nil, err
		}
		if value.Valid && (!oldest.Valid || value.String < oldest.String) {
			oldest = value
		}
	}
	return nullSQLString(oldest), nil
}

func (h *Handler) narratorCleanupSamples(r *http.Request, ids []string, limit int) []any {
	samples := []any{}
	for _, id := range ids {
		if len(samples) >= limit {
			break
		}
		var title, status, last sql.NullString
		var messageCount int
		sample := map[string]any{"type": "narrator", "id": id, "descendantNarratorCount": 0}
		if err := h.DB.QueryRowContext(r.Context(), `SELECT title, status, COALESCE(last_message_at, updated_at, created_at), message_count FROM narrators WHERE id = ?`, id).Scan(&title, &status, &last, &messageCount); err != nil {
			sample["degraded"] = true
			sample["fallback"] = true
			sample["fallbacks"] = []any{"narrator_cleanup_sample_query_failed"}
			sample["error"] = err.Error()
		} else {
			sample["title"] = nullSQLString(title)
			sample["status"] = nullSQLString(status)
			sample["lastActivityAt"] = nullSQLString(last)
			sample["messageCount"] = messageCount
			sample["approxBytes"] = int64(messageCount * 1024)
		}
		samples = append(samples, sample)
	}
	return samples
}

func (h *Handler) apiRequestDumpSamples(r *http.Request, cutoff string, limit int) []any {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT ar.id, ar.narrator_id, n.title, c.title, ar.created_at, LENGTH(ar.raw_dump_json) FROM api_requests ar LEFT JOIN narrators n ON n.id = ar.narrator_id LEFT JOIN chapters c ON c.id = n.chapter_id WHERE ar.raw_dump_json IS NOT NULL AND ar.raw_dump_json != '' AND ar.created_at < ? ORDER BY ar.created_at ASC LIMIT ?`, cutoff, limit)
	if err != nil {
		return []any{}
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, createdAt string
		var narratorID, narratorTitle, chapterTitle sql.NullString
		var bytesValue int64
		if err := rows.Scan(&id, &narratorID, &narratorTitle, &chapterTitle, &createdAt, &bytesValue); err != nil {
			continue
		}
		items = append(items, map[string]any{"type": "apiRequest", "id": id, "narratorId": nullSQLString(narratorID), "narratorTitle": nullSQLString(narratorTitle), "chapterTitle": nullSQLString(chapterTitle), "createdAt": createdAt, "approxBytes": bytesValue})
	}
	return items
}

func defaultDatabaseRetentionDays(target string) int {
	if target == "apiRequestDumps" {
		return 30
	}
	if target == "staleSessions" {
		return 90
	}
	return 0
}

func databaseFileSizes() (int64, int64, int64) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".narrafork", "narrafork.db")
	return fileSize(base), fileSize(base + "-wal"), fileSize(base + "-shm")
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func (h *Handler) pragmaInt(r *http.Request, name string) int {
	var value int
	_ = h.DB.QueryRowContext(r.Context(), `PRAGMA `+name).Scan(&value)
	return value
}

func dirSize(root string) int64 {
	total, _, _ := dirSizeLimited(context.Background(), root, maxStorageScanEntries)
	return total
}

func dirSizeLimited(ctx context.Context, root string, maxEntries int) (int64, int, bool) {
	total := int64(0)
	files := 0
	truncated := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil {
			return nil
		}
		if ctx.Err() != nil {
			truncated = true
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		if maxEntries > 0 && files >= maxEntries {
			truncated = true
			return filepath.SkipAll
		}
		files++
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, files, truncated
}

func removeChildren(root string) (int, int64) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	removed := 0
	freed := int64(0)
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		size := dirSize(path)
		if !entry.IsDir() {
			size = fileSize(path)
		}
		if err := os.RemoveAll(path); err == nil {
			removed++
			freed += size
		}
	}
	return removed, freed
}

func (h *Handler) cleanupOrphanedUploads(r *http.Request) (int, int64) {
	root := uploadsRoot()
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id FROM narrators`)
	if err != nil {
		return 0, 0
	}
	active := map[string]bool{"avatars": true}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && strings.TrimSpace(id) != "" {
			active[id] = true
		}
	}
	rows.Close()
	h.addReferencedUploadOwnerIDs(r, active)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	removed := 0
	freed := int64(0)
	for _, entry := range entries {
		if !entry.IsDir() || active[entry.Name()] {
			continue
		}
		path := filepath.Join(root, entry.Name())
		size := dirSize(path)
		if err := os.RemoveAll(path); err == nil {
			removed++
			freed += size
		}
	}
	return removed, freed
}

func (h *Handler) addReferencedUploadOwnerIDs(r *http.Request, active map[string]bool) {
	if h == nil || h.DB == nil {
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT narrator_id, content_json FROM narrator_messages WHERE narrator_id IS NOT NULL`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var narratorID string
		var contentJSON string
		if err := rows.Scan(&narratorID, &contentJSON); err != nil {
			continue
		}
		if strings.TrimSpace(narratorID) != "" && contentJSONHasImageBlocks(contentJSON) {
			active[narratorID] = true
		}
	}
}

func contentJSONHasImageBlocks(raw string) bool {
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		blockType, _ := block["type"].(string)
		imageID, _ := block["imageId"].(string)
		if blockType == "image" && strings.TrimSpace(imageID) != "" {
			return true
		}
	}
	return false
}

func databaseTableCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "narrator") || strings.HasPrefix(name, "terminal") || name == "api_requests":
		return "sessions"
	case strings.HasPrefix(name, "project") || strings.HasPrefix(name, "chapter"):
		return "projects"
	case strings.HasPrefix(name, "user"):
		return "users"
	case strings.Contains(name, "fts"):
		return "search"
	case strings.HasPrefix(name, "gateway"):
		return "gateway"
	case strings.HasPrefix(name, "benchmark"):
		return "benchmarks"
	default:
		return "other"
	}
}

func int64Number(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func rowsAffected(res sql.Result) int64 {
	count, _ := res.RowsAffected()
	return count
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sqlInPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		args[i] = value
	}
	return strings.Join(placeholders, ","), args
}

func intMapOr(values url.Values, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	if v := strings.TrimSpace(values.Get(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func stringMap(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func numericMapOr(m map[string]any, key string, fallback float64) float64 {
	if m == nil {
		return fallback
	}
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if n, err := v.Float64(); err == nil {
			return n
		}
	}
	return fallback
}

func stringMapOr(items []map[string]any, idx int, key, fallback string) string {
	if idx < 0 || idx >= len(items) {
		return fallback
	}
	return stringMap(items[idx], key, fallback)
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullSQLString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func (h *Handler) tableExists(ctx context.Context, table string) bool {
	var name string
	err := h.DB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	return err == nil && name == table
}

func (h *Handler) tableRowCount(r *http.Request, table string) (int64, error) {
	var count int64
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) RegisterNotifications(r chi.Router) {
	r.Post("/test-dingtalk", httpx.Handler(h.testDingtalkWebhook))
	r.Post("/test-feishu", httpx.Handler(h.testFeishuWebhook))
}

func (h *Handler) testDingtalkWebhook(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Webhook string `json:"webhook"`
		Secret  string `json:"secret"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Webhook) == "" {
		return httpx.BadRequest("webhook is required")
	}
	if err := sendDingtalkWebhook(r.Context(), req.Webhook, req.Secret, "NarraFork: Test", "**Test Notification**\n\nDingTalk webhook is working."); err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) testFeishuWebhook(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Webhook string `json:"webhook"`
		Secret  string `json:"secret"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Webhook) == "" {
		return httpx.BadRequest("webhook is required")
	}
	if err := sendFeishuWebhook(r.Context(), req.Webhook, req.Secret, "NarraFork Notification", "**Test Notification**\n\nFeishu webhook is working."); err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func sendDingtalkWebhook(ctx context.Context, webhook, secret, title, text string) error {
	endpoint := webhook
	if strings.TrimSpace(secret) != "" {
		timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp + "\n" + secret))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		separator := "&"
		if !strings.Contains(endpoint, "?") {
			separator = "?"
		}
		endpoint = endpoint + separator + "timestamp=" + url.QueryEscape(timestamp) + "&sign=" + url.QueryEscape(sign)
	}
	payload, _ := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"title": title, "text": text},
	})
	return postWebhook(ctx, endpoint, payload, "DingTalk")
}

func sendFeishuWebhook(ctx context.Context, webhook, secret, title, content string) error {
	body := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header":   map[string]any{"title": map[string]any{"tag": "plain_text", "content": title}},
			"elements": []any{map[string]any{"tag": "markdown", "content": content}},
		},
	}
	if strings.TrimSpace(secret) != "" {
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		body["timestamp"] = timestamp
		body["sign"] = sign
	}
	payload, _ := json.Marshal(body)
	return postWebhook(ctx, webhook, payload, "Feishu")
}

func postWebhook(ctx context.Context, endpoint string, payload []byte, provider string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s webhook failed: %d %s", provider, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (h *Handler) RegisterReviews(r chi.Router) {
	r.Get("/by-source/{sourceChapterId}/conclusion", httpx.Handler(h.getReviewConclusionBySource))
	r.Get("/{id}/conclusion", httpx.Handler(h.getReviewConclusion))
	r.Post("/{id}/convert-to-subagent", httpx.Handler(h.convertReviewToSubagent))
	r.Post("/{id}/promote", httpx.Handler(h.promoteReview))
	r.Post("/{id}/dismiss", httpx.Handler(h.dismissReview))
}

func (h *Handler) getReviewConclusion(w http.ResponseWriter, r *http.Request) error {
	conclusion, err := h.latestReviewConclusion(r.Context(), "review", chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conclusion": conclusion})
	return nil
}

func (h *Handler) getReviewConclusionBySource(w http.ResponseWriter, r *http.Request) error {
	conclusion, err := h.latestReviewConclusion(r.Context(), "source", chi.URLParam(r, "sourceChapterId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conclusion": conclusion})
	return nil
}

func (h *Handler) latestReviewConclusion(ctx context.Context, mode, chapterID string) (any, error) {
	column := "review_chapter_id"
	if mode == "source" {
		column = "source_chapter_id"
	}
	row := h.DB.QueryRowContext(ctx, `SELECT id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at FROM review_conclusions WHERE `+column+` = ? ORDER BY created_at DESC LIMIT 1`, chapterID)
	var idValue, reviewChapterID, sourceChapterID, verdict, createdAt string
	var findingsRaw sql.NullString
	if err := row.Scan(&idValue, &reviewChapterID, &sourceChapterID, &verdict, &findingsRaw, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	findings := []any{}
	if findingsRaw.Valid && findingsRaw.String != "" {
		_ = json.Unmarshal([]byte(findingsRaw.String), &findings)
	}
	return map[string]any{
		"id": idValue, "reviewChapterId": reviewChapterID, "sourceChapterId": sourceChapterID,
		"verdict": verdict, "findingsJson": findings, "findings": findings, "createdAt": createdAt,
	}, nil
}

type reviewChapterRecord struct {
	ID                    string
	ProjectID             string
	Role                  string
	ReviewStatus          sql.NullString
	ReviewSourceChapterID sql.NullString
	WorktreePath          sql.NullString
	Branch                string
}

func (h *Handler) dismissReview(w http.ResponseWriter, r *http.Request) error {
	reviewID := chi.URLParam(r, "id")
	chapter, err := h.getReviewChapter(r.Context(), reviewID)
	if err != nil {
		return err
	}
	if chapter.ReviewStatus.String == "converted" {
		return httpx.BadRequest("Cannot dismiss a converted review")
	}
	now := db.NowISO()
	h.cleanupReviewGit(r.Context(), chapter)
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE narrators SET status = 'archived', updated_at = ? WHERE chapter_id = ?`, now, reviewID); err != nil {
		return err
	}
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE chapters SET review_status = 'dismissed', status = 'abandoned', worktree_path = NULL, updated_at = ? WHERE id = ?`, now, reviewID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ok": true})
	return nil
}

func (h *Handler) convertReviewToSubagent(w http.ResponseWriter, r *http.Request) error {
	reviewID := chi.URLParam(r, "id")
	chapter, err := h.getReviewChapter(r.Context(), reviewID)
	if err != nil {
		return err
	}
	if chapter.ReviewStatus.String != "concluded" {
		return httpx.BadRequest("Review must be concluded before converting to subagent")
	}
	if !chapter.ReviewSourceChapterID.Valid || chapter.ReviewSourceChapterID.String == "" {
		return httpx.BadRequest("Review has no source chapter")
	}
	reviewNarratorID, err := h.primaryNarratorID(r.Context(), reviewID)
	if err != nil {
		return err
	}
	sourceNarratorID, err := h.primaryNarratorID(r.Context(), chapter.ReviewSourceChapterID.String)
	if err != nil {
		return httpx.BadRequest("Source chapter has no narrator")
	}
	sourceCwd, err := h.sourceChapterCwd(r.Context(), chapter.ReviewSourceChapterID.String, sourceNarratorID)
	if err != nil {
		return err
	}
	now := db.NowISO()
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE narrators SET type = 'subagent', variant = 'subagent:review', subagent_type = 'review', parent_narrator_id = ?, chapter_id = ?, cwd = ?, status = 'idle', substatus = ?, updated_at = ? WHERE id = ?`, sourceNarratorID, chapter.ReviewSourceChapterID.String, nullableString(sourceCwd), `["unread"]`, now, reviewNarratorID); err != nil {
		return err
	}
	h.cleanupReviewGit(r.Context(), chapter)
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE chapters SET review_status = 'converted', status = 'abandoned', worktree_path = NULL, updated_at = ? WHERE id = ?`, now, reviewID); err != nil {
		return err
	}
	narrator, err := h.singleMap(r.Context(), `SELECT * FROM narrators WHERE id = ?`, reviewNarratorID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"narrator": narrator})
	return nil
}

func (h *Handler) promoteReview(w http.ResponseWriter, r *http.Request) error {
	reviewID := chi.URLParam(r, "id")
	chapter, err := h.getReviewChapter(r.Context(), reviewID)
	if err != nil {
		return err
	}
	status := chapter.ReviewStatus.String
	if status != "concluded" && status != "reviewing" {
		return httpx.BadRequest("Review must be in reviewing or concluded state to promote")
	}
	now := db.NowISO()
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE chapters SET role = 'branch', review_status = 'converted', review_source_chapter_id = NULL, updated_at = ? WHERE id = ?`, now, reviewID); err != nil {
		return err
	}
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE chapter_edges SET type = 'fork' WHERE target_id = ? AND type = 'review'`, reviewID); err != nil {
		return err
	}
	chapterMap, err := h.singleMap(r.Context(), `SELECT * FROM chapters WHERE id = ?`, reviewID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chapter": chapterMap})
	return nil
}

func (h *Handler) getReviewChapter(ctx context.Context, reviewID string) (reviewChapterRecord, error) {
	var ch reviewChapterRecord
	err := h.DB.QueryRowContext(ctx, `SELECT id, project_id, role, review_status, review_source_chapter_id, worktree_path, branch FROM chapters WHERE id = ?`, reviewID).Scan(&ch.ID, &ch.ProjectID, &ch.Role, &ch.ReviewStatus, &ch.ReviewSourceChapterID, &ch.WorktreePath, &ch.Branch)
	if err == sql.ErrNoRows {
		return ch, httpx.BadRequest("Not a review chapter")
	}
	if err != nil {
		return ch, err
	}
	if ch.Role != "review" {
		return ch, httpx.BadRequest("Not a review chapter")
	}
	return ch, nil
}

func (h *Handler) primaryNarratorID(ctx context.Context, chapterID string) (string, error) {
	var narratorID string
	err := h.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID)
	if err == sql.ErrNoRows {
		return "", httpx.BadRequest("Review has no narrator")
	}
	return narratorID, err
}

func (h *Handler) sourceChapterCwd(ctx context.Context, chapterID, narratorID string) (string, error) {
	var worktree, cwd sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT worktree_path FROM chapters WHERE id = ?`, chapterID).Scan(&worktree); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if worktree.Valid && worktree.String != "" {
		return worktree.String, nil
	}
	if err := h.DB.QueryRowContext(ctx, `SELECT cwd FROM narrators WHERE id = ?`, narratorID).Scan(&cwd); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if cwd.Valid {
		return cwd.String, nil
	}
	return "", nil
}

func (h *Handler) cleanupReviewGit(ctx context.Context, ch reviewChapterRecord) {
	if h.Git == nil || !ch.WorktreePath.Valid || ch.WorktreePath.String == "" {
		return
	}
	var gitPath sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT git_path FROM projects WHERE id = ?`, ch.ProjectID).Scan(&gitPath); err != nil || !gitPath.Valid || gitPath.String == "" {
		return
	}
	if !safeReviewWorktreePath(gitPath.String, ch.WorktreePath.String) {
		return
	}
	_ = h.Git.RemoveWorktree(ctx, gitPath.String, ch.WorktreePath.String)
	if ch.Branch != "" {
		_ = h.Git.DeleteBranch(ctx, gitPath.String, ch.Branch)
	}
}

func safeReviewWorktreePath(repoPath, worktreePath string) bool {
	repoAbs, err := filepath.Abs(repoPath)
	if err != nil {
		return false
	}
	worktreeAbs, err := filepath.Abs(worktreePath)
	if err != nil {
		return false
	}
	worktreesRoot := filepath.Join(repoAbs, ".worktrees")
	rel, err := filepath.Rel(worktreesRoot, worktreeAbs)
	return err == nil && rel != "." && rel != "" && !strings.HasPrefix(rel, "..")
}

func (h *Handler) singleMap(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, httpx.NotFound("record")
	}
	return items[0], nil
}

func (h *Handler) rulerFork(w http.ResponseWriter, r *http.Request) error {
	if h.Chapters == nil {
		rulerFeatureDisabled(w, "fork", "Ruler fork requires chapter service support that is not available in this go_backend runtime")
		return nil
	}
	projectID := chi.URLParam(r, "projectId")
	var body struct {
		StartCommitSHA  string   `json:"startCommitSha"`
		Title           *string  `json:"title"`
		Description     *string  `json:"description"`
		Role            string   `json:"role"`
		InheritMode     string   `json:"inheritMode"`
		ParentChapterID string   `json:"parentChapterId"`
		AnchorCommitSHA *string  `json:"anchorCommitSha"`
		AxisOffset      *float64 `json:"axisOffset"`
		CrossOffset     *float64 `json:"crossOffset"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	start := strings.TrimSpace(body.StartCommitSHA)
	if start == "" {
		return httpx.BadRequest("startCommitSha is required for ruler fork")
	}
	parentID := strings.TrimSpace(body.ParentChapterID)
	var err error
	if parentID == "" {
		parentID, err = h.rootChapterID(r.Context(), projectID)
		if err != nil {
			return err
		}
	} else if ok, err := h.chapterBelongsToProject(r.Context(), parentID, projectID); err != nil {
		return err
	} else if !ok {
		return httpx.NotFound("Chapter")
	}
	if split, err := h.rulerForkShouldSplit(r.Context(), projectID, parentID, start); err != nil {
		return err
	} else if split {
		title := strings.TrimSpace(stringPtrValue(body.Title))
		if title == "" {
			short := start
			if len(short) > 8 {
				short = short[:8]
			}
			title = "Fork from " + short
		}
		result, err := h.Chapters.SplitAtCommit(r.Context(), parentID, chapters.SplitInput{
			CommitSHA: start,
			NewFork: chapters.SplitNewForkInput{
				Title:       title,
				Description: body.Description,
				InheritMode: body.InheritMode,
			},
			AnchorCommitSHA: body.AnchorCommitSHA,
			AxisOffset:      body.AxisOffset,
			CrossOffset:     body.CrossOffset,
		})
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusCreated, result.NewForkChapter)
		return nil
	}
	chapter, err := h.Chapters.Fork(r.Context(), parentID, chapters.ForkInput{
		Title: body.Title, Description: body.Description, Role: body.Role, InheritMode: body.InheritMode,
		StartCommitSHA: &start, AnchorCommitSHA: body.AnchorCommitSHA,
		AxisOffset: body.AxisOffset, CrossOffset: body.CrossOffset,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, chapter)
	return nil
}

func (h *Handler) rulerForkShouldSplit(ctx context.Context, projectID, parentID, startCommitSHA string) (bool, error) {
	gitService := h.Git
	if gitService == nil && h.Chapters != nil {
		gitService = h.Chapters.Git
	}
	if gitService == nil {
		return false, nil
	}
	database := h.DB
	if database == nil && h.Chapters != nil {
		database = h.Chapters.DB
	}
	if database == nil {
		return false, nil
	}
	var worktreePath, branch, gitPath sql.NullString
	err := database.QueryRowContext(ctx, `SELECT c.worktree_path, c.branch, p.git_path FROM chapters c JOIN projects p ON p.id = c.project_id WHERE c.id = ? AND c.project_id = ?`, parentID, projectID).Scan(&worktreePath, &branch, &gitPath)
	if errors.Is(err, sql.ErrNoRows) {
		return false, httpx.NotFound("Chapter")
	}
	if err != nil {
		return false, err
	}
	if !gitPath.Valid || strings.TrimSpace(gitPath.String) == "" {
		return false, nil
	}
	repoPath := strings.TrimSpace(gitPath.String)
	head := ""
	if worktreePath.Valid && strings.TrimSpace(worktreePath.String) != "" {
		head, err = gitService.HeadCommit(ctx, strings.TrimSpace(worktreePath.String))
	} else if branch.Valid && strings.TrimSpace(branch.String) != "" {
		head, err = gitService.GetRefCommit(ctx, repoPath, strings.TrimSpace(branch.String))
	} else {
		head, err = gitService.HeadCommit(ctx, repoPath)
	}
	if err != nil {
		return false, err
	}
	head = strings.TrimSpace(head)
	if head == "" {
		return false, nil
	}
	start, err := gitService.GetRefCommit(ctx, repoPath, strings.TrimSpace(startCommitSHA))
	if err != nil {
		return false, nil
	}
	start = strings.TrimSpace(start)
	if start == "" || start == head {
		return false, nil
	}
	return gitService.IsAncestor(ctx, repoPath, start, head), nil
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (h *Handler) rulerMerge(w http.ResponseWriter, r *http.Request) error {
	if h.Chapters == nil {
		rulerFeatureDisabled(w, "merge", "Ruler merge requires chapter service support that is not available in this go_backend runtime")
		return nil
	}
	projectID := chi.URLParam(r, "projectId")
	var body struct {
		SourceChapterID string `json:"sourceChapterId"`
		Strategy        string `json:"strategy"`
		Message         string `json:"message"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sourceID := strings.TrimSpace(body.SourceChapterID)
	if sourceID == "" {
		return httpx.BadRequest("sourceChapterId is required")
	}
	if ok, err := h.chapterBelongsToProject(r.Context(), sourceID, projectID); err != nil {
		return err
	} else if !ok {
		return httpx.NotFound("Chapter")
	}
	if err := h.ensureSourceWorktreeClean(r.Context(), sourceID); err != nil {
		return err
	}
	targetID, err := h.rootChapterID(r.Context(), projectID)
	if err != nil {
		return err
	}
	result, err := h.Chapters.Merge(r.Context(), sourceID, targetID, body.Strategy, body.Message, optionalUserID(r))
	if err != nil {
		return err
	}
	if ok, _ := result["ok"].(bool); !ok {
		aiResult, err := h.Chapters.AIResolve(r.Context(), sourceID, targetID, body.Strategy, body.Message, optionalUserID(r), "")
		if err != nil {
			return err
		}
		if resolved, _ := aiResult["resolved"].(bool); resolved {
			response := map[string]any{}
			if mergeResult, ok := aiResult["mergeResult"].(map[string]any); ok {
				for key, value := range mergeResult {
					response[key] = value
				}
			}
			response["success"] = true
			response["aiResolved"] = true
			response["resolvedConflictFiles"] = result["conflictFiles"]
			h.deleteRulerMergedBranch(r.Context(), projectID, sourceID)
			httpx.WriteJSON(w, http.StatusOK, response)
			return nil
		}
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"success":        false,
			"conflictFiles":  result["conflictFiles"],
			"aiAttempted":    true,
			"aiError":        aiResult["error"],
			"remainingFiles": aiResult["remainingFiles"],
		})
		return nil
	}
	result["success"] = true
	h.deleteRulerMergedBranch(r.Context(), projectID, sourceID)
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) rulerRebase(w http.ResponseWriter, r *http.Request) error {
	if h.Git == nil {
		rulerFeatureDisabled(w, "rebase", "Ruler rebase requires git service support that is not available in this go_backend runtime")
		return nil
	}
	projectID := chi.URLParam(r, "projectId")
	var body struct {
		ChapterID string `json:"chapterId"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	chapterID := strings.TrimSpace(body.ChapterID)
	if chapterID == "" {
		return httpx.BadRequest("chapterId is required")
	}
	if ok, err := h.chapterBelongsToProject(r.Context(), chapterID, projectID); err != nil {
		return err
	} else if !ok {
		return httpx.NotFound("Chapter")
	}
	var status string
	var worktree sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT status, worktree_path FROM chapters WHERE id = ?`, chapterID).Scan(&status, &worktree); err != nil {
		return err
	}
	if status != "active" {
		return httpx.BadRequest("Can only rebase active chapters")
	}
	if !worktree.Valid || worktree.String == "" {
		return httpx.BadRequest("Chapter has no worktree")
	}
	if err := h.ensureWorktreeClean(r.Context(), worktree.String, "REBASE_DIRTY_SOURCE"); err != nil {
		return err
	}
	rootID, err := h.rootChapterID(r.Context(), projectID)
	if err != nil {
		return err
	}
	var rootWorktree sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT worktree_path FROM chapters WHERE id = ?`, rootID).Scan(&rootWorktree); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if rootWorktree.Valid && rootWorktree.String != "" {
		if err := h.ensureWorktreeClean(r.Context(), rootWorktree.String, "REBASE_DIRTY_TRUNK"); err != nil {
			return err
		}
	}
	gitPath, trunkBranch, err := h.projectGitPathAndDefaultBranch(r.Context(), projectID)
	if err != nil {
		return err
	}
	result, err := h.Git.Rebase(r.Context(), worktree.String, trunkBranch)
	if err != nil {
		return err
	}
	if result.Success {
		trunkHead, _ := h.Git.HeadCommit(r.Context(), gitPath)
		if err := h.updateRebasedChapterMetadata(r.Context(), chapterID, trunkHead, result.CommitSHA); err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "commitSha": result.CommitSHA})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "conflictFiles": result.ConflictFiles})
	return nil
}

func (h *Handler) updateRebasedChapterMetadata(ctx context.Context, chapterID, trunkHead, commitSHA string) error {
	_, err := h.DB.ExecContext(ctx, `UPDATE chapters SET start_commit_sha = ?, head_commit_sha = ?, updated_at = ? WHERE id = ?`, nullableString(trunkHead), nullableString(commitSHA), db.NowISO(), chapterID)
	return err
}

func (h *Handler) rulerRebaseResolve(w http.ResponseWriter, r *http.Request) error {
	if h.Git == nil {
		rulerFeatureDisabled(w, "rebaseResolve", "Ruler rebase conflict resolution requires git service support that is not available in this go_backend runtime")
		return nil
	}
	projectID := chi.URLParam(r, "projectId")
	var body struct {
		ChapterID string `json:"chapterId"`
		Action    string `json:"action"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	chapterID := strings.TrimSpace(body.ChapterID)
	if chapterID == "" {
		return httpx.BadRequest("chapterId is required")
	}
	if ok, err := h.chapterBelongsToProject(r.Context(), chapterID, projectID); err != nil {
		return err
	} else if !ok {
		return httpx.NotFound("Chapter")
	}
	var worktree sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT worktree_path FROM chapters WHERE id = ?`, chapterID).Scan(&worktree); err != nil {
		return err
	}
	if !worktree.Valid || worktree.String == "" {
		return httpx.BadRequest("Chapter has no worktree")
	}
	switch body.Action {
	case "abort":
		if err := h.Git.RebaseAbort(r.Context(), worktree.String); err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return nil
	case "continue":
		if h.Narrators == nil {
			rulerFeatureDisabled(w, "rebaseContinue", "Ruler rebase continuation requires narrator service support that is not available in this go_backend runtime")
			return nil
		}
		_, trunkBranch, err := h.projectGitPathAndDefaultBranch(r.Context(), projectID)
		if err != nil {
			return err
		}
		conflictFiles, err := h.Git.ConflictFiles(r.Context(), worktree.String)
		if err != nil {
			return err
		}
		narratorID, err := h.primaryNarratorIDOrCreate(r.Context(), chapterID)
		if err != nil {
			return err
		}
		userID, err := currentUserID(r)
		if err != nil {
			return err
		}
		_, err = h.Narrators.AddUserMessage(r.Context(), narratorID, userID, rebaseConflictPrompt(trunkBranch, conflictFiles))
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "narratorId": narratorID})
		return nil
	default:
		return httpx.BadRequest("action must be abort or continue")
	}
}

func (h *Handler) rulerAbandon(w http.ResponseWriter, r *http.Request) error {
	if h.Chapters == nil {
		rulerFeatureDisabled(w, "abandon", "Ruler abandon requires chapter service support that is not available in this go_backend runtime")
		return nil
	}
	projectID := chi.URLParam(r, "projectId")
	var body struct {
		ChapterID string `json:"chapterId"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	chapterID := strings.TrimSpace(body.ChapterID)
	if chapterID == "" {
		return httpx.BadRequest("chapterId is required")
	}
	if ok, err := h.chapterBelongsToProject(r.Context(), chapterID, projectID); err != nil {
		return err
	} else if !ok {
		return httpx.NotFound("Chapter")
	}
	var status string
	if err := h.DB.QueryRowContext(r.Context(), `SELECT status FROM chapters WHERE id = ?`, chapterID).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return httpx.BadRequest("Can only abandon active chapters")
	}
	if err := h.Chapters.Delete(r.Context(), chapterID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
	return nil
}

func (h *Handler) rootChapterID(ctx context.Context, projectID string) (string, error) {
	var idValue string
	err := h.DB.QueryRowContext(ctx, `SELECT id FROM chapters WHERE project_id = ? AND is_root = 1 LIMIT 1`, projectID).Scan(&idValue)
	if err == sql.ErrNoRows {
		return "", httpx.NotFound("Root chapter for project")
	}
	return idValue, err
}

func (h *Handler) chapterBelongsToProject(ctx context.Context, chapterID, projectID string) (bool, error) {
	var count int
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapters WHERE id = ? AND project_id = ?`, chapterID, projectID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (h *Handler) ensureSourceWorktreeClean(ctx context.Context, sourceID string) error {
	if h.Git == nil {
		return nil
	}
	var worktree sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT worktree_path FROM chapters WHERE id = ?`, sourceID).Scan(&worktree); err != nil {
		return err
	}
	if !worktree.Valid || worktree.String == "" {
		return nil
	}
	status, err := h.Git.Status(ctx, worktree.String)
	if err != nil {
		return err
	}
	if !status.Clean {
		return httpx.BadRequest("MERGE_DIRTY_SOURCE")
	}
	return nil
}

func (h *Handler) ensureWorktreeClean(ctx context.Context, worktreePath, code string) error {
	if h.Git == nil || strings.TrimSpace(worktreePath) == "" {
		return nil
	}
	status, err := h.Git.Status(ctx, worktreePath)
	if err != nil {
		return err
	}
	if !status.Clean {
		return httpx.Error(http.StatusBadRequest, "VALIDATION_ERROR", code)
	}
	return nil
}

func (h *Handler) primaryNarratorIDOrCreate(ctx context.Context, chapterID string) (string, error) {
	var narratorID string
	err := h.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID)
	if err == nil {
		return narratorID, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	if h.Narrators == nil {
		return "", httpx.NotImplemented("primary narrator creation")
	}
	chapterIDCopy := chapterID
	narrator, err := h.Narrators.Create(ctx, narrators.CreateInput{ChapterID: &chapterIDCopy, PermissionMode: "default"})
	if err != nil {
		return "", err
	}
	return narrator.ID, nil
}

func rebaseConflictPrompt(ontoBranch string, conflictFiles []gitx.ConflictFile) string {
	var builder strings.Builder
	builder.WriteString("Please resolve the current git rebase conflicts while rebasing this chapter onto ")
	builder.WriteString(ontoBranch)
	builder.WriteString(".\n\nConflict files:\n")
	if len(conflictFiles) == 0 {
		builder.WriteString("  - No conflicted files are currently reported by git, but the rebase is still awaiting resolution.\n")
	} else {
		for _, file := range conflictFiles {
			builder.WriteString(fmt.Sprintf("  - %s (%d conflict marker(s))\n", file.File, file.ConflictLines))
		}
	}
	builder.WriteString("\nAfter resolving the files, stage the resolved changes and continue the rebase with `git rebase --continue`. If continuing is unsafe, explain the blocker clearly.")
	return builder.String()
}

func (h *Handler) projectGitPathAndDefaultBranch(ctx context.Context, projectID string) (string, string, error) {
	var gitPath, defaultBranch sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT git_path, default_branch FROM projects WHERE id = ?`, projectID).Scan(&gitPath, &defaultBranch); err != nil {
		if err == sql.ErrNoRows {
			return "", "", httpx.NotFound("Project")
		}
		return "", "", err
	}
	if !gitPath.Valid || gitPath.String == "" {
		return "", "", httpx.BadRequest("Project has no git path")
	}
	branch := "main"
	if defaultBranch.Valid && defaultBranch.String != "" {
		branch = defaultBranch.String
	}
	return gitPath.String, branch, nil
}

func (h *Handler) deleteRulerMergedBranch(ctx context.Context, projectID, sourceID string) {
	if h.Git == nil {
		return
	}
	var gitPath, branch sql.NullString
	_ = h.DB.QueryRowContext(ctx, `SELECT git_path FROM projects WHERE id = ?`, projectID).Scan(&gitPath)
	_ = h.DB.QueryRowContext(ctx, `SELECT branch FROM chapters WHERE id = ?`, sourceID).Scan(&branch)
	if gitPath.Valid && gitPath.String != "" && branch.Valid && branch.String != "" {
		_ = h.Git.DeleteBranch(ctx, gitPath.String, branch.String)
	}
}

func (h *Handler) RegisterRuler(r chi.Router) {
	r.Get("/{projectId}/ruler", httpx.Handler(h.rulerData))
	r.Get("/{projectId}/ruler/segment", httpx.Handler(h.rulerSegment))
	r.Patch("/{projectId}/ruler/positions", httpx.Handler(h.rulerPositions))
	r.Post("/{projectId}/ruler/fork", httpx.Handler(h.rulerFork))
	r.Post("/{projectId}/ruler/merge", httpx.Handler(h.rulerMerge))
	r.Post("/{projectId}/ruler/abandon", httpx.Handler(h.rulerAbandon))
	r.Post("/{projectId}/ruler/rebase", httpx.Handler(h.rulerRebase))
	r.Post("/{projectId}/ruler/rebase-resolve", httpx.Handler(h.rulerRebaseResolve))

}

func rulerCapabilities(h *Handler) map[string]any {
	chapterServiceReady := h != nil && h.Chapters != nil
	gitServiceReady := h != nil && h.Git != nil
	return map[string]any{
		"read": map[string]any{
			"supported": true,
			"fallback":  false,
			"mode":      "git-log-plus-chapter-overlay",
		},
		"mutations": map[string]any{
			"fork": map[string]any{
				"supported": chapterServiceReady,
				"fallback":  !chapterServiceReady,
				"code":      fallbackCodeIfDisabled(chapterServiceReady),
			},
			"merge": map[string]any{
				"supported": chapterServiceReady,
				"fallback":  !chapterServiceReady,
				"code":      fallbackCodeIfDisabled(chapterServiceReady),
			},
			"abandon": map[string]any{
				"supported": chapterServiceReady,
				"fallback":  !chapterServiceReady,
				"code":      fallbackCodeIfDisabled(chapterServiceReady),
			},
			"rebase": map[string]any{
				"supported": gitServiceReady,
				"fallback":  !gitServiceReady,
				"code":      fallbackCodeIfDisabled(gitServiceReady),
			},
			"rebaseResolve": map[string]any{
				"supported": gitServiceReady,
				"fallback":  !gitServiceReady,
				"code":      fallbackCodeIfDisabled(gitServiceReady),
			},
		},
		"degradedRead": map[string]any{
			"supported": true,
			"fallbacks": []string{"project_git_path_missing", "git_rev_list_failed", "git_log_failed"},
		},
	}
}

func fallbackCodeIfDisabled(supported bool) any {
	if supported {
		return nil
	}
	return "FEATURE_DISABLED"
}

func (h *Handler) rulerData(w http.ResponseWriter, r *http.Request) error {
	projectID := chi.URLParam(r, "projectId")
	limit := intMapOr(r.URL.Query(), "limit", 200)
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	skip := intMapOr(r.URL.Query(), "skip", 0)
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	direction := strings.TrimSpace(r.URL.Query().Get("direction"))
	fallbacks := []map[string]any{}

	var gitPath, defaultBranch sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT git_path, default_branch FROM projects WHERE id = ?`, projectID).Scan(&gitPath, &defaultBranch); err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("Project")
		}
		return err
	}
	branch := "main"
	if defaultBranch.Valid && defaultBranch.String != "" {
		branch = defaultBranch.String
	}
	if !gitPath.Valid || strings.TrimSpace(gitPath.String) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"commits":           []any{},
			"segments":          []any{},
			"activeChapters":    []any{},
			"mergedChapters":    []any{},
			"capabilities":      rulerCapabilities(h),
			"totalCommitCount":  0,
			"oldestLoadedIndex": 0,
			"newestLoadedIndex": -1,
			"degraded":          true,
			"fallbacks": []map[string]any{{
				"feature": "ruler.gitLog",
				"reason":  "project_git_path_missing",
				"message": "Project has no git path; returning an empty ruler backbone so the frontend can degrade gracefully.",
			}},
		})
		return nil
	}

	totalCommitCount := 0
	if count, err := gitRevListCount(r.Context(), gitPath.String, branch); err == nil {
		totalCommitCount = count
	} else {
		fallbacks = append(fallbacks, map[string]any{"feature": "ruler.totalCommitCount", "reason": "git_rev_list_failed", "error": err.Error()})
	}
	if cursor != "" {
		if countAfterCursor, err := gitRevListCount(r.Context(), gitPath.String, cursor+".."+branch); err == nil {
			cursorIndex := countAfterCursor
			switch direction {
			case "older":
				skip = cursorIndex + 1
			case "newer":
				skip = maxInt(0, cursorIndex-limit)
			default:
				skip = cursorIndex
			}
		}
	}
	commits, err := gitLogCommits(r.Context(), gitPath.String, branch, limit, skip)
	if err != nil {
		fallbacks = append(fallbacks, map[string]any{"feature": "ruler.commits", "reason": "git_log_failed", "error": err.Error()})
		commits = []map[string]any{}
	}
	projectChapters, err := h.rulerProjectChapters(r.Context(), projectID)
	if err != nil {
		return err
	}
	commitShaSet := map[string]bool{}
	for _, commit := range commits {
		if sha := stringMap(commit, "sha", ""); sha != "" {
			commitShaSet[sha] = true
		}
	}
	chapterByID := map[string]map[string]any{}
	for _, ch := range projectChapters {
		chapterByID[stringMap(ch, "id", "")] = ch
	}
	resolveEffectiveSha := func(ch map[string]any) string {
		if sha := stringMap(ch, "startCommitSha", ""); sha != "" && commitShaSet[sha] {
			return sha
		}
		visited := map[string]bool{}
		cur := ch
		for {
			parentID := strings.TrimSpace(stringMap(cur, "parentChapterId", ""))
			if parentID == "" || visited[parentID] {
				break
			}
			visited[parentID] = true
			parent := chapterByID[parentID]
			if parent == nil {
				break
			}
			if sha := stringMap(parent, "startCommitSha", ""); sha != "" && commitShaSet[sha] {
				return sha
			}
			cur = parent
		}
		return ""
	}
	chaptersByStart := map[string][]map[string]any{}
	for _, ch := range projectChapters {
		if sha := resolveEffectiveSha(ch); sha != "" {
			chaptersByStart[sha] = append(chaptersByStart[sha], ch)
		}
	}
	segments := []map[string]any{}
	for i, commit := range commits {
		sha := stringMap(commit, "sha", "")
		chaptersAtSha := chaptersByStart[sha]
		if len(chaptersAtSha) == 0 {
			continue
		}
		toIndex := i
		if i > 0 {
			toIndex = i - 1
		}
		activeIDs := []string{}
		activeCount := 0
		for _, ch := range chaptersAtSha {
			if stringMap(ch, "status", "") == "active" {
				activeIDs = append(activeIDs, stringMap(ch, "id", ""))
				activeCount++
			}
		}
		segments = append(segments, map[string]any{
			"fromSha":            sha,
			"toSha":              commitShaAt(commits, toIndex, sha),
			"fromIndex":          i,
			"toIndex":            toIndex,
			"activeChapterCount": activeCount,
			"totalChapterCount":  len(chaptersAtSha),
			"activeChapterIds":   activeIDs,
			"isExpandable":       len(chaptersAtSha) > 0,
		})
	}
	activeChapters := []map[string]any{}
	mergedChapters := []map[string]any{}
	for _, ch := range projectChapters {
		item := map[string]any{
			"id":              stringMap(ch, "id", ""),
			"title":           stringMap(ch, "title", ""),
			"branch":          stringMap(ch, "branch", ""),
			"role":            stringMap(ch, "role", ""),
			"parentChapterId": nullIfEmpty(stringMap(ch, "parentChapterId", "")),
			"startCommitSha":  nullIfEmpty(stringMap(ch, "startCommitSha", "")),
			"mergeCommitSha":  nullIfEmpty(stringMap(ch, "mergeCommitSha", "")),
			"narratorId":      nullIfEmpty(stringMap(ch, "narratorId", "")),
			"narratorStatus":  nullIfEmpty(stringMap(ch, "narratorStatus", "")),
			"axisOffset":      numericMapOr(ch, "axisOffset", 0),
			"crossOffset":     numericMapOr(ch, "crossOffset", 0),
		}
		if stringMap(ch, "status", "") == "active" {
			activeChapters = append(activeChapters, item)
		} else if stringMap(ch, "status", "") == "merged" {
			mergedChapters = append(mergedChapters, item)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"commits":           commits,
		"segments":          segments,
		"activeChapters":    activeChapters,
		"mergedChapters":    mergedChapters,
		"capabilities":      rulerCapabilities(h),
		"totalCommitCount":  totalCommitCount,
		"oldestLoadedIndex": skip,
		"newestLoadedIndex": skip + len(commits) - 1,
		"degraded":          len(fallbacks) > 0,
		"fallbacks":         fallbacks,
	})
	return nil
}

func (h *Handler) rulerSegment(w http.ResponseWriter, r *http.Request) error {
	projectID := chi.URLParam(r, "projectId")
	fromSha := strings.TrimSpace(r.URL.Query().Get("from"))
	detail := strings.TrimSpace(r.URL.Query().Get("detail"))
	if fromSha == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"chapters": []any{}, "edges": []any{}})
		return nil
	}
	projectChapters, err := h.rulerProjectChapters(r.Context(), projectID)
	if err != nil {
		return err
	}
	chapterByID := map[string]map[string]any{}
	for _, ch := range projectChapters {
		chapterByID[stringMap(ch, "id", "")] = ch
	}
	segmentChapters := []map[string]any{}
	for _, ch := range projectChapters {
		if h.chapterBelongsToRulerSegment(ch, fromSha, chapterByID) {
			segmentChapters = append(segmentChapters, ch)
		}
	}
	chapterIDs := make([]string, 0, len(segmentChapters))
	for _, ch := range segmentChapters {
		chapterIDs = append(chapterIDs, stringMap(ch, "id", ""))
	}
	if detail == "summary" {
		items := make([]map[string]any, 0, len(segmentChapters))
		for _, ch := range segmentChapters {
			items = append(items, map[string]any{
				"id":              stringMap(ch, "id", ""),
				"title":           stringMap(ch, "title", ""),
				"status":          stringMap(ch, "status", ""),
				"role":            stringMap(ch, "role", ""),
				"narratorStatus":  nullIfEmpty(stringMap(ch, "narratorStatus", "")),
				"anchorCommitSha": nullIfEmpty(firstNonEmpty(stringMap(ch, "anchorCommitSha", ""), stringMap(ch, "startCommitSha", ""))),
				"axisOffset":      numericMapOr(ch, "axisOffset", 0),
				"crossOffset":     numericMapOr(ch, "crossOffset", 0),
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"chapters": items, "edges": []any{}})
		return nil
	}
	edges := []map[string]any{}
	if len(chapterIDs) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chapterIDs)), ",")
		args := make([]any, 0, len(chapterIDs)+1)
		args = append(args, projectID)
		for _, idValue := range chapterIDs {
			args = append(args, idValue)
		}
		query := `SELECT id, source_id, target_id, type FROM chapter_edges WHERE project_id = ? AND source_id IN (` + placeholders + `)`
		rows, err := h.DB.QueryContext(r.Context(), query, args...)
		if err == nil {
			defer rows.Close()
			edges, _ = rowsToMaps(rows)
		}
	}
	result := make([]map[string]any, 0, len(segmentChapters))
	for _, ch := range segmentChapters {
		result = append(result, map[string]any{
			"id":                    stringMap(ch, "id", ""),
			"title":                 stringMap(ch, "title", ""),
			"status":                stringMap(ch, "status", ""),
			"branch":                stringMap(ch, "branch", ""),
			"role":                  stringMap(ch, "role", ""),
			"parentChapterId":       nullIfEmpty(stringMap(ch, "parentChapterId", "")),
			"startCommitSha":        nullIfEmpty(stringMap(ch, "startCommitSha", "")),
			"mergeCommitSha":        nullIfEmpty(stringMap(ch, "mergeCommitSha", "")),
			"headCommitSha":         nullIfEmpty(stringMap(ch, "headCommitSha", "")),
			"color":                 nullIfEmpty(stringMap(ch, "color", "")),
			"narratorId":            nullIfEmpty(stringMap(ch, "narratorId", "")),
			"narratorStatus":        nullIfEmpty(stringMap(ch, "narratorStatus", "")),
			"reviewSourceChapterId": nullIfEmpty(stringMap(ch, "reviewSourceChapterId", "")),
			"reviewStatus":          nullIfEmpty(stringMap(ch, "reviewStatus", "")),
			"anchorCommitSha":       nullIfEmpty(firstNonEmpty(stringMap(ch, "anchorCommitSha", ""), stringMap(ch, "startCommitSha", ""))),
			"axisOffset":            numericMapOr(ch, "axisOffset", 0),
			"crossOffset":           numericMapOr(ch, "crossOffset", 0),
			"panelWidth":            nullIfZero(numericMapOr(ch, "panelWidth", 0)),
			"panelHeight":           nullIfZero(numericMapOr(ch, "panelHeight", 0)),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chapters": result, "edges": edges})
	return nil
}

func (h *Handler) rulerPositions(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Positions []struct {
			ChapterID       string   `json:"chapterId"`
			AnchorCommitSHA string   `json:"anchorCommitSha"`
			AxisOffset      float64  `json:"axisOffset"`
			CrossOffset     float64  `json:"crossOffset"`
			Width           *float64 `json:"width"`
			Height          *float64 `json:"height"`
		} `json:"positions"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	projectID := chi.URLParam(r, "projectId")
	for _, pos := range body.Positions {
		if strings.TrimSpace(pos.ChapterID) == "" {
			continue
		}
		sets := []string{"anchor_commit_sha = ?", "axis_offset = ?", "cross_offset = ?", "updated_at = ?"}
		args := []any{nullableString(pos.AnchorCommitSHA), pos.AxisOffset, pos.CrossOffset, db.NowISO()}
		if pos.Width != nil {
			sets = append(sets, "panel_width = ?")
			args = append(args, *pos.Width)
		}
		if pos.Height != nil {
			sets = append(sets, "panel_height = ?")
			args = append(args, *pos.Height)
		}
		args = append(args, projectID, pos.ChapterID)
		_, err := h.DB.ExecContext(r.Context(), `UPDATE chapters SET `+strings.Join(sets, ", ")+` WHERE project_id = ? AND id = ?`, args...)
		if err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
	return nil
}

func (h *Handler) rulerProjectChapters(ctx context.Context, projectID string) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `
		SELECT
			c.id,
			c.title,
			c.status,
			c.branch,
			c.role,
			c.parent_chapter_id,
			c.start_commit_sha,
			c.merge_commit_sha,
			c.head_commit_sha,
			c.anchor_commit_sha,
			c.axis_offset,
			c.cross_offset,
			c.panel_width,
			c.panel_height,
			c.color,
			(
				SELECT n.id
				FROM narrators n
				WHERE n.chapter_id = c.id
				ORDER BY CASE WHEN n.variant = 'primary' OR n.type = 'primary' THEN 0 ELSE 1 END, n.created_at ASC
				LIMIT 1
			) AS narrator_id,
			(
				SELECT n.status
				FROM narrators n
				WHERE n.chapter_id = c.id
				ORDER BY CASE WHEN n.variant = 'primary' OR n.type = 'primary' THEN 0 ELSE 1 END, n.created_at ASC
				LIMIT 1
			) AS narrator_status,
			c.review_source_chapter_id,
			c.review_status
		FROM chapters c
		WHERE c.project_id = ? AND c.is_root = 0
		ORDER BY c.created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}

func (h *Handler) chapterBelongsToRulerSegment(ch map[string]any, fromSha string, byID map[string]map[string]any) bool {
	if stringMap(ch, "startCommitSha", "") == fromSha {
		return true
	}
	visited := map[string]bool{}
	cur := ch
	for {
		parentID := strings.TrimSpace(stringMap(cur, "parentChapterId", ""))
		if parentID == "" || visited[parentID] {
			break
		}
		visited[parentID] = true
		parent := byID[parentID]
		if parent == nil {
			break
		}
		if stringMap(parent, "startCommitSha", "") == fromSha {
			return true
		}
		cur = parent
	}
	return false
}

func gitRevListCount(ctx context.Context, gitPath, ref string) (int, error) {
	out, stderr, err := runCompatGit(ctx, "-C", gitPath, "rev-list", "--count", ref)
	if err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(out)
		}
		return 0, fmt.Errorf("git rev-list --count %s: %w: %s", ref, err, detail)
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return count, nil
}

func gitLogCommits(ctx context.Context, gitPath, branch string, limit, skip int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []string{"-C", gitPath, "log", "--max-count", strconv.Itoa(limit), "--skip", strconv.Itoa(maxInt(0, skip)), "--pretty=format:%H%x1f%h%x1f%s%x1f%an%x1f%aI", branch}
	out, stderr, err := runCompatGit(ctx, args...)
	if err != nil {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(out)
		}
		return nil, fmt.Errorf("git log %s: %w: %s", branch, err, detail)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	commits := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		commits = append(commits, map[string]any{"sha": parts[0], "shortSha": parts[1], "message": parts[2], "author": parts[3], "date": parts[4]})
	}
	return commits, nil
}

func runCompatGit(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	stdout := &limitedStringWriter{limit: maxCompatGitOutputBytes}
	stderr := &limitedStringWriter{limit: 32 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func commitShaAt(commits []map[string]any, idx int, fallback string) string {
	if idx < 0 || idx >= len(commits) {
		return fallback
	}
	return stringMap(commits[idx], "sha", fallback)
}

func nullIfZero(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) RegisterVolumeSnapshots(r chi.Router) {
	r.Get("/{projectId}/volume-snapshots", httpx.Handler(h.listVolumeSnapshots))
	r.Post("/{projectId}/volume-snapshots", httpx.Handler(h.createVolumeSnapshot))
}

func (h *Handler) RegisterVolumeSnapshotRoot(r chi.Router) {
	r.Get("/{id}", httpx.Handler(h.getVolumeSnapshot))
	r.Patch("/{id}", httpx.Handler(h.updateVolumeSnapshot))
	r.Delete("/{id}", httpx.Handler(h.deleteVolumeSnapshot))
	r.Post("/{id}/apply", httpx.Handler(h.applyVolumeSnapshot))
	r.Get("/{id}/applications", httpx.Handler(h.listSnapshotApplications))
}

func (h *Handler) listVolumeSnapshots(w http.ResponseWriter, r *http.Request) error {
	query := `SELECT id,project_id,name,description,source_chapter_id,service_name,container_path,size_bytes,created_by,created_at,updated_at FROM volume_snapshots WHERE project_id = ?`
	args := []any{chi.URLParam(r, "projectId")}
	if serviceName := strings.TrimSpace(r.URL.Query().Get("serviceName")); serviceName != "" {
		query += ` AND service_name = ?`
		args = append(args, serviceName)
	}
	if containerPath := strings.TrimSpace(r.URL.Query().Get("containerPath")); containerPath != "" {
		query += ` AND container_path = ?`
		args = append(args, containerPath)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}
func (h *Handler) createVolumeSnapshot(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if !podmanSnapshotAvailable() {
		writeVolumeSnapshotFallback(w, "PODMAN_NOT_FOUND", "podman is not available; volume snapshot creation requires Podman")
		return nil
	}
	createdBy := any(nil)
	if claims != nil {
		createdBy = claims.Subject
	}
	chapterID := strings.TrimSpace(stringOr(body["chapterId"], ""))
	if chapterID == "" {
		chapterID = strings.TrimSpace(stringOr(body["sourceChapterId"], ""))
	}
	snapshotID, err := h.createVolumeSnapshotArchive(r.Context(), chi.URLParam(r, "projectId"), chapterID, strings.TrimSpace(stringOr(body["serviceName"], "")), stringOr(body["containerPath"], ""), strings.TrimSpace(stringOr(body["name"], "")), nullableString(body["description"]), createdBy)
	if err != nil {
		return err
	}
	return h.getVolumeSnapshotByID(w, r, snapshotID, http.StatusCreated)
}
func (h *Handler) getVolumeSnapshot(w http.ResponseWriter, r *http.Request) error {
	return h.getVolumeSnapshotByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) updateVolumeSnapshot(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	_, err := h.DB.ExecContext(r.Context(), `UPDATE volume_snapshots SET name=COALESCE(?,name), description=COALESCE(?,description), updated_at=? WHERE id=?`, nullableString(body["name"]), nullableString(body["description"]), db.NowISO(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	return h.getVolumeSnapshotByID(w, r, chi.URLParam(r, "id"), http.StatusOK)
}
func (h *Handler) deleteVolumeSnapshot(w http.ResponseWriter, r *http.Request) error {
	snapshotID := chi.URLParam(r, "id")
	snap, err := h.getVolumeSnapshotRecord(r.Context(), snapshotID)
	if err != nil {
		return err
	}
	archivePath, err := volumeSnapshotPath(snap.ProjectID, snap.ID)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM volume_snapshot_applications WHERE snapshot_id=?`, snapshotID); err != nil {
		return err
	}
	_, err = h.DB.ExecContext(r.Context(), `DELETE FROM volume_snapshots WHERE id=?`, snapshotID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
	return nil
}
func (h *Handler) applyVolumeSnapshot(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	var body struct {
		TargetChapterID string `json:"targetChapterId"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.TargetChapterID) == "" {
		return httpx.BadRequest("targetChapterId is required")
	}
	if !podmanSnapshotAvailable() {
		writeVolumeSnapshotFallback(w, "PODMAN_NOT_FOUND", "podman is not available; volume snapshot apply requires Podman")
		return nil
	}
	appliedBy := any(nil)
	if claims != nil {
		appliedBy = claims.Subject
	}
	if err := h.applyVolumeSnapshotArchive(r.Context(), chi.URLParam(r, "id"), strings.TrimSpace(body.TargetChapterID), appliedBy); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
	return nil
}
func (h *Handler) listSnapshotApplications(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,snapshot_id,chapter_id,applied_at,applied_by FROM volume_snapshot_applications WHERE snapshot_id=? ORDER BY applied_at DESC`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}
func (h *Handler) getVolumeSnapshotByID(w http.ResponseWriter, r *http.Request, snapshotID string, status int) error {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,project_id,name,description,source_chapter_id,service_name,container_path,size_bytes,created_by,created_at,updated_at FROM volume_snapshots WHERE id=?`, snapshotID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items, err := rowsToMaps(rows)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return httpx.NotFound("Volume snapshot")
	}
	httpx.WriteJSON(w, status, items[0])
	return nil
}

func optionalUserID(r *http.Request) string {
	claims := auth.UserFromContext(r.Context())
	if claims == nil || claims.Subject == "" {
		return "anonymous"
	}
	return claims.Subject
}

func currentUserID(r *http.Request) (string, error) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return "", httpx.Unauthorized("Authentication required")
	}
	return claims.Subject, nil
}
func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, col := range cols {
			item[snakeToCamel(col)] = normalize(values[i], col)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func normalize(v any, col string) any {
	if col == "enabled" {
		return v == int64(1)
	}
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}
func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}
func intOr(v any, fallback int) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	if n, ok := v.(int); ok {
		return n
	}
	return fallback
}
func boolInt(v any, fallback bool) int {
	b, ok := v.(bool)
	if !ok {
		b = fallback
	}
	if b {
		return 1
	}
	return 0
}
func nullableString(v any) any {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return nil
}
func nullableNumber(v any) any {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return nil
}
func nullableBoolInt(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return 1
		}
		return 0
	}
	return nil
}
