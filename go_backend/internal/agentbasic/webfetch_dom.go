package agentbasic

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// WebFetch "dom" mode — extract and clean rendered HTML via the no-browser
// HTTP-fetch path. This mirrors the TypeScript fetchDom fallback (server/lib/
// web-fetch/dom.ts) that runs when Chrome/Puppeteer is unavailable: HTTP fetch
// the page, optionally apply a CSS selector, then clean the HTML by discarding
// dangerous/noisy tags (and their children), stripping non-essential attributes,
// and collapsing whitespace.
//
// Honest scope: this implements the HTTP-fetch fallback only (no JS rendering).
// The CSS selector support is a common subset (tag, .class, #id, tag.class).

const webFetchDomDefaultMaxLength = 20000

// discardDomTags are removed together with their entire subtree, matching the
// TypeScript DISCARD_TAGS list.
var discardDomTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"svg":      true,
	"iframe":   true,
	"link":     true,
	"meta":     true,
	"head":     true,
}

// keepDomAttrs are the attributes preserved on surviving elements, matching the
// TypeScript KEEP_ATTRS list (aria-* handled via prefix check).
var keepDomAttrs = map[string]bool{
	"href": true, "src": true, "alt": true, "title": true,
	"class": true, "id": true, "role": true,
	"type": true, "name": true, "value": true, "placeholder": true,
	"for": true, "action": true, "method": true, "disabled": true,
	"checked": true, "selected": true, "readonly": true, "required": true,
}

var (
	domWhitespaceRunRE = regexp.MustCompile(`[ \t]+`)
	domBlankLinesRE    = regexp.MustCompile(`\n{3,}`)
)

// fetchDomHTTP fetches a URL over HTTP and returns cleaned DOM-mode output.
func fetchDomHTTP(ctx context.Context, rawURL, selector string, maxLength int) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "url is required"
	}
	parsed, _, errMessage := validateWebFetchURL(rawURL)
	if errMessage != "" {
		return errMessage
	}
	if maxLength <= 0 || maxLength > 200000 {
		maxLength = webFetchDomDefaultMaxLength
	}

	var rawHTML string
	switch parsed.Scheme {
	case "data":
		// Mirror the TS fetchDom -> httpFetchHtml path which handles data: URLs.
		decoded, derr := decodeDataURLBytes(rawURL)
		if derr != "" {
			return derr
		}
		rawHTML = decoded
	case "http", "https":
		if parsed.Host == "" {
			return "Invalid URL: " + rawURL
		}
		fetched, fetchErr := webFetchRawHTML(ctx, parsed.String())
		if fetchErr != "" {
			return fetchErr
		}
		rawHTML = fetched
	default:
		return "Unsupported protocol: " + parsed.Scheme + ": — only http://, https://, and data: URLs are supported. Use the Read tool for local files."
	}

	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "failed to parse HTML: " + err.Error()
	}

	var roots []*html.Node
	selector = strings.TrimSpace(selector)
	if selector != "" {
		matcher, ok := parseSimpleSelector(selector)
		if !ok {
			return "unsupported CSS selector for dom mode (supported: tag, .class, #id, tag.class): " + selector
		}
		roots = matchNodes(doc, matcher)
		if len(roots) == 0 {
			return "No elements found matching selector: " + selector
		}
	} else {
		if body := findFirstElement(doc, atom.Body); body != nil {
			roots = []*html.Node{body}
		} else {
			roots = []*html.Node{doc}
		}
	}

	var sb strings.Builder
	for i, node := range roots {
		if i > 0 {
			sb.WriteString("\n")
		}
		renderCleanNode(&sb, node, selector != "")
	}
	cleaned := collapseDomWhitespace(sb.String())

	// Mirror the TS fetchDom truncation (server/lib/web-fetch/dom.ts:121):
	// `cleaned.length > maxLength` uses the JS string length (UTF-16 code units)
	// and `cleaned.slice(0, maxLength)` cuts at a UTF-16 boundary. Go previously
	// used len(cleaned) (BYTES) and cleaned[:maxLength] (byte slice), so CJK/emoji
	// pages truncated at a far smaller character count than TS and could split a
	// multi-byte rune mid-sequence (producing invalid UTF-8 in the model context).
	if units := utf16.Encode([]rune(cleaned)); len(units) > maxLength {
		head := string(utf16.Decode(units[:maxLength]))
		return fmt.Sprintf("%s\n\n[DOM truncated at %d characters]", head, maxLength)
	}
	return cleaned
}

// webFetchRawHTML performs the HTTP GET and returns the body, or a non-empty
// error string on failure.
func webFetchRawHTML(ctx context.Context, target string) (string, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err.Error()
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	res, err := webFetchHTTPClient(ctx).Do(req)
	if err != nil {
		return "", err.Error()
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return "", fmt.Sprintf("HTTP %d: %s", res.StatusCode, http.StatusText(res.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 5*1024*1024))
	if err != nil {
		return "", err.Error()
	}
	return string(data), ""
}

// simpleSelector matches the common CSS selector subset: an optional tag name
// plus an optional .class or #id qualifier.
type simpleSelector struct {
	tag   string // lowercase tag name, "" means any
	class string // single class to match, "" means none
	id    string // id to match, "" means none
}

func parseSimpleSelector(sel string) (simpleSelector, bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return simpleSelector{}, false
	}
	// Reject combinators/compound selectors we do not support.
	if strings.ContainsAny(sel, " >+~,[]:()*") {
		return simpleSelector{}, false
	}
	var s simpleSelector
	switch {
	case strings.HasPrefix(sel, "#"):
		s.id = sel[1:]
	case strings.HasPrefix(sel, "."):
		s.class = sel[1:]
	default:
		// tag, optionally followed by .class or #id
		if idx := strings.IndexAny(sel, ".#"); idx >= 0 {
			s.tag = strings.ToLower(sel[:idx])
			rest := sel[idx:]
			if strings.HasPrefix(rest, ".") {
				s.class = rest[1:]
			} else {
				s.id = rest[1:]
			}
		} else {
			s.tag = strings.ToLower(sel)
		}
	}
	if s.tag == "" && s.class == "" && s.id == "" {
		return simpleSelector{}, false
	}
	if s.class != "" && strings.ContainsAny(s.class, ".#") {
		return simpleSelector{}, false
	}
	if s.id != "" && strings.ContainsAny(s.id, ".#") {
		return simpleSelector{}, false
	}
	return s, true
}

func (s simpleSelector) matches(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if s.tag != "" && !strings.EqualFold(n.Data, s.tag) {
		return false
	}
	if s.id != "" && getAttr(n, "id") != s.id {
		return false
	}
	if s.class != "" {
		if !hasClass(n, s.class) {
			return false
		}
	}
	return true
}

func matchNodes(root *html.Node, sel simpleSelector) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if sel.matches(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func findFirstElement(root *html.Node, a atom.Atom) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == a {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for _, field := range strings.Fields(getAttr(n, "class")) {
		if field == class {
			return true
		}
	}
	return false
}

// renderCleanNode writes a cleaned representation of n into sb. When n is itself
// a selected element (includeSelf), it is rendered including its own tag.
func renderCleanNode(sb *strings.Builder, n *html.Node, includeSelf bool) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)
		return
	case html.ElementNode:
		if discardDomTags[strings.ToLower(n.Data)] {
			return
		}
		if includeSelf {
			writeOpenTag(sb, n)
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				renderCleanNode(sb, c, true)
			}
			fmt.Fprintf(sb, "</%s>", strings.ToLower(n.Data))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderCleanNode(sb, c, true)
		}
		return
	default:
		// Document/comment/doctype: recurse into children only.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			renderCleanNode(sb, c, includeSelf)
		}
	}
}

func writeOpenTag(sb *strings.Builder, n *html.Node) {
	tag := strings.ToLower(n.Data)
	sb.WriteString("<")
	sb.WriteString(tag)
	for _, attr := range n.Attr {
		key := strings.ToLower(attr.Key)
		if keepDomAttrs[key] || strings.HasPrefix(key, "aria-") {
			if attr.Val == "" {
				fmt.Fprintf(sb, " %s", key)
			} else {
				fmt.Fprintf(sb, " %s=%q", key, attr.Val)
			}
		}
	}
	sb.WriteString(">")
}

func collapseDomWhitespace(s string) string {
	s = domWhitespaceRunRE.ReplaceAllString(s, " ")
	s = domBlankLinesRE.ReplaceAllString(s, "\n\n")
	// Trim trailing spaces on each line to avoid noisy output.
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// decodeDataURLBytes decodes a data: URL into its raw payload string, mirroring
// the TS httpFetchHtml data: handling that the dom fallback relies on. Returns a
// non-empty error string on failure.
func decodeDataURLBytes(rawURL string) (string, string) {
	payload := strings.TrimPrefix(rawURL, "data:")
	header, dataPart, ok := strings.Cut(payload, ",")
	if !ok {
		return "", "invalid data URL"
	}
	_, base64Encoded := parseDataURLHeader(header)
	if base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataPart))
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(dataPart))
			if err != nil {
				return "", "invalid data URL: " + err.Error()
			}
		}
		return string(decoded), ""
	}
	decoded, err := url.PathUnescape(dataPart)
	if err != nil {
		return "", "invalid data URL: " + err.Error()
	}
	return decoded, ""
}
