package auth

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type Handler struct {
	Service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{Service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/register", httpx.Handler(h.register))
	r.Post("/login", httpx.Handler(h.login))
	r.With(h.Service.Middleware).Get("/me", httpx.Handler(h.me))
	r.With(h.Service.Middleware).Patch("/me", httpx.Handler(h.updateMe))
	r.With(h.Service.Middleware).Patch("/me/avatar", httpx.Handler(h.uploadAvatar))
	r.With(h.Service.Middleware).Delete("/me/avatar", httpx.Handler(h.deleteAvatar))
}

type registerRequest struct {
	Username string  `json:"username"`
	Password string  `json:"password"`
	Language *string `json:"language"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type updateProfileRequest struct {
	GitUsername *string `json:"gitUsername"`
	GitEmail    *string `json:"gitEmail"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) error {
	var req registerRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	language := "en"
	if req.Language != nil {
		language = *req.Language
	}
	result, err := h.Service.Register(r.Context(), req.Username, req.Password, language)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
	return nil
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	result, err := h.Service.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	claims := UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	user, err := h.Service.GetUser(r.Context(), claims.Subject)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, user)
	return nil
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) error {
	claims := UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	var req updateProfileRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if err := h.Service.UpdateProfile(r.Context(), claims.Subject, req.GitUsername, req.GitEmail); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) uploadAvatar(w http.ResponseWriter, r *http.Request) error {
	claims := UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	// MaxBytesReader caps the multipart read defensively; the clean 2MB limit
	// (matching TS MAX_AVATAR_SIZE in server/lib/uploads.ts) is enforced below via
	// the part size so an oversize avatar returns a 400 rather than a read error.
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarUploadBytes+512*1024)
	if err := r.ParseMultipartForm(maxAvatarUploadBytes + 512*1024); err != nil {
		return err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return httpx.BadRequest("No file provided")
	}
	defer file.Close()
	ext := avatarExt(header.Header.Get("Content-Type"), filepath.Ext(header.Filename))
	if ext == "" {
		return httpx.BadRequest("Unsupported avatar image type")
	}
	// Mirror the TS 2MB avatar size limit (MAX_AVATAR_SIZE) with a clean 400.
	if header.Size > maxAvatarUploadBytes {
		return httpx.BadRequest("Avatar too large: max 2MB")
	}
	imageID := id.Must(12)
	dir := filepath.Join(uploadsRoot(), "avatars", claims.Subject)
	// Mirror the TS saveAvatarImage: remove any existing avatar dir before writing
	// the new file so only one avatar file per user ever exists (no stale files).
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, imageID+ext)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		return err
	}
	_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE users SET avatar_image_id = ? WHERE id = ?`, imageID, claims.Subject)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "avatarImageId": imageID})
	return nil
}

const maxAvatarUploadBytes = 2 * 1024 * 1024

func (h *Handler) deleteAvatar(w http.ResponseWriter, r *http.Request) error {
	claims := UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	_ = os.RemoveAll(filepath.Join(uploadsRoot(), "avatars", claims.Subject))
	_, err := h.Service.DB.ExecContext(r.Context(), `UPDATE users SET avatar_image_id = NULL WHERE id = ?`, claims.Subject)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func uploadsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "uploads")
}

// avatarExt mirrors the TS AVATAR_MIME_TYPES allowlist (server/lib/uploads.ts),
// which is {image/png, image/jpeg, image/webp} — NOTE: GIF is allowed for inline
// images but NOT for avatars. Returns "" for unsupported types.
func avatarExt(mediaType, fallback string) string {
	if mediaType == "" {
		mediaType = mime.TypeByExtension(fallback)
	}
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	}
	fallback = strings.ToLower(fallback)
	if fallback == ".jpg" || fallback == ".jpeg" || fallback == ".png" || fallback == ".webp" {
		return fallback
	}
	return ""
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error {
		return httpx.NotImplemented(feature)
	}
}
