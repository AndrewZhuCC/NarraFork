package auth

import (
	"context"
	"errors"
	"testing"

	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
)

// TestCreateTokenVerifyRoundTrip covers the JWT happy path that the expired-token
// test does not: a freshly created token verifies and yields the original subject
// and role claims. It also confirms a token signed with a different secret is
// rejected, and that UserFromContext extracts (or absents) claims.
func TestCreateTokenVerifyRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	service := NewService(nil, settings)

	token, err := service.CreateToken("user-42", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	claims, err := service.VerifyToken(token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.Subject != "user-42" || claims.Role != "admin" {
		t.Fatalf("round-trip claims mismatch: subject=%q role=%q", claims.Subject, claims.Role)
	}
	if claims.ExpiresAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) {
		t.Fatalf("token should carry a future expiry, got %#v", claims)
	}

	// A token signed under a different JWT secret must be rejected.
	t.Setenv("HOME", t.TempDir())
	otherSettings, err := config.Load()
	if err != nil {
		t.Fatalf("load other config: %v", err)
	}
	if otherSettings.Core().Auth.JWTSecret == settings.Core().Auth.JWTSecret {
		t.Skip("distinct auto-generated JWT secrets required for cross-secret rejection check")
	}
	otherService := NewService(nil, otherSettings)
	otherToken, err := otherService.CreateToken("user-42", "admin")
	if err != nil {
		t.Fatalf("create other token: %v", err)
	}
	if _, err := service.VerifyToken(otherToken); err == nil {
		t.Fatalf("token signed with a different secret should be rejected")
	} else {
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) || appErr.Status != 401 {
			t.Fatalf("expected 401 unauthorized for foreign-secret token, got %T %v", err, err)
		}
	}
}

// TestUserFromContextExtractsClaims covers the context accessor used by AdminOnly
// and handlers: it returns the stored claims when present and nil otherwise.
func TestUserFromContextExtractsClaims(t *testing.T) {
	if got := UserFromContext(context.Background()); got != nil {
		t.Fatalf("UserFromContext on bare context should be nil, got %#v", got)
	}
	want := &Claims{Role: "user"}
	want.Subject = "u-1"
	ctx := context.WithValue(context.Background(), userContextKey, want)
	got := UserFromContext(ctx)
	if got == nil || got.Subject != "u-1" || got.Role != "user" {
		t.Fatalf("UserFromContext should return stored claims, got %#v", got)
	}
}
