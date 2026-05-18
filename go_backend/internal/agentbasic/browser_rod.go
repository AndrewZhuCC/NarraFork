package agentbasic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"

	"narrafork-go/internal/id"
)

const (
	browserDefaultMaxLength = 20_000
	browserMaxOutputLength  = 200_000
	browserDefaultTimeoutMs = 10_000
	browserLaunchTimeoutMs  = 30_000
	browserViewportWidth    = 1280
	browserViewportHeight   = 900
	browserUserAgent        = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	browserDefaultSessionTTL = 10 * time.Minute
	browserMinSessionTTL     = time.Second
	browserMaxSessionTTL     = 24 * time.Hour
	browserCleanupInterval   = time.Minute
	browserMaxConsoleEntries = 200
	browserMaxNetworkEntries = 500
	browserMaxPostDataLength = 4_000
)

var browserDefaultTraceCategories = []string{
	"devtools.timeline",
	"v8.execute",
	"disabled-by-default-devtools.timeline",
	"disabled-by-default-devtools.timeline.frame",
	"toplevel",
	"blink.console",
	"blink.user_timing",
	"loading",
}

type browserSession struct {
	mu sync.Mutex

	ID                    string             `json:"id"`
	URL                   string             `json:"url"`
	Title                 string             `json:"title"`
	Headless              bool               `json:"headless"`
	ChromePath            string             `json:"chromePath,omitempty"`
	Browser               *rod.Browser       `json:"-"`
	Page                  *rod.Page          `json:"-"`
	Launcher              *launcher.Launcher `json:"-"`
	CreatedAt             time.Time          `json:"createdAt"`
	UpdatedAt             time.Time          `json:"updatedAt"`
	LastActivity          time.Time          `json:"lastActivity"`
	TtlMs                 int64              `json:"ttlMs"`
	NetworkCaptureEnabled bool               `json:"networkCaptureEnabled"`
	ConsoleMessages       []browserConsoleMessage
	ConsoleMessageSeq     int
	NetworkRequests       []browserNetworkRequest
	NetworkRequestMap     map[proto.NetworkRequestID]*browserNetworkRequest
	Tracing               *browserTracing
	eventCancel           context.CancelFunc
}

type browserSessionSummary struct {
	ID                    string    `json:"id"`
	URL                   string    `json:"url"`
	Title                 string    `json:"title"`
	Headless              bool      `json:"headless"`
	ChromePath            string    `json:"chromePath,omitempty"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
	LastActivity          time.Time `json:"lastActivity"`
	TtlMs                 int64     `json:"ttlMs"`
	ExpiresAt             time.Time `json:"expiresAt"`
	NetworkCaptureEnabled bool      `json:"networkCaptureEnabled"`
	NetworkRequestCount   int       `json:"networkRequestCount"`
	ConsoleMessageCount   int       `json:"consoleMessageCount"`
	TracingActive         bool      `json:"tracingActive"`
}

type browserConsoleLocation struct {
	URL          string `json:"url,omitempty"`
	LineNumber   int    `json:"lineNumber,omitempty"`
	ColumnNumber int    `json:"columnNumber,omitempty"`
}

type browserConsoleMessage struct {
	Type      string                  `json:"type"`
	Text      string                  `json:"text"`
	Args      []string                `json:"args,omitempty"`
	Seq       int                     `json:"seq"`
	Timestamp time.Time               `json:"timestamp"`
	Location  *browserConsoleLocation `json:"location,omitempty"`
}

type browserNetworkRequest struct {
	ID              string            `json:"id"`
	RequestID       string            `json:"requestId"`
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	ResourceType    string            `json:"resourceType"`
	StartedAt       time.Time         `json:"startedAt"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	PostData        string            `json:"postData,omitempty"`
	Status          *int              `json:"status,omitempty"`
	StatusText      string            `json:"statusText,omitempty"`
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	FinishedAt      *time.Time        `json:"finishedAt,omitempty"`
	DurationMs      *int64            `json:"durationMs,omitempty"`
	Failed          bool              `json:"failed,omitempty"`
	FailureText     string            `json:"failureText,omitempty"`
}

type browserTracing struct {
	Active           bool
	StartedAt        time.Time
	Events           []map[string]any
	Done             chan struct{}
	DataLossOccurred bool
}

type browserCoordinate struct {
	X float64
	Y float64
}

var browserSessions = struct {
	sync.Mutex
	items        map[string]*browserSession
	cleanupTimer *time.Timer
}{items: map[string]*browserSession{}}

func toolBrowser(ctx context.Context, cwd string, args map[string]any) string {
	action := strings.TrimSpace(stringValue(args["action"]))
	if action == "" {
		return "action is required"
	}

	maxLength := normalizeBrowserMaxLength(intValue(args["max_length"], browserDefaultMaxLength))
	timeoutMs := normalizeBrowserTimeoutMs(intValue(args["timeout"], browserDefaultTimeoutMs))
	headless := boolArg(args, "headless", true)
	coordinate, hasCoordinate := coordinateValue(args["coordinate"])

	switch action {
	case "launch":
		return browserLaunch(ctx, stringValue(args["url"]), headless, intValue(args["ttl_ms"], int(browserDefaultSessionTTL/time.Millisecond)), boolValue(args["capture_network"]), timeoutMs, maxLength)
	case "navigate":
		return browserNavigate(ctx, stringValue(args["session_id"]), stringValue(args["url"]), strings.TrimSpace(stringValue(args["direction"])), timeoutMs, maxLength)
	case "dom":
		return browserDOM(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), timeoutMs, maxLength)
	case "get_text":
		return browserText(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), timeoutMs, maxLength)
	case "click":
		return browserClick(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), coordinate, hasCoordinate, timeoutMs)
	case "fill":
		return browserFill(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), stringValue(args["value"]), timeoutMs)
	case "select":
		return browserSelect(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), stringValue(args["value"]), timeoutMs)
	case "type":
		return browserType(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), stringValue(args["value"]), stringValue(args["key"]), timeoutMs)
	case "hover":
		return browserHover(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), timeoutMs)
	case "screenshot":
		return browserScreenshot(ctx, cwd, stringValue(args["session_id"]), pathValue(args["file_path"]), boolValue(args["full_page"]) || boolValue(args["fullPage"]), timeoutMs, maxLength)
	case "get_attribute":
		return browserGetAttribute(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), stringValue(args["value"]), timeoutMs)
	case "get_console":
		return browserGetConsole(ctx, stringValue(args["session_id"]), boolValue(args["clear"]), maxLength)
	case "get_network":
		return browserGetNetwork(ctx, stringValue(args["session_id"]), stringValue(args["value"]), boolValue(args["include_details"]), boolValue(args["clear"]), maxLength)
	case "network_start":
		return browserNetworkStart(stringValue(args["session_id"]), boolValue(args["clear"]))
	case "network_stop":
		return browserNetworkStop(stringValue(args["session_id"]))
	case "evaluate":
		return browserEvaluate(ctx, stringValue(args["session_id"]), stringValue(args["value"]), timeoutMs, maxLength)
	case "evaluate_capture":
		return browserEvaluateCapture(ctx, stringValue(args["session_id"]), stringValue(args["value"]), boolArg(args, "clear", true), intValue(args["wait_after_ms"], 0), timeoutMs, maxLength)
	case "wait":
		return browserWait(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), stringValue(args["state"]), timeoutMs)
	case "scroll":
		return browserScroll(ctx, stringValue(args["session_id"]), stringValue(args["selector"]), coordinate, hasCoordinate, strings.TrimSpace(stringValue(args["direction"])), intValue(args["amount"], 500), timeoutMs)
	case "set_ttl":
		return browserSetTTL(stringValue(args["session_id"]), intValue(args["ttl_ms"], 0))
	case "list_sessions":
		return browserListSessions()
	case "perf_start":
		return browserPerfStart(ctx, stringValue(args["session_id"]), stringList(args["categories"]), timeoutMs)
	case "perf_stop":
		return browserPerfStop(ctx, cwd, stringValue(args["session_id"]), pathValue(args["file_path"]), timeoutMs)
	case "close":
		return browserClose(stringValue(args["session_id"]))
	default:
		return "Browser action unsupported in Go rod implementation: " + action
	}
}

func browserLaunch(ctx context.Context, rawURL string, headless bool, ttlMs int, captureNetwork bool, timeoutMs int, maxLength int) string {
	if err := validateBrowserURL(rawURL); err != nil {
		return err.Error()
	}
	if err := ensureHeadedBrowserAllowed(headless); err != nil {
		return err.Error()
	}
	ttl, err := normalizeBrowserSessionTTL(time.Duration(ttlMs) * time.Millisecond)
	if err != nil {
		return err.Error()
	}

	chromePath, err := findChromePath()
	if err != nil {
		return err.Error()
	}

	launchCtx, cancel := context.WithTimeout(ctx, time.Duration(browserLaunchTimeoutMs)*time.Millisecond)
	defer cancel()

	l := buildRodLauncher(launchCtx, chromePath, headless)
	controlURL, err := l.Launch()
	if err != nil {
		if isFlatpakChromePath(chromePath) {
			return "Chrome was found at a Flatpak path but cannot be launched directly. Please install Chrome natively (deb/rpm), or run: bunx puppeteer browsers install chrome"
		}
		return "Chrome was found but failed to launch. Please install Chrome or run: bunx puppeteer browsers install chrome"
	}

	browser := rod.New().ControlURL(controlURL)
	connectBrowser := browser.Context(launchCtx)
	if err := connectBrowser.Connect(); err != nil {
		l.Kill()
		go l.Cleanup()
		return "Chrome was found but failed to launch. Please install Chrome or run: bunx puppeteer browsers install chrome"
	}
	browser = connectBrowser.Context(context.Background())

	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		_ = browser.Close()
		l.Kill()
		go l.Cleanup()
		return err.Error()
	}
	if err := setPageDefaults(page); err != nil {
		_ = browser.Close()
		l.Kill()
		go l.Cleanup()
		return err.Error()
	}

	now := time.Now().UTC()
	session := &browserSession{
		ID:                    id.Must(8),
		Headless:              headless,
		ChromePath:            chromePath,
		Browser:               browser,
		Page:                  page,
		Launcher:              l,
		CreatedAt:             now,
		UpdatedAt:             now,
		LastActivity:          now,
		TtlMs:                 int64(ttl / time.Millisecond),
		NetworkCaptureEnabled: captureNetwork,
		ConsoleMessages:       []browserConsoleMessage{},
		NetworkRequests:       []browserNetworkRequest{},
		NetworkRequestMap:     map[proto.NetworkRequestID]*browserNetworkRequest{},
	}
	if err := session.attachListeners(); err != nil {
		session.close()
		return err.Error()
	}
	if err := navigateRodPage(ctx, page, rawURL, timeoutMs); err != nil {
		session.close()
		return err.Error()
	}
	_ = session.refreshSnapshotLocked(ctx, timeoutMs)

	browserSessions.Lock()
	browserSessions.items[session.ID] = session
	browserSessions.Unlock()
	ensureBrowserCleanupTimer()

	text, _ := pageText(ctx, page, "", timeoutMs)
	return jsonTool(map[string]any{
		"session_id":            session.ID,
		"url":                   session.URL,
		"title":                 session.Title,
		"text":                  truncateString(text, maxLength),
		"ttlMs":                 session.TtlMs,
		"networkCaptureEnabled": session.NetworkCaptureEnabled,
	})
}

func browserNavigate(ctx context.Context, sessionID, rawURL, direction string, timeoutMs int, maxLength int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction == "back" {
		err = session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).NavigateBack()
	} else if direction == "forward" {
		err = session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).NavigateForward()
	} else {
		if err = validateBrowserURL(rawURL); err != nil {
			return err.Error()
		}
		err = navigateRodPage(ctx, session.Page, rawURL, timeoutMs)
	}
	if err != nil {
		return err.Error()
	}
	_ = session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).WaitLoad()
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	text, _ := pageText(ctx, session.Page, "", timeoutMs)
	return jsonTool(map[string]any{"session_id": session.ID, "url": session.URL, "title": session.Title, "text": truncateString(text, maxLength)})
}

func browserDOM(ctx context.Context, sessionID, selector string, timeoutMs int, maxLength int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	var html string
	if strings.TrimSpace(selector) != "" {
		obj, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs)*time.Millisecond).Eval(`(selector) => Array.from(document.querySelectorAll(selector)).map(el => el.outerHTML).join("\n")`, selector)
		if err != nil {
			return err.Error()
		}
		html, _ = remoteObjectValue(obj).(string)
		if html == "" {
			html = "No elements found matching selector: " + selector
		}
	} else {
		obj, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Eval(`() => document.body ? document.body.innerHTML : ""`)
		if err != nil {
			return err.Error()
		}
		html, _ = remoteObjectValue(obj).(string)
	}
	session.touchLocked()
	return truncateString(cleanBrowserHTML(html), maxLength)
}

func browserText(ctx context.Context, sessionID, selector string, timeoutMs int, maxLength int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	text, err := pageText(ctx, session.Page, selector, timeoutMs)
	if err != nil {
		return err.Error()
	}
	session.touchLocked()
	return truncateString(text, maxLength)
}

func browserClick(ctx context.Context, sessionID, selector string, coordinate browserCoordinate, hasCoordinate bool, timeoutMs int) string {
	if strings.TrimSpace(selector) == "" && !hasCoordinate {
		return "selector or coordinate is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	if hasCoordinate {
		if err := session.Page.Mouse.MoveTo(proto.NewPoint(coordinate.X, coordinate.Y)); err != nil {
			return err.Error()
		}
		if err := session.Page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return err.Error()
		}
	} else {
		el, err := browserElement(ctx, session.Page, selector, timeoutMs)
		if err != nil {
			return err.Error()
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return err.Error()
		}
	}
	browserShortSettle(ctx, 500*time.Millisecond)
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserFill(ctx context.Context, sessionID, selector, value string, timeoutMs int) string {
	if strings.TrimSpace(selector) == "" {
		return "selector is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	el, err := browserElement(ctx, session.Page, selector, timeoutMs)
	if err != nil {
		return err.Error()
	}
	_, err = el.Eval(`function(value) {
		const el = this;
		el.focus();
		if ("value" in el) {
			el.value = value;
			el.dispatchEvent(new Event("input", { bubbles: true }));
			el.dispatchEvent(new Event("change", { bubbles: true }));
		} else {
			el.textContent = value;
			el.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText", data: value }));
		}
	}`, value)
	if err != nil {
		return err.Error()
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserSelect(ctx context.Context, sessionID, selector, value string, timeoutMs int) string {
	if strings.TrimSpace(selector) == "" {
		return "selector is required"
	}
	if value == "" {
		return "value is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	el, err := browserElement(ctx, session.Page, selector, timeoutMs)
	if err != nil {
		return err.Error()
	}
	_, err = el.Eval(`function(value) {
		if (!("value" in this)) throw new Error("Element does not support value selection");
		this.value = value;
		this.dispatchEvent(new Event("input", { bubbles: true }));
		this.dispatchEvent(new Event("change", { bubbles: true }));
		return this.value;
	}`, value)
	if err != nil {
		return err.Error()
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserType(ctx context.Context, sessionID, selector, value, key string, timeoutMs int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	if strings.TrimSpace(selector) != "" {
		el, err := browserElement(ctx, session.Page, selector, timeoutMs)
		if err != nil {
			return err.Error()
		}
		if err := el.Focus(); err != nil {
			return err.Error()
		}
	}

	if strings.TrimSpace(key) != "" {
		keys, err := browserKeys(key)
		if err != nil {
			return err.Error()
		}
		if err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Keyboard.Type(keys...); err != nil {
			return err.Error()
		}
	} else if value != "" {
		if err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).InsertText(value); err != nil {
			return err.Error()
		}
	} else {
		return "value or key is required"
	}

	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserHover(ctx context.Context, sessionID, selector string, timeoutMs int) string {
	if strings.TrimSpace(selector) == "" {
		return "selector is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	el, err := browserElement(ctx, session.Page, selector, timeoutMs)
	if err != nil {
		return err.Error()
	}
	if err := el.Hover(); err != nil {
		return err.Error()
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserScreenshot(ctx context.Context, cwd, sessionID, filePath string, fullPage bool, timeoutMs int, maxLength int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	data, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs)*time.Millisecond).Screenshot(fullPage, &proto.PageCaptureScreenshot{Format: proto.PageCaptureScreenshotFormatPng, FromSurface: true})
	if err != nil {
		return err.Error()
	}
	if strings.TrimSpace(filePath) != "" {
		resolved, err := resolveWithin(cwd, filePath)
		if err != nil {
			return err.Error()
		}
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return err.Error()
		}
		if err := os.WriteFile(resolved, data, 0o644); err != nil {
			return err.Error()
		}
		if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
			return err.Error()
		}
		return jsonTool(map[string]any{"session_id": session.ID, "mime": "image/png", "bytes": len(data), "file_path": resolved, "snapshot": session.snapshotLocked()})
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{
		"session_id":      session.ID,
		"mime":            "image/png",
		"bytes":           len(data),
		"base64":          truncateString(encoded, maxLength),
		"base64Length":    len(encoded),
		"base64Truncated": len(encoded) > maxLength,
		"snapshot":        session.snapshotLocked(),
	})
}

func browserGetAttribute(ctx context.Context, sessionID, selector, attribute string, timeoutMs int) string {
	if strings.TrimSpace(selector) == "" {
		return "selector is required"
	}
	if strings.TrimSpace(attribute) == "" {
		return "value (attribute name) is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	el, err := browserElement(ctx, session.Page, selector, timeoutMs)
	if err != nil {
		return err.Error()
	}
	value, err := el.Attribute(attribute)
	if err != nil {
		return err.Error()
	}
	session.touchLocked()
	return jsonTool(map[string]any{"session_id": session.ID, "attribute": attribute, "value": value, "snapshot": session.snapshotLocked()})
}

func browserGetConsole(ctx context.Context, sessionID string, clear bool, maxLength int) string {
	_ = ctx
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.touchLocked()
	messages := append([]browserConsoleMessage(nil), session.ConsoleMessages...)
	output := formatConsoleMessages(messages)
	if output == "" {
		output = "No console messages captured."
	}
	if clear {
		session.ConsoleMessages = nil
	}
	return jsonTool(map[string]any{"output": truncateString(output, maxLength), "count": len(messages), "cleared": clear, "snapshot": session.snapshotLocked()})
}

func browserGetNetwork(ctx context.Context, sessionID, filter string, includeDetails, clear bool, maxLength int) string {
	_ = ctx
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.touchLocked()
	total := len(session.NetworkRequests)
	requests := filterNetworkRequests(session.NetworkRequests, filter)
	output := formatNetworkRequests(requests, includeDetails)
	if output == "" {
		if !session.NetworkCaptureEnabled && total == 0 {
			output = "Network capture is not enabled. Use network_start first, or launch with capture_network=true to capture initial page requests."
		} else if strings.TrimSpace(filter) != "" {
			output = "No network requests matched filter: " + filter
		} else {
			output = "No network requests captured."
		}
	}
	if clear {
		session.NetworkRequests = nil
		session.NetworkRequestMap = map[proto.NetworkRequestID]*browserNetworkRequest{}
	}
	return jsonTool(map[string]any{"output": truncateString(output, maxLength), "count": len(requests), "totalCount": total, "cleared": clear, "networkCaptureEnabled": session.NetworkCaptureEnabled, "snapshot": session.snapshotLocked()})
}

func browserNetworkStart(sessionID string, clear bool) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if clear {
		session.NetworkRequests = nil
		session.NetworkRequestMap = map[proto.NetworkRequestID]*browserNetworkRequest{}
	}
	session.NetworkCaptureEnabled = true
	session.touchLocked()
	return jsonTool(map[string]any{"session_id": session.ID, "networkCaptureEnabled": true, "networkRequestCount": len(session.NetworkRequests), "cleared": clear})
}

func browserNetworkStop(sessionID string) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.NetworkCaptureEnabled = false
	session.touchLocked()
	return jsonTool(map[string]any{"session_id": session.ID, "networkCaptureEnabled": false, "networkRequestCount": len(session.NetworkRequests)})
}

func browserEvaluate(ctx context.Context, sessionID, expression string, timeoutMs int, maxLength int) string {
	if strings.TrimSpace(expression) == "" {
		return "value is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	obj, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Eval(normalizeBrowserExpression(expression))
	if err != nil {
		return err.Error()
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	result := formatRemoteObject(obj)
	return jsonTool(map[string]any{"result": truncateString(result, maxLength), "snapshot": session.snapshotLocked()})
}

func browserEvaluateCapture(ctx context.Context, sessionID, expression string, clear bool, waitAfterMs int, timeoutMs int, maxLength int) string {
	if strings.TrimSpace(expression) == "" {
		return "value is required"
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	startedAt := time.Now()
	startSeq := 0
	session.mu.Lock()
	if clear {
		session.ConsoleMessages = nil
	}
	startSeq = session.ConsoleMessageSeq
	session.mu.Unlock()

	result := ""
	isError := false
	errorText := ""
	session.mu.Lock()
	obj, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Eval(normalizeBrowserExpression(expression))
	if err != nil {
		isError = true
		errorText = err.Error()
		result = "Evaluation failed: " + err.Error()
	} else {
		result = formatRemoteObject(obj)
	}
	session.mu.Unlock()

	if waitAfterMs > 0 {
		browserShortSettle(ctx, time.Duration(waitAfterMs)*time.Millisecond)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	_ = session.refreshSnapshotLocked(ctx, timeoutMs)
	captured := []browserConsoleMessage{}
	for _, message := range session.ConsoleMessages {
		if message.Seq > startSeq {
			captured = append(captured, message)
		}
	}
	consoleOutput := formatConsoleMessages(captured)
	if consoleOutput == "" {
		consoleOutput = "No console messages captured during script."
	}
	return jsonTool(map[string]any{
		"result":        truncateString(result, maxLength),
		"consoleOutput": truncateString(consoleOutput, maxLength),
		"consoleCount":  len(captured),
		"durationMs":    time.Since(startedAt).Milliseconds(),
		"isError":       isError,
		"error":         errorText,
		"snapshot":      session.snapshotLocked(),
	})
}

func browserWait(ctx context.Context, sessionID, selector, state string, timeoutMs int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	if strings.TrimSpace(selector) != "" {
		if err := waitForSelectorState(ctx, session.Page, selector, state, timeoutMs); err != nil {
			return err.Error()
		}
	} else {
		browserShortSettle(ctx, time.Duration(timeoutMs)*time.Millisecond)
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserScroll(ctx context.Context, sessionID, selector string, coordinate browserCoordinate, hasCoordinate bool, direction string, amount int, timeoutMs int) string {
	if amount <= 0 {
		amount = 500
	}
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	direction = strings.ToLower(strings.TrimSpace(direction))
	var deltaX, deltaY int
	switch direction {
	case "up":
		deltaY = -amount
	case "left":
		deltaX = -amount
	case "right":
		deltaX = amount
	default:
		deltaY = amount
	}

	if strings.TrimSpace(selector) != "" {
		el, err := browserElement(ctx, session.Page, selector, timeoutMs)
		if err != nil {
			return err.Error()
		}
		_, err = el.Eval(`function(dx, dy) {
			const el = this;
			if (typeof el.scrollBy === "function") {
				el.scrollBy(dx, dy);
			} else {
				el.scrollTop += dy;
				el.scrollLeft += dx;
			}
		}`, deltaX, deltaY)
		if err != nil {
			return err.Error()
		}
	} else if hasCoordinate {
		if err := session.Page.Mouse.MoveTo(proto.NewPoint(coordinate.X, coordinate.Y)); err != nil {
			return err.Error()
		}
		if err := session.Page.Mouse.Scroll(float64(deltaX), float64(deltaY), 1); err != nil {
			return err.Error()
		}
	} else {
		_, err := session.Page.Context(ctx).Timeout(time.Duration(timeoutMs)*time.Millisecond).Eval(`(dx, dy) => window.scrollBy(dx, dy)`, deltaX, deltaY)
		if err != nil {
			return err.Error()
		}
	}
	if err := session.refreshSnapshotLocked(ctx, timeoutMs); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "snapshot": session.snapshotLocked()})
}

func browserSetTTL(sessionID string, ttlMs int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	ttl, err := normalizeBrowserSessionTTL(time.Duration(ttlMs) * time.Millisecond)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.TtlMs = int64(ttl / time.Millisecond)
	session.touchLocked()
	ensureBrowserCleanupTimer()
	return jsonTool(map[string]any{"session_id": session.ID, "ttlMs": session.TtlMs, "expiresAt": session.LastActivity.Add(ttl)})
}

func browserPerfStart(ctx context.Context, sessionID string, categories []string, timeoutMs int) string {
	_ = ctx
	_ = timeoutMs
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.Tracing != nil && session.Tracing.Active {
		return "Tracing is already active on this session"
	}
	if len(categories) == 0 {
		categories = append([]string(nil), browserDefaultTraceCategories...)
	}
	trace := &browserTracing{Active: true, StartedAt: time.Now(), Events: []map[string]any{}, Done: make(chan struct{})}
	session.Tracing = trace
	if err := (proto.TracingStart{Categories: strings.Join(categories, ","), TransferMode: proto.TracingStartTransferModeReportEvents}).Call(session.Browser); err != nil {
		session.Tracing = nil
		return err.Error()
	}
	session.touchLocked()
	return jsonTool(map[string]any{"session_id": session.ID, "tracingStatus": "active", "categories": categories})
}

func browserPerfStop(ctx context.Context, cwd, sessionID, filePath string, timeoutMs int) string {
	session, err := browserRequireSession(sessionID)
	if err != nil {
		return err.Error()
	}

	session.mu.Lock()
	trace := session.Tracing
	if trace == nil || !trace.Active {
		session.mu.Unlock()
		return "No active tracing on this session"
	}
	session.mu.Unlock()

	if err := (proto.TracingEnd{}).Call(session.Browser); err != nil {
		return err.Error()
	}
	select {
	case <-trace.Done:
	case <-ctx.Done():
		return ctx.Err().Error()
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		return fmt.Sprintf("Tracing stop timed out after %dms", timeoutMs)
	}

	session.mu.Lock()
	events := append([]map[string]any(nil), trace.Events...)
	durationMs := time.Since(trace.StartedAt).Milliseconds()
	trace.Active = false
	session.Tracing = nil
	session.touchLocked()
	snapshot := session.snapshotLocked()
	session.mu.Unlock()

	if strings.TrimSpace(filePath) == "" {
		filePath = fmt.Sprintf("trace-%s-%d.json", session.ID, time.Now().UnixMilli())
	}
	resolved, err := resolveWithin(cwd, filePath)
	if err != nil {
		return err.Error()
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return err.Error()
	}
	payload := map[string]any{"traceEvents": events}
	data, err := json.Marshal(payload)
	if err != nil {
		return err.Error()
	}
	if err := os.WriteFile(resolved, data, 0o644); err != nil {
		return err.Error()
	}
	return jsonTool(map[string]any{"session_id": session.ID, "file_path": resolved, "fileSize": len(data), "durationMs": durationMs, "dataLossOccurred": trace.DataLossOccurred, "snapshot": snapshot})
}

func browserListSessions() string {
	browserSessions.Lock()
	items := make([]*browserSession, 0, len(browserSessions.items))
	for id, session := range browserSessions.items {
		if session.isExpired(time.Now()) {
			delete(browserSessions.items, id)
			go session.close()
			continue
		}
		items = append(items, session)
	}
	browserSessions.Unlock()

	summaries := make([]browserSessionSummary, 0, len(items))
	for _, session := range items {
		session.mu.Lock()
		ttl := time.Duration(session.TtlMs) * time.Millisecond
		tracingActive := session.Tracing != nil && session.Tracing.Active
		summaries = append(summaries, browserSessionSummary{
			ID:                    session.ID,
			URL:                   session.URL,
			Title:                 session.Title,
			Headless:              session.Headless,
			ChromePath:            session.ChromePath,
			CreatedAt:             session.CreatedAt,
			UpdatedAt:             session.UpdatedAt,
			LastActivity:          session.LastActivity,
			TtlMs:                 session.TtlMs,
			ExpiresAt:             session.LastActivity.Add(ttl),
			NetworkCaptureEnabled: session.NetworkCaptureEnabled,
			NetworkRequestCount:   len(session.NetworkRequests),
			ConsoleMessageCount:   len(session.ConsoleMessages),
			TracingActive:         tracingActive,
		})
		session.mu.Unlock()
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt) })
	return jsonTool(map[string]any{"sessions": summaries})
}

func browserClose(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "session_id is required"
	}
	browserSessions.Lock()
	session, existed := browserSessions.items[sessionID]
	delete(browserSessions.items, sessionID)
	browserSessions.Unlock()
	if existed {
		session.close()
	}
	return jsonTool(map[string]any{"ok": existed, "closed": sessionID})
}

func ActiveBrowserSessionIDs() []string {
	browserSessions.Lock()
	defer browserSessions.Unlock()
	ids := make([]string, 0, len(browserSessions.items))
	for id := range browserSessions.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func CloseAllBrowserSessions() []string {
	browserSessions.Lock()
	ids := make([]string, 0, len(browserSessions.items))
	toClose := make([]*browserSession, 0, len(browserSessions.items))
	for id, session := range browserSessions.items {
		ids = append(ids, id)
		toClose = append(toClose, session)
		delete(browserSessions.items, id)
	}
	if browserSessions.cleanupTimer != nil {
		browserSessions.cleanupTimer.Stop()
		browserSessions.cleanupTimer = nil
	}
	browserSessions.Unlock()
	sort.Strings(ids)
	for _, session := range toClose {
		session.close()
	}
	return ids
}

func browserRequireSession(sessionID string) (*browserSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	browserSessions.Lock()
	session, ok := browserSessions.items[sessionID]
	if ok && session.isExpired(time.Now()) {
		delete(browserSessions.items, sessionID)
		browserSessions.Unlock()
		session.close()
		return nil, errors.New("browser session not found or expired")
	}
	browserSessions.Unlock()
	if !ok {
		return nil, errors.New("browser session not found")
	}
	return session, nil
}

func (s *browserSession) attachListeners() error {
	maxPost := browserMaxPostDataLength
	if err := (proto.RuntimeEnable{}).Call(s.Page); err != nil {
		return err
	}
	if err := (proto.NetworkEnable{MaxPostDataSize: &maxPost}).Call(s.Page); err != nil {
		return err
	}
	eventCtx, cancel := context.WithCancel(context.Background())
	s.eventCancel = cancel
	go s.Page.Context(eventCtx).EachEvent(
		func(e *proto.RuntimeConsoleAPICalled) {
			s.handleConsole(e)
		},
		func(e *proto.RuntimeExceptionThrown) {
			s.handlePageError(e)
		},
		func(e *proto.NetworkRequestWillBeSent) {
			s.handleNetworkRequest(e)
		},
		func(e *proto.NetworkResponseReceived) {
			s.handleNetworkResponse(e)
		},
		func(e *proto.NetworkLoadingFailed) {
			s.handleNetworkFailure(e)
		},
	)()
	go s.Browser.Context(eventCtx).EachEvent(
		func(e *proto.TracingDataCollected) {
			s.handleTracingData(e)
		},
		func(e *proto.TracingTracingComplete) {
			s.handleTracingComplete(e)
		},
	)()
	return nil
}

func (s *browserSession) handleConsole(e *proto.RuntimeConsoleAPICalled) {
	if e == nil {
		return
	}
	args := make([]string, 0, len(e.Args))
	for _, arg := range e.Args {
		args = append(args, formatRemoteObject(arg))
	}
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		text = string(e.Type)
	}
	location := consoleLocationFromStack(e.StackTrace)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushConsoleLocked(string(e.Type), text, args, location)
}

func (s *browserSession) handlePageError(e *proto.RuntimeExceptionThrown) {
	if e == nil || e.ExceptionDetails == nil {
		return
	}
	details := e.ExceptionDetails
	text := details.Text
	if details.Exception != nil {
		text = formatRemoteObject(details.Exception)
	}
	if text == "" {
		text = "Unhandled page exception"
	}
	location := &browserConsoleLocation{URL: details.URL, LineNumber: details.LineNumber, ColumnNumber: details.ColumnNumber}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushConsoleLocked("pageerror", text, nil, location)
}

func (s *browserSession) handleNetworkRequest(e *proto.NetworkRequestWillBeSent) {
	if e == nil || e.Request == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.NetworkCaptureEnabled {
		return
	}
	entry := browserNetworkRequest{
		ID:             id.Must(8),
		RequestID:      string(e.RequestID),
		URL:            redactBrowserURL(e.Request.URL),
		Method:         e.Request.Method,
		ResourceType:   string(e.Type),
		StartedAt:      time.Now().UTC(),
		RequestHeaders: redactHeaders(protoHeadersToStrings(e.Request.Headers)),
		PostData:       truncatePostData(redactPostData(e.Request.PostData)),
	}
	if entry.ResourceType == "" {
		entry.ResourceType = "Other"
	}
	s.NetworkRequests = append(s.NetworkRequests, entry)
	idx := len(s.NetworkRequests) - 1
	s.NetworkRequestMap[e.RequestID] = &s.NetworkRequests[idx]
	if len(s.NetworkRequests) > browserMaxNetworkEntries {
		removed := s.NetworkRequests[:len(s.NetworkRequests)-browserMaxNetworkEntries]
		for _, item := range removed {
			delete(s.NetworkRequestMap, proto.NetworkRequestID(item.RequestID))
		}
		s.NetworkRequests = s.NetworkRequests[len(s.NetworkRequests)-browserMaxNetworkEntries:]
		s.rebuildNetworkMapLocked()
	}
}

func (s *browserSession) handleNetworkResponse(e *proto.NetworkResponseReceived) {
	if e == nil || e.Response == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.NetworkRequestMap[e.RequestID]
	if item == nil {
		return
	}
	status := e.Response.Status
	item.Status = &status
	item.StatusText = e.Response.StatusText
	item.ResponseHeaders = redactHeaders(protoHeadersToStrings(e.Response.Headers))
	now := time.Now().UTC()
	item.FinishedAt = &now
	duration := now.Sub(item.StartedAt).Milliseconds()
	item.DurationMs = &duration
	if item.ResourceType == "" {
		item.ResourceType = string(e.Type)
	}
}

func (s *browserSession) handleNetworkFailure(e *proto.NetworkLoadingFailed) {
	if e == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.NetworkRequestMap[e.RequestID]
	if item == nil {
		return
	}
	item.Failed = true
	item.FailureText = e.ErrorText
	now := time.Now().UTC()
	item.FinishedAt = &now
	duration := now.Sub(item.StartedAt).Milliseconds()
	item.DurationMs = &duration
	if item.ResourceType == "" {
		item.ResourceType = string(e.Type)
	}
}

func (s *browserSession) handleTracingData(e *proto.TracingDataCollected) {
	if e == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Tracing == nil || !s.Tracing.Active {
		return
	}
	for _, item := range e.Value {
		s.Tracing.Events = append(s.Tracing.Events, gsonMapToAny(item))
	}
}

func (s *browserSession) handleTracingComplete(e *proto.TracingTracingComplete) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Tracing == nil {
		return
	}
	s.Tracing.DataLossOccurred = e != nil && e.DataLossOccurred
	select {
	case <-s.Tracing.Done:
	default:
		close(s.Tracing.Done)
	}
}

func (s *browserSession) pushConsoleLocked(messageType, text string, args []string, location *browserConsoleLocation) {
	s.ConsoleMessageSeq++
	s.ConsoleMessages = append(s.ConsoleMessages, browserConsoleMessage{
		Type:      messageType,
		Text:      text,
		Args:      args,
		Seq:       s.ConsoleMessageSeq,
		Timestamp: time.Now().UTC(),
		Location:  location,
	})
	if len(s.ConsoleMessages) > browserMaxConsoleEntries {
		s.ConsoleMessages = s.ConsoleMessages[len(s.ConsoleMessages)-browserMaxConsoleEntries:]
	}
}

func (s *browserSession) rebuildNetworkMapLocked() {
	s.NetworkRequestMap = map[proto.NetworkRequestID]*browserNetworkRequest{}
	for i := range s.NetworkRequests {
		s.NetworkRequestMap[proto.NetworkRequestID(s.NetworkRequests[i].RequestID)] = &s.NetworkRequests[i]
	}
}

func (s *browserSession) refreshSnapshotLocked(ctx context.Context, timeoutMs int) error {
	info, err := s.Page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Info()
	if err != nil {
		return err
	}
	s.URL = info.URL
	s.Title = info.Title
	s.touchLocked()
	return nil
}

func (s *browserSession) touchLocked() {
	now := time.Now().UTC()
	s.LastActivity = now
	s.UpdatedAt = now
}

func (s *browserSession) snapshotLocked() map[string]any {
	return map[string]any{"url": s.URL, "title": s.Title}
}

func (s *browserSession) isExpired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.TtlMs <= 0 {
		return false
	}
	return now.Sub(s.LastActivity) > time.Duration(s.TtlMs)*time.Millisecond
}

func (s *browserSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventCancel != nil {
		s.eventCancel()
		s.eventCancel = nil
	}
	if s.Tracing != nil && s.Tracing.Active {
		_ = proto.TracingEnd{}.Call(s.Browser)
		s.Tracing.Active = false
	}
	if s.Page != nil {
		_ = s.Page.Close()
	}
	err := error(nil)
	if s.Browser != nil {
		err = s.Browser.Close()
	}
	if s.Launcher != nil {
		if err != nil {
			s.Launcher.Kill()
		}
		go s.Launcher.Cleanup()
	}
}

func setPageDefaults(page *rod.Page) error {
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width: browserViewportWidth, Height: browserViewportHeight, DeviceScaleFactor: 1, Mobile: false}); err != nil {
		return err
	}
	return page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: browserUserAgent})
}

func navigateRodPage(ctx context.Context, page *rod.Page, rawURL string, timeoutMs int) error {
	if err := validateBrowserURL(rawURL); err != nil {
		return err
	}
	p := page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond)
	if err := p.Navigate(rawURL); err != nil {
		return err
	}
	return p.WaitLoad()
}

func browserElement(ctx context.Context, page *rod.Page, selector string, timeoutMs int) (*rod.Element, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, errors.New("selector is required")
	}
	return page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Element(selector)
}

func pageText(ctx context.Context, page *rod.Page, selector string, timeoutMs int) (string, error) {
	if strings.TrimSpace(selector) != "" {
		el, err := browserElement(ctx, page, selector, timeoutMs)
		if err != nil {
			return "", err
		}
		return el.Text()
	}
	obj, err := page.Context(ctx).Timeout(time.Duration(timeoutMs) * time.Millisecond).Eval(`() => document.body ? document.body.innerText : ""`)
	if err != nil {
		return "", err
	}
	if value, ok := remoteObjectValue(obj).(string); ok {
		return value, nil
	}
	return formatRemoteObject(obj), nil
}

func waitForSelectorState(ctx context.Context, page *rod.Page, selector, state string, timeoutMs int) error {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "" {
		state = "attached"
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		el, err := page.Context(ctx).Timeout(500 * time.Millisecond).Element(selector)
		if state == "hidden" {
			if err != nil {
				return nil
			}
			visible, _ := el.Visible()
			if !visible {
				return nil
			}
		} else if err == nil {
			if state == "visible" {
				visible, _ := el.Visible()
				if visible {
					return nil
				}
			} else {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for selector %q state %q timed out after %dms", selector, state, timeoutMs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func browserShortSettle(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func findChromePath() (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("PUPPETEER_EXECUTABLE_PATH")); envPath != "" && existsFile(envPath) {
		return envPath, nil
	}

	cacheDir := strings.TrimSpace(os.Getenv("PUPPETEER_CACHE_DIR"))
	if cacheDir == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cacheDir = filepath.Join(home, ".cache", "puppeteer")
		}
	}
	if cacheDir != "" {
		if puppeteerPath := scanPuppeteerCache(cacheDir); puppeteerPath != "" {
			return puppeteerPath, nil
		}
	}

	for _, candidate := range systemChromePaths() {
		if existsFile(candidate) {
			return candidate, nil
		}
	}

	if runtime.GOOS == "linux" {
		if flatpakPath := scanFlatpakChrome(); flatpakPath != "" {
			return flatpakPath, nil
		}
	}

	return "", errors.New("Could not find Chrome/Chromium. Please install Chrome or run: bunx puppeteer browsers install chrome")
}

func scanPuppeteerCache(cacheDir string) string {
	chromeDir := filepath.Join(cacheDir, "chrome")
	entries, err := os.ReadDir(chromeDir)
	if err != nil {
		return ""
	}
	platforms := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			platforms = append(platforms, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(platforms)))
	for _, platform := range platforms {
		platformDir := filepath.Join(chromeDir, platform)
		buildEntries, err := os.ReadDir(platformDir)
		if err != nil {
			continue
		}
		for _, entry := range buildEntries {
			if !entry.IsDir() {
				continue
			}
			root := filepath.Join(platformDir, entry.Name())
			candidates := []string{
				filepath.Join(root, "chrome.exe"),
				filepath.Join(root, "chrome"),
				filepath.Join(root, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing"),
			}
			for _, candidate := range candidates {
				if existsFile(candidate) {
					return candidate
				}
			}
		}
	}
	return ""
}

func scanFlatpakChrome() string {
	xdgDataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if xdgDataHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			xdgDataHome = filepath.Join(home, ".local", "share")
		}
	}
	apps := []struct {
		id     string
		binary string
	}{
		{id: "com.google.Chrome", binary: filepath.Join("extra", "chrome")},
		{id: "org.chromium.Chromium", binary: filepath.Join("extra", "chromium")},
	}
	roots := []string{filepath.Join(xdgDataHome, "flatpak", "app"), filepath.Join(string(os.PathSeparator), "var", "lib", "flatpak", "app")}
	for _, root := range roots {
		for _, app := range apps {
			candidate := filepath.Join(root, app.id, "current", "active", "files", app.binary)
			if existsFile(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func systemChromePaths() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			filepath.Join(envOr("PROGRAMFILES", `C:\Program Files`), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(envOr("PROGRAMFILES(X86)", `C:\Program Files (x86)`), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
		}
	}
}

func buildRodLauncher(ctx context.Context, chromePath string, headless bool) *launcher.Launcher {
	l := launcher.New().Context(ctx).Bin(chromePath).Headless(headless).NoSandbox(true)
	l.Set(flags.Flag("disable-setuid-sandbox"))
	l.Set(flags.Flag("disable-dev-shm-usage"))
	l.Set(flags.Flag("disable-extensions"))
	if headless {
		l.Set(flags.Flag("disable-gpu"))
	}
	return l
}

func validateBrowserURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if parsed.Scheme == "data" {
		return nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("Browser only supports http, https, and data URLs")
	}
	if parsed.Host == "" {
		return errors.New("url host is required")
	}
	return nil
}

func ensureHeadedBrowserAllowed(headless bool) error {
	if headless || runtime.GOOS == "windows" {
		return nil
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New("Cannot launch headed browser: no DISPLAY or WAYLAND_DISPLAY environment variable found. Headed mode requires a graphical environment (X11 or Wayland). Use headless mode on servers.")
	}
	return nil
}

func browserKeys(key string) ([]input.Key, error) {
	trimmed := strings.TrimSpace(key)
	switch strings.ToLower(trimmed) {
	case "enter", "return":
		return []input.Key{input.Enter}, nil
	case "escape", "esc":
		return []input.Key{input.Escape}, nil
	case "tab":
		return []input.Key{input.Tab}, nil
	case "backspace":
		return []input.Key{input.Backspace}, nil
	case "delete", "del":
		return []input.Key{input.Delete}, nil
	case "arrowup", "up":
		return []input.Key{input.ArrowUp}, nil
	case "arrowdown", "down":
		return []input.Key{input.ArrowDown}, nil
	case "arrowleft", "left":
		return []input.Key{input.ArrowLeft}, nil
	case "arrowright", "right":
		return []input.Key{input.ArrowRight}, nil
	}
	if trimmed == "" {
		return nil, errors.New("key is required")
	}
	keys := make([]input.Key, 0, len([]rune(trimmed)))
	for _, r := range trimmed {
		if r > 127 {
			return nil, fmt.Errorf("unsupported key %q; use value for text input", trimmed)
		}
		keys = append(keys, input.Key(r))
	}
	return keys, nil
}

func normalizeBrowserExpression(expression string) string {
	trimmed := strings.TrimSpace(expression)
	if strings.HasPrefix(trimmed, "function") || strings.HasPrefix(trimmed, "async") || strings.Contains(trimmed, "=>") {
		return trimmed
	}
	return "() => (" + trimmed + ")"
}

func formatRemoteObject(obj *proto.RuntimeRemoteObject) string {
	value := remoteObjectValue(obj)
	data, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		return string(data)
	}
	return fmt.Sprint(value)
}

func remoteObjectValue(obj *proto.RuntimeRemoteObject) any {
	if obj == nil {
		return nil
	}
	if value := obj.Value.Val(); value != nil {
		return value
	}
	if obj.UnserializableValue != "" {
		return string(obj.UnserializableValue)
	}
	if obj.Description != "" {
		return obj.Description
	}
	return nil
}

func normalizeBrowserMaxLength(maxLength int) int {
	if maxLength <= 0 || maxLength > browserMaxOutputLength {
		return browserDefaultMaxLength
	}
	return maxLength
}

func normalizeBrowserTimeoutMs(timeoutMs int) int {
	if timeoutMs <= 0 {
		return browserDefaultTimeoutMs
	}
	if timeoutMs < 1000 {
		return 1000
	}
	if timeoutMs > 60_000 {
		return 60_000
	}
	return timeoutMs
}

func normalizeBrowserSessionTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return browserDefaultSessionTTL, nil
	}
	if ttl < browserMinSessionTTL || ttl > browserMaxSessionTTL {
		return 0, fmt.Errorf("Browser session TTL must be between %dms and %dms", browserMinSessionTTL/time.Millisecond, browserMaxSessionTTL/time.Millisecond)
	}
	return ttl, nil
}

func boolArg(args map[string]any, name string, fallback bool) bool {
	value, ok := args[name]
	if !ok {
		return fallback
	}
	return boolValue(value)
}

func coordinateValue(v any) (browserCoordinate, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return browserCoordinate{}, false
	}
	x, okX := numberValue(m["x"])
	y, okY := numberValue(m["y"])
	if !okX || !okY {
		return browserCoordinate{}, false
	}
	return browserCoordinate{X: x, Y: y}, true
}

func numberValue(v any) (float64, bool) {
	switch typed := v.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func truncateString(value string, maxLength int) string {
	maxLength = normalizeBrowserMaxLength(maxLength)
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "\n[truncated]"
}

func jsonTool(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func existsFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isFlatpakChromePath(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/flatpak/")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func ensureBrowserCleanupTimer() {
	browserSessions.Lock()
	defer browserSessions.Unlock()
	if browserSessions.cleanupTimer != nil {
		return
	}
	browserSessions.cleanupTimer = time.AfterFunc(browserCleanupInterval, browserCleanupTick)
}

func browserCleanupTick() {
	now := time.Now()
	toClose := []*browserSession{}
	browserSessions.Lock()
	for id, session := range browserSessions.items {
		if session.isExpired(now) {
			delete(browserSessions.items, id)
			toClose = append(toClose, session)
		}
	}
	if len(browserSessions.items) == 0 {
		browserSessions.cleanupTimer = nil
	} else {
		browserSessions.cleanupTimer = time.AfterFunc(browserCleanupInterval, browserCleanupTick)
	}
	browserSessions.Unlock()
	for _, session := range toClose {
		session.close()
	}
}

func protoHeadersToStrings(headers proto.NetworkHeaders) map[string]string {
	out := map[string]string{}
	for key, value := range headers {
		out[key] = fmt.Sprint(value.Val())
	}
	return out
}

func redactHeaders(headers map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range headers {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "cookie" || lower == "set-cookie" || lower == "proxy-authorization" || strings.Contains(lower, "api-key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			out[key] = "[redacted]"
		} else {
			out[key] = value
		}
	}
	return out
}

func redactBrowserURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	changed := false
	for key := range query {
		lower := strings.ToLower(key)
		if lower == "token" || lower == "access_token" || lower == "api_key" || lower == "key" || lower == "secret" || lower == "password" || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			query.Set(key, "[redacted]")
			changed = true
		}
	}
	if changed {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func redactPostData(postData string) string {
	if postData == "" {
		return ""
	}
	sensitive := regexp.MustCompile(`(?i)("?(?:password|token|access_token|api_key|secret|authorization)"?\s*[:=]\s*)"?[^"&\s,}]+"?`)
	return sensitive.ReplaceAllString(postData, `${1}"[redacted]"`)
}

func truncatePostData(postData string) string {
	if postData == "" {
		return ""
	}
	if len(postData) <= browserMaxPostDataLength {
		return postData
	}
	return postData[:browserMaxPostDataLength] + fmt.Sprintf("\n\n[Post data truncated at %d characters]", browserMaxPostDataLength)
}

func filterNetworkRequests(requests []browserNetworkRequest, filter string) []browserNetworkRequest {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return append([]browserNetworkRequest(nil), requests...)
	}
	out := []browserNetworkRequest{}
	for _, request := range requests {
		haystack := strings.ToLower(strings.Join([]string{request.URL, request.Method, request.ResourceType, request.StatusText, request.FailureText}, " "))
		if request.Status != nil {
			haystack += fmt.Sprintf(" %d", *request.Status)
		}
		if strings.Contains(haystack, filter) {
			out = append(out, request)
		}
	}
	return out
}

func formatNetworkRequests(requests []browserNetworkRequest, includeDetails bool) string {
	lines := []string{}
	for _, request := range requests {
		status := "PENDING"
		if request.Failed {
			status = strings.TrimSpace("FAILED " + request.FailureText)
		} else if request.Status != nil {
			status = strings.TrimSpace(fmt.Sprintf("%d %s", *request.Status, request.StatusText))
		}
		duration := ""
		if request.DurationMs != nil {
			duration = fmt.Sprintf(" %dms", *request.DurationMs)
		}
		line := fmt.Sprintf("[%s] %s %s %s%s %s", request.StartedAt.Format(time.RFC3339), request.Method, status, request.ResourceType, duration, request.URL)
		if includeDetails {
			parts := []string{line, "  requestHeaders: " + compactJSON(request.RequestHeaders)}
			if request.PostData != "" {
				parts = append(parts, "  postData: "+request.PostData)
			}
			if request.ResponseHeaders != nil {
				parts = append(parts, "  responseHeaders: "+compactJSON(request.ResponseHeaders))
			}
			line = strings.Join(parts, "\n")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatConsoleMessages(messages []browserConsoleMessage) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		location := ""
		if message.Location != nil && message.Location.URL != "" {
			location = fmt.Sprintf(" (%s:%d:%d)", message.Location.URL, message.Location.LineNumber, message.Location.ColumnNumber)
		}
		lines = append(lines, fmt.Sprintf("[%s] %s%s %s", message.Timestamp.Format(time.RFC3339), strings.ToUpper(message.Type), location, message.Text))
	}
	return strings.Join(lines, "\n")
}

func consoleLocationFromStack(stack *proto.RuntimeStackTrace) *browserConsoleLocation {
	if stack == nil || len(stack.CallFrames) == 0 {
		return nil
	}
	frame := stack.CallFrames[0]
	return &browserConsoleLocation{URL: frame.URL, LineNumber: frame.LineNumber, ColumnNumber: frame.ColumnNumber}
}

func cleanBrowserHTML(html string) string {
	cleaned := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(html, "")
	cleaned = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`).ReplaceAllString(cleaned, "")
	cleaned = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

func gsonMapToAny(input map[string]gson.JSON) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value.Val()
	}
	return out
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}
