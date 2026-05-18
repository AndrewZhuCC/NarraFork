package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst struct {
		Value string `json:"value"`
	}
	err := DecodeJSON(req, &dst)
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T %v", err, err)
	}
	if appErr.Status != http.StatusRequestEntityTooLarge || appErr.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestDecodeLooseJSONRejectsOversizedBody(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst map[string]any
	err := DecodeLooseJSON(req, &dst)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("expected REQUEST_BODY_TOO_LARGE, got %T %v", err, err)
	}
}

func TestDecodeOptionalLooseJSONAllowsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	dst := map[string]any{"keep": true}
	if err := DecodeOptionalLooseJSON(req, &dst); err != nil {
		t.Fatalf("DecodeOptionalLooseJSON returned error for empty body: %v", err)
	}
	if dst["keep"] != true {
		t.Fatalf("empty optional body should not mutate destination: %#v", dst)
	}
}

func TestDecodeOptionalLooseJSONRejectsOversizedBody(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var dst map[string]any
	err := DecodeOptionalLooseJSON(req, &dst)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("expected REQUEST_BODY_TOO_LARGE, got %T %v", err, err)
	}
}

func TestDecodeJSONStillDisallowsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"known":"ok","extra":true}`))
	var dst struct {
		Known string `json:"known"`
	}
	err := DecodeJSON(req, &dst)
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != "BAD_REQUEST" {
		t.Fatalf("expected BAD_REQUEST for unknown field, got %T %v", err, err)
	}
}
