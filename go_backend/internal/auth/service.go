package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type contextKey string

const userContextKey contextKey = "user"

const tokenExpiry = 7 * 24 * time.Hour

var avatarColors = []string{
	"#4C6EF5", "#7950F2", "#BE4BDB", "#E64980", "#FA5252", "#FD7E14",
	"#FAB005", "#40C057", "#12B886", "#15AABF", "#228BE6", "#845EF7",
}

type Service struct {
	DB       *sql.DB
	Settings *config.Store
}

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type User struct {
	ID            string  `json:"id"`
	Username      string  `json:"username"`
	Role          string  `json:"role"`
	AvatarColor   *string `json:"avatarColor"`
	AvatarImageID *string `json:"avatarImageId"`
	GitUsername   *string `json:"gitUsername,omitempty"`
	GitEmail      *string `json:"gitEmail,omitempty"`
	CreatedAt     string  `json:"createdAt"`
}

type AuthResult struct {
	User     User   `json:"user"`
	Token    string `json:"token"`
	Language string `json:"language"`
}

func NewService(sqlDB *sql.DB, settings *config.Store) *Service {
	return &Service{DB: sqlDB, Settings: settings}
}

func (s *Service) Register(ctx context.Context, username, password, language string) (AuthResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 100 {
		return AuthResult{}, httpx.BadRequest("username is required")
	}
	if len(password) < 6 || len(password) > 200 {
		return AuthResult{}, httpx.BadRequest("password must be 6-200 characters")
	}
	if language == "" {
		language = "en"
	}

	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return AuthResult{}, err
	}
	if count > 0 && !s.Settings.Core().Auth.RegistrationOpen {
		return AuthResult{}, httpx.Error(http.StatusForbidden, "REGISTRATION_CLOSED", "Registration is closed")
	}

	var existing string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&existing)
	if err == nil {
		return AuthResult{}, httpx.Error(http.StatusConflict, "USERNAME_TAKEN", "Username already taken")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuthResult{}, err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return AuthResult{}, err
	}
	role := "user"
	if count == 0 {
		role = "admin"
	}
	now := db.NowISO()
	userID := id.New()
	prefID := id.New()
	avatarColor := avatarColors[rand.Intn(len(avatarColors))]

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, avatar_color, created_at) VALUES (?, ?, ?, ?, ?, ?)`, userID, username, string(passwordHash), role, avatarColor, now); err != nil {
		return AuthResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_preferences (id, user_id, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, prefID, userID, language, now, now); err != nil {
		return AuthResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuthResult{}, err
	}

	user := User{ID: userID, Username: username, Role: role, AvatarColor: &avatarColor, CreatedAt: now}
	token, err := s.CreateToken(userID, role)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: user, Token: token, Language: language}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (AuthResult, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, username, password_hash, role, avatar_color, avatar_image_id, git_username, git_email, created_at FROM users WHERE username = ?`, username)
	var passwordHash string
	user, err := scanUserWithPassword(row, &passwordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthResult{}, httpx.Error(http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		}
		return AuthResult{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return AuthResult{}, httpx.Error(http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
	}
	language := "en"
	if err := s.DB.QueryRowContext(ctx, `SELECT language FROM user_preferences WHERE user_id = ?`, user.ID).Scan(&language); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuthResult{}, fmt.Errorf("query user language: %w", err)
	}
	token, err := s.CreateToken(user.ID, user.Role)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: user, Token: token, Language: language}, nil
}

func (s *Service) CreateToken(userID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenExpiry)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.Settings.Core().Auth.JWTSecret))
}

func (s *Service) VerifyToken(tokenText string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenText, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, httpx.Unauthorized("Invalid token signing method")
		}
		return []byte(s.Settings.Core().Auth.JWTSecret), nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, httpx.TokenExpired("Token expired")
		}
		return nil, httpx.Unauthorized("Invalid or expired token")
	}
	if !token.Valid {
		return nil, httpx.Unauthorized("Invalid or expired token")
	}
	return claims, nil
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.WriteError(w, httpx.Unauthorized("Authentication required"))
			return
		}
		claims, err := s.VerifyToken(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		var exists int
		if err := s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE id = ?`, claims.Subject).Scan(&exists); err != nil {
			httpx.WriteError(w, err)
			return
		}
		if exists == 0 {
			httpx.WriteError(w, httpx.Unauthorized("User no longer exists"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, claims)))
	})
}

func (s *Service) AdminOnly(next http.Handler) http.Handler {
	return s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			httpx.WriteError(w, httpx.Forbidden("Admin access required"))
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func UserFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(userContextKey).(*Claims)
	return claims
}

func (s *Service) GetUser(ctx context.Context, userID string) (User, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, username, role, avatar_color, avatar_image_id, git_username, git_email, created_at FROM users WHERE id = ?`, userID)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, httpx.NotFound("User")
	}
	return user, err
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, gitUsername, gitEmail *string) error {
	if gitUsername == nil && gitEmail == nil {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET git_username = COALESCE(?, git_username), git_email = COALESCE(?, git_email) WHERE id = ?`, nullableString(gitUsername), nullableString(gitEmail), userID)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUserWithPassword(row rowScanner, passwordHash *string) (User, error) {
	var avatarColor, avatarImageID, gitUsername, gitEmail sql.NullString
	var user User
	err := row.Scan(&user.ID, &user.Username, passwordHash, &user.Role, &avatarColor, &avatarImageID, &gitUsername, &gitEmail, &user.CreatedAt)
	if err != nil {
		return User{}, err
	}
	user.AvatarColor = nullStringPtr(avatarColor)
	user.AvatarImageID = nullStringPtr(avatarImageID)
	user.GitUsername = nullStringPtr(gitUsername)
	user.GitEmail = nullStringPtr(gitEmail)
	return user, nil
}

func scanUser(row rowScanner) (User, error) {
	var avatarColor, avatarImageID, gitUsername, gitEmail sql.NullString
	var user User
	err := row.Scan(&user.ID, &user.Username, &user.Role, &avatarColor, &avatarImageID, &gitUsername, &gitEmail, &user.CreatedAt)
	if err != nil {
		return User{}, err
	}
	user.AvatarColor = nullStringPtr(avatarColor)
	user.AvatarImageID = nullStringPtr(avatarImageID)
	user.GitUsername = nullStringPtr(gitUsername)
	user.GitEmail = nullStringPtr(gitEmail)
	return user, nil
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	if *v == "" {
		return nil
	}
	return *v
}
