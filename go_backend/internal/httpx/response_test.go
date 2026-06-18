package httpx

import (
	"encoding/json"
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

// TestWriteErrorShapeMatchesCrossStackContract locks the error-response body
// shape that the frontend depends on. The frontend's readFetchError
// (frontend/lib/api/client.ts) reads error text from ["reason","message","error"]
// in order and the code from "code", so Go's WriteError must expose at minimum
// "error", "message", and "code" with message==error, and map AppError.Status to
// the HTTP status. Non-AppError values must fall back to a 500 with a generic
// "error" field (matching the TypeScript app.onError contract).
func TestWriteErrorShapeMatchesCrossStackContract(t *testing.T) {
	t.Run("AppError exposes error+message+code with matching status", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, ValidationError("title is required"))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] != "title is required" || body["message"] != "title is required" || body["code"] != "VALIDATION_ERROR" {
			t.Fatalf("unexpected error body: %#v", body)
		}
		// error and message must carry the same text so the frontend's
		// message-preferred precedence renders identically to TS's error field.
		if body["error"] != body["message"] {
			t.Fatalf("error and message must match, got %#v", body)
		}
	})

	t.Run("non-AppError falls back to 500 internal error", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, errors.New("boom"))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] != "Internal server error" {
			t.Fatalf("unexpected fallback body: %#v", body)
		}
	})

	t.Run("FeatureDisabled AppError emits the fallback envelope", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, NotImplemented("Some feature"))
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("expected 501, got %d", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		for _, key := range []string{"ok", "code", "feature", "supported", "fallback", "reason", "error", "message"} {
			if _, present := body[key]; !present {
				t.Fatalf("fallback envelope missing %q: %#v", key, body)
			}
		}
		if body["supported"] != false || body["fallback"] != true || body["ok"] != false {
			t.Fatalf("unexpected fallback flags: %#v", body)
		}
	})
}
