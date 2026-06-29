package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Claims represents the JWT payload for NarraFork.
//
// The user ID lives in the standard "sub" (Subject) registered claim so that
// tokens are interoperable with the TypeScript backend, which signs { sub,
// role, iat, exp } and reads payload.sub / payload.role. Username is an extra
// custom claim that the TS side ignores harmlessly.
type Claims struct {
	Username string `json:"username,omitempty"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// UserID returns the user ID, which is stored in the standard Subject claim.
func (c *Claims) UserID() string {
	return c.Subject
}

// JWTManager handles JWT token creation and validation.
type JWTManager struct {
	secret []byte
	expiry time.Duration
}

// NewJWTManager creates a JWT manager with the given secret and expiry duration.
func NewJWTManager(secret string, expiry time.Duration) *JWTManager {
	return &JWTManager{
		secret: []byte(secret),
		expiry: expiry,
	}
}

// GenerateToken creates a signed JWT for the given user.
// The user ID is placed in the standard "sub" claim for TS/Go interoperability.
func (m *JWTManager) GenerateToken(userID, username, role string) (string, error) {
	now := time.Now()
	claims := &Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(now.Add(m.expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ValidateToken parses and validates a JWT, returning the claims.
func (m *JWTManager) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		// Pin to HS256 specifically (matches the TS backend's verify(...,"HS256"))
		// so a token can't be downgraded to another HMAC variant or "none".
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}

// HashPassword hashes a password using bcrypt with default cost.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compares a password against a bcrypt hash.
func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateSecret creates a random hex-encoded secret of the given byte length.
// A crypto/rand failure is unrecoverable and would otherwise yield an all-zero
// (forgeable) secret, so we panic rather than return a weak value.
func GenerateSecret(byteLen int) string {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed generating secret: " + err.Error())
	}
	return hex.EncodeToString(b)
}
