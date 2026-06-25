package fs

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
)

const (
	maxDirectoryEntries = 500
	maxBinaryPreview    = 20 * 1024 * 1024
	maxTextPreview      = 1024 * 1024
)

var runtimeGOOS = runtime.GOOS

type Handler struct{ Settings *config.Store }

func NewHandler(settings *config.Store) *Handler { return &Handler{Settings: settings} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/browse", httpx.Handler(h.browse))
	r.Get("/shortcuts", httpx.Handler(h.shortcuts))
	r.Post("/mkdir", httpx.Handler(h.mkdir))
	r.Post("/reveal", httpx.Handler(h.reveal))
	r.Get("/preview", httpx.Handler(h.preview))
}

type entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Type  string `json:"type"`
	IsDir bool   `json:"isDir"`
}

func (h *Handler) browse(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	showHidden := r.URL.Query().Get("showHidden") == "1"
	if path == "" {
		drives := driveObjects()
		if runtimeGOOS == "windows" {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"path":    nil,
				"parent":  nil,
				"sep":     string(os.PathSeparator),
				"drives":  drives,
				"entries": []entry{},
			})
			return nil
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			path = home
		} else {
			path = h.Settings.Core().Paths.DefaultProjectDir
		}
	}
	resolved, err := expand(path)
	if err != nil {
		return err
	}
	// Mirror the TS /api/fs/browse error contract (server/routes/fs.ts): an
	// absent path -> "Path does not exist: <abs>", a non-directory ->
	// "Not a directory: <abs>", and any other stat failure -> "Cannot access:
	// <abs>". Go previously returned the raw os.ReadDir error string (e.g.
	// "open /foo: no such file or directory"), which differs from TS's messages
	// and did not distinguish the not-a-directory case.
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return httpx.ValidationError("Path does not exist: " + resolved)
		}
		return httpx.ValidationError("Cannot access: " + resolved)
	}
	if !info.IsDir() {
		return httpx.ValidationError("Not a directory: " + resolved)
	}
	items, err := os.ReadDir(resolved)
	if err != nil {
		return httpx.ValidationError("Cannot access: " + resolved)
	}
	result := make([]entry, 0, len(items))
	for _, item := range items {
		if len(result) >= maxDirectoryEntries {
			break
		}
		if !item.IsDir() {
			continue
		}
		if !showHidden && strings.HasPrefix(item.Name(), ".") {
			continue
		}
		result = append(result, entry{Name: item.Name(), Path: filepath.Join(resolved, item.Name()), Type: "directory", IsDir: true})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"path":    resolved,
		"parent":  parentDir(resolved),
		"sep":     string(os.PathSeparator),
		"drives":  driveObjects(),
		"entries": result,
	})
	return nil
}

func parentDir(path string) any {
	parent := filepath.Dir(path)
	if parent == path {
		return nil
	}
	return parent
}

func driveObjects() []map[string]string {
	if runtimeGOOS != "windows" {
		return []map[string]string{}
	}
	drives := []map[string]string{}
	for letter := 'A'; letter <= 'Z'; letter++ {
		drive := string(letter) + ":" + string(os.PathSeparator)
		if isExistingDir(drive) {
			drives = append(drives, map[string]string{"name": drive, "path": drive})
		}
	}
	return drives
}

func (h *Handler) shortcuts(w http.ResponseWriter, _ *http.Request) error {
	home, _ := os.UserHomeDir()
	candidates := []map[string]any{{"key": "home", "path": home}}
	for _, item := range []struct {
		key string
		rel string
	}{
		{key: "desktop", rel: "Desktop"},
		{key: "documents", rel: "Documents"},
		{key: "downloads", rel: "Downloads"},
	} {
		if home != "" {
			candidates = append(candidates, map[string]any{"key": item.key, "path": filepath.Join(home, item.rel)})
		}
	}
	if runtimeGOOS != "windows" {
		candidates = append(candidates, map[string]any{"key": "root", "path": string(os.PathSeparator)})
	}
	if h.Settings != nil {
		if projectDir := strings.TrimSpace(h.Settings.Core().Paths.DefaultProjectDir); projectDir != "" {
			candidates = append(candidates, map[string]any{"key": "projects", "path": projectDir})
		}
	}
	shortcuts := []map[string]any{}
	for _, candidate := range candidates {
		path, _ := candidate["path"].(string)
		if isExistingDir(path) {
			shortcuts = append(shortcuts, candidate)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"shortcuts": shortcuts, "drives": driveObjects(), "sep": string(os.PathSeparator)})
	return nil
}

func (h *Handler) reveal(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	resolved, err := expand(req.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return httpx.ValidationError("Directory does not exist: " + resolved)
	}
	if !info.IsDir() {
		return httpx.ValidationError("Directory does not exist: " + resolved)
	}
	cmdName := ""
	args := []string{resolved}
	switch runtimeGOOS {
	case "darwin":
		cmdName = "open"
	case "windows":
		cmdName = "explorer"
	default:
		return httpx.ValidationError("Opening file manager is not supported on this platform")
	}
	cmd := exec.Command(cmdName, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "path": resolved})
	return nil
}

func (h *Handler) mkdir(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	parent := strings.TrimSpace(req.Parent)
	name := strings.TrimSpace(req.Name)
	if parent == "" || name == "" {
		return httpx.ValidationError("parent and name are required")
	}
	if err := validateFolderName(name); err != nil {
		return err
	}
	resolvedParent, err := expand(parent)
	if err != nil {
		return err
	}
	if !isExistingDir(resolvedParent) {
		return httpx.ValidationError("Parent directory does not exist: " + resolvedParent)
	}
	resolved := filepath.Join(resolvedParent, name)
	if _, err := os.Stat(resolved); err == nil {
		return httpx.ValidationError("Already exists: " + filepath.Base(resolved))
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Mkdir(resolved, 0o755); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "path": resolved})
	return nil
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	resolved, err := expand(path)
	if err != nil {
		return err
	}
	// Mirror the TS /api/fs/preview error contract (server/routes/fs.ts): a
	// missing path -> "File does not exist: <abs>", any other stat failure ->
	// "Cannot access: <abs>", and a non-regular file (directory, socket,
	// device, FIFO, ...) -> "Not a file: <abs>", all as VALIDATION_ERROR (same
	// as the sibling browse handler). Go previously returned the raw os.Stat
	// error string (e.g. "stat /foo: no such file or directory"), which leaked
	// the absolute worktree path and OS-specific wording, used the BAD_REQUEST
	// code instead of VALIDATION_ERROR, and reported directories with the
	// generic "path is a directory" instead of TS's "Not a file" while only
	// rejecting directories rather than every non-regular file (TS !stat.isFile()).
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return httpx.ValidationError("File does not exist: " + resolved)
		}
		return httpx.ValidationError("Cannot access: " + resolved)
	}
	if !info.Mode().IsRegular() {
		return httpx.ValidationError("Not a file: " + resolved)
	}
	ctype := previewMimeType(filepath.Ext(resolved))
	limit := int64(maxTextPreview)
	if ctype != "" {
		limit = maxBinaryPreview
	}
	if info.Size() > limit {
		reason := "File too large to preview"
		httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"ok":       false,
			"code":     "FS_PREVIEW_TOO_LARGE",
			"reason":   reason,
			"error":    reason,
			"message":  reason,
			"size":     info.Size(),
			"maxBytes": limit,
		})
		return nil
	}
	if ctype == "" {
		ctype = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeFile(w, r, resolved)
	return nil
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}

func validateFolderName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) {
		return httpx.ValidationError("Invalid folder name")
	}
	if strings.ContainsAny(name, `/\\<>:"|?*`) {
		return httpx.ValidationError("Invalid folder name")
	}
	for _, r := range name {
		if r < 32 {
			return httpx.ValidationError("Invalid folder name")
		}
	}
	return nil
}

func isExistingDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func previewMimeType(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func expand(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", httpx.BadRequest("path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return filepath.Abs(path)
}
