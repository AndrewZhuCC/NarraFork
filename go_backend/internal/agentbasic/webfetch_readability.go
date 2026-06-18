package agentbasic

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf16"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// WebFetch "readability" mode — heuristic main-content extraction via the
// no-browser HTTP-fetch path. This mirrors the TS fetchReadability default mode
// (server/lib/web-fetch/readability.ts) SEMANTICALLY: it returns clean article
// text plus the page title/excerpt, stripping navigation/footer/ad boilerplate.
//
// Honest scope: TS uses Mozilla Readability (a full content-scoring algorithm).
// Go implements a lightweight heuristic (prefer <article>/<main>, else the
// highest text-density block, then strip boilerplate and extract text). Output
// is NOT byte-identical to Mozilla Readability; this is recorded as a capability
// difference (readabilityMode=heuristic). No JS rendering (HTTP fetch only).

const webFetchReadabilityDefaultMaxLength = 20000

// readabilityBoilerplateTags are stripped from the extracted content (in
// addition to discardDomTags) because they rarely contain article body text.
var readabilityBoilerplateTags = map[string]bool{
	"nav":        true,
	"footer":     true,
	"aside":      true,
	"header":     true,
	"form":       true,
	"button":     true,
	"figure":     true,
	"figcaption": true,
}

// readabilityBoilerplateHints are substrings in an element's class/id that mark
// it as likely boilerplate (navigation, ads, comments, sidebars).
var readabilityBoilerplateHints = []string{
	"nav", "menu", "sidebar", "footer", "header", "comment",
	"advert", "promo", "social", "share", "related", "breadcrumb", "banner",
}

// fetchReadabilityHTTP fetches a URL over HTTP (or decodes a data: URL) and
// returns heuristic readability-mode output, or a non-empty error string.
func fetchReadabilityHTTP(ctx context.Context, rawURL string, maxLength int) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "url is required"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid URL"
	}
	if maxLength <= 0 || maxLength > 200000 {
		maxLength = webFetchReadabilityDefaultMaxLength
	}

	var rawHTML string
	switch parsed.Scheme {
	case "data":
		decoded, derr := decodeDataURLBytes(rawURL)
		if derr != "" {
			return derr
		}
		rawHTML = decoded
	case "http", "https":
		if parsed.Host == "" {
			return "invalid URL"
		}
		fetched, fetchErr := webFetchRawHTML(ctx, parsed.String())
		if fetchErr != "" {
			return fetchErr
		}
		rawHTML = fetched
	default:
		return "only http, https, and data URLs are supported"
	}

	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "failed to parse HTML: " + err.Error()
	}

	title := readabilityTitle(doc)
	excerpt := readabilityExcerpt(doc)
	content := readabilityContent(doc)

	if strings.TrimSpace(content) == "" {
		// Mirror the TS fetchReadability "no article" fallback message.
		return "Failed to extract article content — page may not have readable text."
	}

	var sb strings.Builder
	if title != "" {
		sb.WriteString("# ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}
	if excerpt != "" {
		sb.WriteString("> ")
		sb.WriteString(excerpt)
		sb.WriteString("\n\n")
	}
	sb.WriteString(content)
	out := sb.String()

	// Mirror TS readability truncation: JS string length (UTF-16 code units) +
	// slice at a UTF-16 boundary so CJK/emoji are not split mid-rune.
	if units := utf16.Encode([]rune(out)); len(units) > maxLength {
		head := string(utf16.Decode(units[:maxLength]))
		return fmt.Sprintf("%s\n\n[Content truncated at %d characters]", head, maxLength)
	}
	return out
}

func readabilityTitle(doc *html.Node) string {
	node := findFirstElement(doc, atom.Title)
	if node == nil {
		return ""
	}
	return strings.TrimSpace(collapseInlineWhitespace(nodeText(node)))
}

func readabilityExcerpt(doc *html.Node) string {
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.DataAtom == atom.Meta {
			name := strings.ToLower(strings.TrimSpace(getAttr(n, "name")))
			prop := strings.ToLower(strings.TrimSpace(getAttr(n, "property")))
			if name == "description" || prop == "og:description" {
				found = strings.TrimSpace(getAttr(n, "content"))
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return collapseInlineWhitespace(found)
}

// readabilityContent picks the dominant content container and extracts its text.
func readabilityContent(doc *html.Node) string {
	root := selectReadabilityRoot(doc)
	if root == nil {
		return ""
	}
	var sb strings.Builder
	extractReadableText(&sb, root)
	return collapseDomWhitespace(sb.String())
}

// selectReadabilityRoot prefers a semantic <article> or <main>, otherwise the
// block element with the highest text-density score, otherwise <body>.
func selectReadabilityRoot(doc *html.Node) *html.Node {
	if article := findFirstElement(doc, atom.Article); article != nil {
		return article
	}
	if main := findFirstElement(doc, atom.Main); main != nil {
		return main
	}
	body := findFirstElement(doc, atom.Body)
	if body == nil {
		body = doc
	}
	best := body
	bestScore := readabilityScore(body)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && isBlockCandidate(n) {
			if score := readabilityScore(n); score > bestScore {
				bestScore = score
				best = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(body)
	return best
}

// isBlockCandidate reports whether n is a block element worth scoring as a
// content container.
func isBlockCandidate(n *html.Node) bool {
	switch n.DataAtom {
	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Td, atom.Li:
		return true
	default:
		return false
	}
}

// readabilityScore approximates text density: total descendant text length minus
// link-text length, penalized when the element looks like boilerplate.
func readabilityScore(n *html.Node) int {
	if isBoilerplate(n) {
		return 0
	}
	total := len(strings.TrimSpace(nodeText(n)))
	linkLen := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.DataAtom == atom.A {
			linkLen += len(strings.TrimSpace(nodeText(node)))
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	score := total - linkLen
	if score < 0 {
		score = 0
	}
	return score
}

// isBoilerplate reports whether an element is a navigation/footer/ad-style
// container by tag or by class/id hints.
func isBoilerplate(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if readabilityBoilerplateTags[strings.ToLower(n.Data)] {
		return true
	}
	hint := strings.ToLower(getAttr(n, "class") + " " + getAttr(n, "id") + " " + getAttr(n, "role"))
	if strings.TrimSpace(hint) == "" {
		return false
	}
	for _, marker := range readabilityBoilerplateHints {
		if strings.Contains(hint, marker) {
			return true
		}
	}
	return false
}

// extractReadableText writes the text content of n into sb, skipping discard and
// boilerplate subtrees and inserting newlines between block elements.
func extractReadableText(sb *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)
		return
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		if discardDomTags[tag] || readabilityBoilerplateTags[tag] || isBoilerplate(n) {
			return
		}
		block := isReadableBlockTag(n.DataAtom)
		if block {
			sb.WriteString("\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractReadableText(sb, c)
		}
		if block {
			sb.WriteString("\n")
		}
		return
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extractReadableText(sb, c)
		}
	}
}

func isReadableBlockTag(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Section, atom.Article, atom.H1, atom.H2, atom.H3,
		atom.H4, atom.H5, atom.H6, atom.Li, atom.Ul, atom.Ol, atom.Br,
		atom.Blockquote, atom.Pre, atom.Tr, atom.Table:
		return true
	default:
		return false
	}
}

// nodeText returns the concatenated text of all descendant text nodes.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// collapseInlineWhitespace collapses all runs of whitespace (including newlines)
// into single spaces and trims; used for title/excerpt single-line fields.
func collapseInlineWhitespace(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
