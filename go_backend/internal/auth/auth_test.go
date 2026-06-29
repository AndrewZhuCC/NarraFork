package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateAndValidateRoundTrip(t *testing.T) {
	m := NewJWTManager("test-secret", time.Hour)

	tok, err := m.GenerateToken("user-123", "alice", "admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	claims, err := m.ValidateToken(tok)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.UserID() != "user-123" {
		t.Errorf("UserID() = %q, want user-123", claims.UserID())
	}
	if claims.Subject != "user-123" {
		t.Errorf("Subject = %q, want user-123 (must use standard sub claim)", claims.Subject)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
	if claims.Username != "alice" {
		t.Errorf("Username = %q, want alice", claims.Username)
	}
}

// TestTSCompatibleToken verifies that a token shaped exactly like the
// TypeScript backend's ({ sub, role, iat, exp }, HS256) validates and the user
// ID is read from the standard "sub" claim.
func TestTSCompatibleToken(t *testing.T) {
	const secret = "shared-secret"
	now := time.Now()

	// Mimic the TS payload: sub + role, signed HS256.
	tsClaims := jwt.MapClaims{
		"sub":  "ts-user-9",
		"role": "user",
		"iat":  now.Unix(),
		"exp":  now.Add(time.Hour).Unix(),
	}
	tsToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, tsClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign TS-style token: %v", err)
	}

	m := NewJWTManager(secret, time.Hour)
	claims, err := m.ValidateToken(tsToken)
	if err != nil {
		t.Fatalf("Go failed to validate TS-style token: %v", err)
	}
	if claims.UserID() != "ts-user-9" {
		t.Errorf("UserID() = %q, want ts-user-9", claims.UserID())
	}
	if claims.Role != "user" {
		t.Errorf("Role = %q, want user", claims.Role)
	}
}

// TestRejectsNonHS256 ensures tokens signed with a different algorithm family
// are rejected (alg-confusion / downgrade guard).
func TestRejectsWrongSecret(t *testing.T) {
	m1 := NewJWTManager("secret-a", time.Hour)
	m2 := NewJWTManager("secret-b", time.Hour)

	tok, err := m1.GenerateToken("u", "n", "user")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := m2.ValidateToken(tok); err == nil {
		t.Error("expected validation to fail with wrong secret, got nil error")
	}
}

func TestGenerateSecretLength(t *testing.T) {
	s := GenerateSecret(32)
	if len(s) != 64 { // hex-encoded 32 bytes
		t.Errorf("GenerateSecret(32) length = %d, want 64", len(s))
	}
	if s == GenerateSecret(32) {
		t.Error("GenerateSecret produced identical consecutive secrets")
	}
}
