package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
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
	return Error(http.StatusNotImplemented, "FEATURE_DISABLED", feature)
}

// JSStringLength mirrors JavaScript String.prototype.length, which counts UTF-16
// code units rather than Unicode code points. The TS backend validates request
// string lengths with Zod .min()/.max(), which check str.length (UTF-16 units),
// so Go length checks must use this instead of utf8.RuneCountInString to avoid
// over-accepting astral characters (an emoji is 1 rune but 2 UTF-16 units, so a
// rune-count limit of N would wrongly accept up to N emoji = 2N UTF-16 units).
func JSStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

// JSStringSlicePrefix mirrors JavaScript value.slice(0, end) operating on UTF-16
// code units (the unit JS String methods use), decoding the kept prefix back to
// a UTF-8 string. Slicing exactly in the middle of an astral character leaves a
// lone surrogate, which utf16.Decode renders as U+FFFD (the standard Go
// replacement). Use this instead of Go byte slicing (value[:n]) when mirroring a
// TS String.prototype.slice/substring whose index is a character count, so
// CJK/emoji inputs are cut at the same boundary as TS rather than at a byte
// offset (which would also risk emitting invalid UTF-8).
func JSStringSlicePrefix(value string, end int) string {
	if end <= 0 {
		return ""
	}
	units := utf16.Encode([]rune(value))
	if end >= len(units) {
		return value
	}
	return string(utf16.Decode(units[:end]))
}

// JSStringSliceFromEnd mirrors JavaScript value.slice(-n) operating on UTF-16
// code units: it returns the last n code units of the string, decoded back to a
// UTF-8 string. Use this instead of Go byte slicing (value[len(value)-n:]) when
// mirroring a TS String.prototype.slice(-n) whose count is a character count, so
// non-ASCII inputs are cut at the same boundary as TS rather than at a byte
// offset (which would also risk emitting invalid UTF-8 mid-rune).
func JSStringSliceFromEnd(value string, n int) string {
	if n <= 0 {
		return ""
	}
	units := utf16.Encode([]rune(value))
	if n >= len(units) {
		return value
	}
	return string(utf16.Decode(units[len(units)-n:]))
}

// ParseIntJS mirrors JavaScript Number.parseInt(value, 10), the parser the TS
// backend uses for integer query/path params (limit, tail, blockIndex, etc.). It
// skips leading ASCII whitespace, accepts an optional +/- sign, then consumes
// the leading run of decimal digits and ignores any trailing characters. It
// returns (n, true) when at least one digit was parsed, or (0, false) when the
// result would be NaN (no leading digits). Unlike strconv.Atoi it does NOT
// require the whole string to be numeric, so "50x" -> 50 and "12.9" -> 12
// exactly like the browser/Bun, instead of failing the whole parse. Callers
// apply their own range clamping/validation after this parse, matching TS.
func ParseIntJS(value string) (int, bool) {
	i := 0
	n := len(value)
	for i < n && (value[i] == ' ' || value[i] == '\t' || value[i] == '\n' || value[i] == '\r' || value[i] == '\f' || value[i] == '\v') {
		i++
	}
	sign := 1
	if i < n && (value[i] == '+' || value[i] == '-') {
		if value[i] == '-' {
			sign = -1
		}
		i++
	}
	start := i
	result := 0
	for i < n && value[i] >= '0' && value[i] <= '9' {
		result = result*10 + int(value[i]-'0')
		i++
	}
	if i == start {
		return 0, false
	}
	return sign * result, true
}

// ParseIntJSOr is the fallback-returning form of ParseIntJS, mirroring the TS
// `Number.isNaN(parseInt(v)) ? fallback : parseInt(v)` idiom.
func ParseIntJSOr(value string, fallback int) int {
	if parsed, ok := ParseIntJS(value); ok {
		return parsed
	}
	return fallback
}

// CoerceNumberJS mirrors JavaScript Number(value), the coercion the TS backend
// applies through Zod's z.coerce.number() for query/body params (git log
// limit/skip, commits limit, usage-history page/pageSize, storage bounds, etc.).
// Unlike ParseIntJS (which mimics parseInt and takes a leading-digit prefix),
// Number() requires the WHOLE trimmed string to be numeric, but it accepts more
// forms than strconv.Atoi: surrounding whitespace, decimals ("50.0"), scientific
// notation ("1e2"), explicit hex/octal/binary literals ("0x10"/"0o17"/"0b101"),
// and the exact tokens "Infinity"/"+Infinity"/"-Infinity". An empty/whitespace
// string coerces to 0. Anything else (e.g. "50x", "5.5x", "inf", "0x10p0",
// underscores) yields NaN, returned here as (0, false). Callers apply their own
// .int()/.min()/.max() range checks after this coercion, matching TS.
func CoerceNumberJS(value string) (float64, bool) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, true // Number("") === 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	// Hex/octal/binary integer literals (JS does not allow a sign on these).
	if len(s) > 2 && s[0] == '0' {
		var base int
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			if v, err := strconv.ParseUint(s[2:], base, 64); err == nil {
				return float64(v), true
			}
			return 0, false
		}
	}
	// Decimal / scientific. strconv.ParseFloat accepts forms JS Number() rejects
	// ("inf"/"nan" case-insensitively, hex floats "0x1p4", underscores), so first
	// restrict to the JS decimal alphabet, then parse.
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			continue
		}
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// CoerceIntJS mirrors Zod z.coerce.number().int() applied to a string: it coerces
// like JS Number() then requires the result to be a finite integer. It returns
// (n, true) on success or (0, false) when the value is NaN, infinite, or has a
// fractional component. Callers apply .min()/.max() bounds afterward.
func CoerceIntJS(value string) (int, bool) {
	f, ok := CoerceNumberJS(value)
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

// urlSpecialSchemes are the WHATWG "special" schemes whose URLs must carry a
// non-empty host. Non-special (opaque) schemes like mailto:/tel:/data: are valid
// without a host, matching the JS URL constructor that Zod v4 .url() uses.
var urlSpecialSchemes = map[string]struct{}{
	"http":  {},
	"https": {},
	"ws":    {},
	"wss":   {},
	"ftp":   {},
	"file":  {}, // file:// allows an empty host, but the scheme itself is special
}

// CoerceURLZod mirrors Zod v4 z.string().url(), which validates by running
// `new URL(value.trim())` and (when it succeeds) STORES the trimmed value. It
// returns (trimmed, true) when the value parses as an absolute URL, or ("", false)
// otherwise. Two behaviours differ from a naive url.Parse + Scheme/Host check:
//
//  1. Leading/trailing ASCII whitespace is trimmed before parsing and the trimmed
//     value is what callers should persist and length-check (TS does
//     `.url().max(n)` against the trimmed value).
//  2. Non-special (opaque) schemes such as mailto:/tel:/data:/javascript: are
//     accepted without a host; only the WHATWG "special" schemes
//     (http/https/ws/wss/ftp/file) require a host.
//
// Known minor deviation: the JS URL constructor strips embedded tab/newline
// characters before parsing while Go's net/url rejects them. Such characters do
// not occur in real webhook/MCP URLs, so this validator does not reimplement the
// full WHATWG cleanup step.
func CoerceURLZod(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return "", false
	}
	if _, special := urlSpecialSchemes[strings.ToLower(parsed.Scheme)]; special {
		// file:// may legitimately have an empty host; every other special scheme
		// requires one (matching `new URL("http://")` throwing).
		if parsed.Host == "" && strings.ToLower(parsed.Scheme) != "file" {
			return "", false
		}
	}
	return trimmed, true
}

func FeatureDisabledPayload(feature, reason string) map[string]any {
	return FeatureDisabledPayloadWithCode(feature, "FEATURE_DISABLED", reason)
}

func FeatureDisabledPayloadWithCode(feature, code, reason string) map[string]any {
	message := reason
	if message == "" {
		message = feature + " is not implemented in go_backend yet"
	}
	return map[string]any{
		"ok":        false,
		"code":      code,
		"feature":   feature,
		"supported": false,
		"fallback":  true,
		"reason":    message,
		"error":     message,
		"message":   message,
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
			feature := appErr.Message
			reason := feature + " is not implemented in go_backend yet"
			WriteJSON(w, appErr.Status, FeatureDisabledPayload(feature, reason))
			return
		}
		WriteJSON(w, appErr.Status, map[string]any{"error": appErr.Message, "message": appErr.Message, "code": appErr.Code})
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
