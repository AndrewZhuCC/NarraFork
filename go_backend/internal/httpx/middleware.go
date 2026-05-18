package httpx

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in HTTP handler", "panic", rec, "stack", string(debug.Stack()))
				WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)
		if duration >= time.Second {
			slog.Warn("slow API request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration", duration.String())
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			w.Header().Set("Vary", "Origin")
			if allowedCORSOrigin(origin, r.Host) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedCORSOrigin(originValue, requestHost string) bool {
	origin, err := url.Parse(originValue)
	if err != nil || origin.Scheme == "" || origin.Host == "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	originHost := normalizeHostWithoutPort(origin.Host)
	requestHost = normalizeHostWithoutPort(requestHost)
	if originHost == "" {
		return false
	}
	if requestHost != "" && strings.EqualFold(originHost, requestHost) {
		return true
	}
	return isLoopbackHost(originHost)
}

func normalizeHostWithoutPort(hostValue string) string {
	hostValue = strings.TrimSpace(hostValue)
	if hostValue == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostValue); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostValue, "[]")
}

func isLoopbackHost(hostValue string) bool {
	if strings.EqualFold(hostValue, "localhost") {
		return true
	}
	ip := net.ParseIP(hostValue)
	return ip != nil && ip.IsLoopback()
}
