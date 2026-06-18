package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
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

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

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
	if httpx.JSStringLength(username) < 3 || httpx.JSStringLength(username) > 50 || !usernamePattern.MatchString(username) {
		return AuthResult{}, httpx.ValidationError("username must be 3-50 characters and contain only letters, numbers, hyphens, and underscores")
	}
	if httpx.JSStringLength(password) < 8 || httpx.JSStringLength(password) > 128 {
		return AuthResult{}, httpx.ValidationError("password must be 8-128 characters")
	}
	if httpx.JSStringLength(language) < 1 || httpx.JSStringLength(language) > 10 {
		return AuthResult{}, httpx.ValidationError("language must be 1-10 characters")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return AuthResult{}, err
	}
	now := db.NowISO()
	userID := id.New()
	prefID := id.New()
	avatarColor := avatarColors[rand.Intn(len(avatarColors))]

	var role string
	for attempt := 0; ; attempt++ {
		role, err = s.registerUserTx(
			ctx,
			username,
			string(passwordHash),
			language,
			userID,
			prefID,
			avatarColor,
			now,
		)
		if err == nil {
			break
		}
		if attempt < 2 && isSerializationFailure(err) {
			continue
		}
		return AuthResult{}, err
	}

	user := User{ID: userID, Username: username, Role: role, AvatarColor: &avatarColor, CreatedAt: now}
	token, err := s.CreateToken(userID, role)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: user, Token: token, Language: language}, nil
}

func (s *Service) registerUserTx(
	ctx context.Context,
	username string,
	passwordHash string,
	language string,
	userID string,
	prefID string,
	avatarColor string,
	now string,
) (string, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	// Keep bootstrap role selection in the same write statement as the user insert.
	// The preference row below commits in the same serializable transaction.
	insertUserQuery := `INSERT INTO users (id, username, password_hash, role, avatar_color, created_at)
		SELECT ?, ?, ?, CASE WHEN NOT EXISTS (SELECT 1 FROM users) THEN 'admin' ELSE 'user' END, ?, ?`
	if !s.Settings.Core().Auth.RegistrationOpen {
		insertUserQuery += ` WHERE NOT EXISTS (SELECT 1 FROM users)`
	}
	insertUserQuery += ` RETURNING role`

	var role string
	if err := tx.QueryRowContext(
		ctx,
		insertUserQuery,
		userID,
		username,
		passwordHash,
		avatarColor,
		now,
	).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", httpx.Error(http.StatusForbidden, "REGISTRATION_CLOSED", "Registration is closed")
		}
		if isUsernameUniqueViolation(err) {
			return "", httpx.Error(http.StatusConflict, "USERNAME_TAKEN", "Username already taken")
		}
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_preferences (id, user_id, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, prefID, userID, language, now, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return role, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (AuthResult, error) {
	if username == "" || password == "" {
		return AuthResult{}, httpx.ValidationError("username and password are required")
	}
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
	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)
	if gitUsername != nil {
		if httpx.JSStringLength(*gitUsername) > 100 {
			return httpx.ValidationError("gitUsername must be at most 100 characters")
		}
		sets = append(sets, "git_username = ?")
		args = append(args, nullableProfileString(*gitUsername))
	}
	if gitEmail != nil {
		if *gitEmail != "" {
			if httpx.JSStringLength(*gitEmail) > 254 || !isValidEmail(*gitEmail) {
				return httpx.ValidationError("gitEmail must be a valid email address")
			}
		}
		sets = append(sets, "git_email = ?")
		args = append(args, nullableProfileString(*gitEmail))
	}
	args = append(args, userID)
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
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

func nullableProfileString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func isUsernameUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "username") {
		return false
	}
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

func isSerializationFailure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "40001") ||
		strings.Contains(message, "serialize") ||
		strings.Contains(message, "serialization")
}

// zodEmailBodyPattern mirrors the Zod v4 "practical email" regex
// (node_modules/zod/v4/core/regexes.js) MINUS its two negative lookaheads, which
// RE2 cannot express:
//
//	/^(?!\.)(?!.*\.\.)([A-Za-z0-9_'+\-\.]*)[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$/
//
// The "(?!\.)" (no leading dot) and "(?!.*\.\.)" (no consecutive dots) constraints
// are enforced separately in isValidEmail. This is what z.string().email() applies
// to gitEmail (server/lib/validators/auth.ts), so it must match here rather than
// net/mail.ParseAddress, which is RFC-5322 permissive and wrongly accepts forms Zod
// rejects (e.g. "user@localhost" / "a@b" with no dotted TLD).
var zodEmailBodyPattern = regexp.MustCompile(`^[A-Za-z0-9_'+\-.]*[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

func isValidEmail(v string) bool {
	if strings.HasPrefix(v, ".") {
		return false
	}
	if strings.Contains(v, "..") {
		return false
	}
	return zodEmailBodyPattern.MatchString(v)
}
