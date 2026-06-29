// Package apperr defines NarraFork's business error type. An AppError carries
// an HTTP status code, a machine-readable Kind, a user-facing message, and an
// optional wrapped cause. The HTTP layer can map these to responses while
// callers can branch on Kind without string-matching messages.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Machine-readable error kinds. These are stable identifiers that clients and
// callers can switch on without parsing human-facing messages.
const (
	KindNotFound     = "NOT_FOUND"
	KindBadRequest   = "BAD_REQUEST"
	KindUnauthorized = "UNAUTHORIZED"
	KindForbidden    = "FORBIDDEN"
	KindConflict     = "CONFLICT"
	KindInternal     = "INTERNAL"
)

// AppError is the base error type for NarraFork business errors.
// It carries an HTTP status code, a machine-readable Kind, and a user-facing
// message, and may wrap an underlying cause.
type AppError struct {
	Code    int    // HTTP status code
	Kind    string // machine-readable code, e.g. "CHAPTER_NOT_FOUND"
	Message string // user-facing message
	Err     error  // wrapped error (optional)
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// StatusCode returns the HTTP status code for this error.
func (e *AppError) StatusCode() int {
	return e.Code
}

// New constructs an AppError with an explicit status code, machine-readable
// kind, and message, optionally wrapping a cause.
//
//	apperr.New(http.StatusNotFound, "CHAPTER_NOT_FOUND", "chapter not found")
//	apperr.New(http.StatusInternalServerError, "DB_WRITE", "save failed", err)
func New(code int, kind, msg string, cause ...error) *AppError {
	return &AppError{Code: code, Kind: kind, Message: msg, Err: firstErr(cause)}
}

// firstErr returns the first non-nil error from a variadic cause list, so the
// convenience constructors can optionally wrap a cause.
func firstErr(cause []error) error {
	for _, c := range cause {
		if c != nil {
			return c
		}
	}
	return nil
}

// Common error constructors. Each accepts an optional wrapped cause.

func NotFound(msg string, cause ...error) *AppError {
	return &AppError{Code: http.StatusNotFound, Kind: KindNotFound, Message: msg, Err: firstErr(cause)}
}

func NotFoundf(format string, args ...any) *AppError {
	return &AppError{Code: http.StatusNotFound, Kind: KindNotFound, Message: fmt.Sprintf(format, args...)}
}

func BadRequest(msg string, cause ...error) *AppError {
	return &AppError{Code: http.StatusBadRequest, Kind: KindBadRequest, Message: msg, Err: firstErr(cause)}
}

func BadRequestf(format string, args ...any) *AppError {
	return &AppError{Code: http.StatusBadRequest, Kind: KindBadRequest, Message: fmt.Sprintf(format, args...)}
}

func Unauthorized(msg string, cause ...error) *AppError {
	return &AppError{Code: http.StatusUnauthorized, Kind: KindUnauthorized, Message: msg, Err: firstErr(cause)}
}

func Forbidden(msg string, cause ...error) *AppError {
	return &AppError{Code: http.StatusForbidden, Kind: KindForbidden, Message: msg, Err: firstErr(cause)}
}

func Conflict(msg string, cause ...error) *AppError {
	return &AppError{Code: http.StatusConflict, Kind: KindConflict, Message: msg, Err: firstErr(cause)}
}

func Internal(msg string, err error) *AppError {
	return &AppError{Code: http.StatusInternalServerError, Kind: KindInternal, Message: msg, Err: err}
}

// IsAppError checks if an error is an AppError and returns it.
func IsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}
