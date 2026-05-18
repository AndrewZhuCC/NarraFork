package projects

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/httpx"
)

type Handler struct{ Service *Service }

func NewHandler(service *Service) *Handler { return &Handler{Service: service} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Post("/import", httpx.Handler(h.importProject))
	r.Post("/{id}/backup/sync", httpx.Handler(h.backupSync))
	r.Get("/{id}/backup/status", httpx.Handler(h.backupStatus))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Delete("/{id}", httpx.Handler(h.delete))
}

type createRequest struct {
	Name          string  `json:"name"`
	Description   *string `json:"description"`
	RepoMode      string  `json:"repoMode"`
	GitPath       string  `json:"gitPath"`
	FlowMode      string  `json:"flowMode"`
	CloneURL      string  `json:"cloneUrl"`
	CloneBranch   string  `json:"cloneBranch"`
	CloneUsername string  `json:"cloneUsername"`
	ClonePassword string  `json:"clonePassword"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	projects, err := h.Service.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, projects)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	input := CreateInput{Name: req.Name, Description: req.Description, RepoMode: req.RepoMode, GitPath: req.GitPath, FlowMode: req.FlowMode, CloneURL: req.CloneURL, CloneBranch: req.CloneBranch, CloneUsername: req.CloneUsername, ClonePassword: req.ClonePassword}
	if req.RepoMode == "clone" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		input.Progress = func(message string) {
			writeSSE(w, "progress", map[string]any{"message": message})
		}
		writeSSE(w, "progress", map[string]any{"message": "Starting clone..."})
		project, err := h.Service.Create(r.Context(), input)
		if err != nil {
			if isGitCredentialError(err) {
				writeSSE(w, "credential_required", map[string]any{"error": err.Error()})
				return nil
			}
			writeSSE(w, "error", map[string]any{"error": err.Error()})
			return nil
		}
		writeSSE(w, "complete", project)
		return nil
	}
	project, err := h.Service.Create(r.Context(), input)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, project)
	return nil
}

func (h *Handler) backupSync(w http.ResponseWriter, r *http.Request) error {
	result, err := h.Service.SyncBackup(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) backupStatus(w http.ResponseWriter, r *http.Request) error {
	result, err := h.Service.BackupStatus(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) importProject(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		GitPath string `json:"gitPath"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	result, err := h.Service.ImportBackup(r.Context(), body.GitPath)
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if skipped, _ := result["skipped"].(bool); skipped {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, result)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	project, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, project)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var fields map[string]any
	if err := httpx.DecodeLooseJSON(r, &fields); err != nil {
		return err
	}
	project, err := h.Service.Update(r.Context(), chi.URLParam(r, "id"), fields)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, project)
	return nil
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(data))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func isGitCredentialError(err error) bool {
	message := strings.ToLower(err.Error())
	needles := []string{"authentication failed", "could not read username", "could not read password", "terminal prompts disabled", "permission denied", "repository not found"}
	for _, needle := range needles {
		if strings.Contains(message, needle) {
			return true
		}
	}
	return false
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	result, err := h.Service.Delete(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}
