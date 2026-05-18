package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxJSONBodyBytes = 4 * 1024 * 1024

type AppError struct {
	Status  int
	Code    string
	Message string
}

func (e *AppError) Error() string {
	return e.Message
}

func Error(status int, code, message string) *AppError {
	return &AppError{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *AppError {
	return Error(http.StatusBadRequest, "BAD_REQUEST", message)
}
func ValidationError(message string) *AppError {
	return Error(http.StatusBadRequest, "VALIDATION_ERROR", message)
}
func Unauthorized(message string) *AppError {
	return Error(http.StatusUnauthorized, "UNAUTHORIZED", message)
}
func TokenExpired(message string) *AppError {
	return Error(http.StatusUnauthorized, "TOKEN_EXPIRED", message)
}
func Forbidden(message string) *AppError { return Error(http.StatusForbidden, "FORBIDDEN", message) }
func NotFound(resource string) *AppError {
	return Error(http.StatusNotFound, "NOT_FOUND", resource+" not found")
}
func NotImplemented(feature string) *AppError {
	return Error(http.StatusNotImplemented, "FEATURE_DISABLED", feature+" is not implemented in go_backend yet")
}

func FeatureDisabledPayload(feature, reason string) map[string]any {
	return FeatureDisabledPayloadWithCode(feature, "FEATURE_DISABLED", reason)
}

func FeatureDisabledPayloadWithCode(feature, code, reason string) map[string]any {
	return map[string]any{
		"ok":        false,
		"code":      code,
		"feature":   feature,
		"supported": false,
		"fallback":  true,
		"reason":    reason,
	}
}

func WriteFeatureDisabled(w http.ResponseWriter, feature, reason string) {
	WriteFeatureDisabledWithCode(w, feature, "FEATURE_DISABLED", reason)
}

func WriteFeatureDisabledWithCode(w http.ResponseWriter, feature, code, reason string) {
	WriteJSON(w, http.StatusNotImplemented, FeatureDisabledPayloadWithCode(feature, code, reason))
}

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteError(w http.ResponseWriter, err error) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		if appErr.Status == http.StatusNotImplemented && appErr.Code == "FEATURE_DISABLED" {
			WriteJSON(w, appErr.Status, FeatureDisabledPayload(appErr.Message, appErr.Message))
			return
		}
		WriteJSON(w, appErr.Status, map[string]any{"error": appErr.Message, "code": appErr.Code})
		return
	}
	WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal server error"})
}

func DecodeJSON(r *http.Request, dst any) error {
	data, err := readLimitedJSONBody(r)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return BadRequest(fmt.Sprintf("invalid JSON: %v", err))
	}
	return nil
}

func DecodeLooseJSON(r *http.Request, dst any) error {
	data, err := readLimitedJSONBody(r)
	if err != nil {
		return err
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(dst); err != nil {
		return BadRequest(fmt.Sprintf("invalid JSON: %v", err))
	}
	return nil
}

func DecodeOptionalLooseJSON(r *http.Request, dst any) error {
	data, err := readLimitedJSONBody(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(dst); err != nil {
		return BadRequest(fmt.Sprintf("invalid JSON: %v", err))
	}
	return nil
}

func readLimitedJSONBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBodyBytes+1))
	if err != nil {
		return nil, BadRequest(fmt.Sprintf("invalid JSON body: %v", err))
	}
	if len(data) > maxJSONBodyBytes {
		return nil, Error(http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", fmt.Sprintf("JSON request body is too large (max %d bytes)", maxJSONBodyBytes))
	}
	return data, nil
}

type HandlerFunc func(http.ResponseWriter, *http.Request) error

func Handler(fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, err)
		}
	}
}
