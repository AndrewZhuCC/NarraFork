package agentbasic

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type providerHTTPError struct {
	StatusCode int
	Body       string
	Provider   string
}

func (e *providerHTTPError) Error() string {
	if strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("%s provider returned status %d: %s", e.Provider, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s provider returned status %d", e.Provider, e.StatusCode)
}

func isSSEContentType(header http.Header) bool {
	contentType := strings.ToLower(header.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

func readProviderErrorBody(res *http.Response) string {
	if res == nil || res.Body == nil {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	return strings.TrimSpace(string(data))
}
