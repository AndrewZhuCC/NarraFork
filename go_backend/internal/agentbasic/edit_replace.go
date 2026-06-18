package agentbasic

import (
	"regexp"
	"strings"
)

// whitespaceRun matches one or more whitespace characters, mirroring the TS
// edit WhitespaceNormalizedReplacer's /\s+/g collapse.
var whitespaceRun = regexp.MustCompile(`\s+`)

// This file ports the TypeScript edit tool's cascading replacer chain
// (server/lib/agent/tools/edit.ts), which is sourced from opencode's approach.
// Go previously did exact substring match only (strings.Count/strings.Replace),
// so an old_string with slightly different indentation/whitespace/line-endings
// than the file — extremely common when a model reproduces code — failed with
// "old_string not found". This brings the Go edit tool to parity: an ordered set
// of replacers each yields candidate matched substrings; findEditReplaceMatch
// selects the first replacer that produces a (unique, unless replaceAll) match.

// editReplacer yields candidate matched substrings of content for a find string.
type editReplacer func(content, find string) []string

// editSimpleReplacer (1): exact match — yield the search string itself.
func editSimpleReplacer(_ string, find string) []string {
	return []string{find}
}

// editLineTrimmedReplacer (2): compare lines after trimming, yield original text.
func editLineTrimmedReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if len(searchLines) > 0 && searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) == 0 {
		return nil
	}
	out := []string{}
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true
		for j := 0; j < len(searchLines); j++ {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		start := 0
		for k := 0; k < i; k++ {
			start += len(originalLines[k]) + 1
		}
		end := start
		for k := 0; k < len(searchLines); k++ {
			end += len(originalLines[i+k])
			if k < len(searchLines)-1 {
				end++
			}
		}
		out = append(out, content[start:end])
	}
	return out
}

// editWhitespaceNormalizedReplacer (4): collapse whitespace before comparing.
func editWhitespaceNormalizedReplacer(content, find string) []string {
	normalize := func(t string) string {
		return strings.TrimSpace(whitespaceRun.ReplaceAllString(t, " "))
	}
	normalizedFind := normalize(find)
	lines := strings.Split(content, "\n")
	out := []string{}
	for _, line := range lines {
		if normalize(line) == normalizedFind {
			out = append(out, line)
		}
	}
	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalize(block) == normalizedFind {
				out = append(out, block)
			}
		}
	}
	return out
}

// editIndentationFlexibleReplacer (5): strip common indent before comparing.
func editIndentationFlexibleReplacer(content, find string) []string {
	removeIndentation := func(text string) string {
		lines := strings.Split(text, "\n")
		minIndent := -1
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			indent := len(l) - len(strings.TrimLeft(l, " \t"))
			if minIndent == -1 || indent < minIndent {
				minIndent = indent
			}
		}
		if minIndent <= 0 {
			return text
		}
		out := make([]string, len(lines))
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				out[i] = l
			} else if len(l) >= minIndent {
				out[i] = l[minIndent:]
			} else {
				out[i] = l
			}
		}
		return strings.Join(out, "\n")
	}
	normalizedFind := removeIndentation(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	out := []string{}
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeIndentation(block) == normalizedFind {
			out = append(out, block)
		}
	}
	return out
}

// editTrimmedBoundaryReplacer (7): trim the entire find string.
func editTrimmedBoundaryReplacer(content, find string) []string {
	trimmed := strings.TrimSpace(find)
	if trimmed == find {
		return nil // already trimmed, nothing new
	}
	out := []string{}
	if strings.Contains(content, trimmed) {
		out = append(out, trimmed)
	}
	lines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmed {
			out = append(out, block)
		}
	}
	return out
}

// editMultiOccurrenceReplacer (9): yield every exact match position.
func editMultiOccurrenceReplacer(content, find string) []string {
	if find == "" {
		return nil
	}
	out := []string{}
	idx := 0
	for {
		pos := strings.Index(content[idx:], find)
		if pos == -1 {
			break
		}
		out = append(out, find)
		idx += pos + len(find)
	}
	return out
}

// editLevenshtein computes the edit distance between two strings, mirroring the
// TS levenshtein helper used by BlockAnchorReplacer.
func editLevenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 || len(br) == 0 {
		if len(ar) > len(br) {
			return len(ar)
		}
		return len(br)
	}
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := 0; j <= len(br); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			cur[j] = m
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

const (
	editSingleCandidateSimilarityThreshold    = 0.0
	editMultipleCandidatesSimilarityThreshold = 0.3
)

// editBlockAnchorReplacer (3): match first/last lines, score middle via Levenshtein.
func editBlockAnchorReplacer(content, find string) []string {
	originalLines := strings.Split(content, "\n")
	searchLines := strings.Split(find, "\n")
	if len(searchLines) < 3 {
		return nil
	}
	if searchLines[len(searchLines)-1] == "" {
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) < 3 {
		return nil
	}
	firstTrimmed := strings.TrimSpace(searchLines[0])
	lastTrimmed := strings.TrimSpace(searchLines[len(searchLines)-1])
	searchBlockSize := len(searchLines)

	type candidate struct{ startLine, endLine int }
	candidates := []candidate{}
	for i := 0; i < len(originalLines); i++ {
		if strings.TrimSpace(originalLines[i]) != firstTrimmed {
			continue
		}
		for j := i + 2; j < len(originalLines); j++ {
			if strings.TrimSpace(originalLines[j]) == lastTrimmed {
				candidates = append(candidates, candidate{startLine: i, endLine: j})
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	calcSimilarity := func(startLine, endLine int) float64 {
		actualBlockSize := endLine - startLine + 1
		linesToCheck := searchBlockSize - 2
		if actualBlockSize-2 < linesToCheck {
			linesToCheck = actualBlockSize - 2
		}
		if linesToCheck <= 0 {
			return 1.0
		}
		similarity := 0.0
		for j := 1; j < searchBlockSize-1 && j < actualBlockSize-1; j++ {
			origLine := strings.TrimSpace(originalLines[startLine+j])
			searchLine := strings.TrimSpace(searchLines[j])
			maxLen := len([]rune(origLine))
			if l := len([]rune(searchLine)); l > maxLen {
				maxLen = l
			}
			if maxLen == 0 {
				continue
			}
			distance := editLevenshtein(origLine, searchLine)
			similarity += (1 - float64(distance)/float64(maxLen)) / float64(linesToCheck)
		}
		return similarity
	}

	extractSubstring := func(startLine, endLine int) string {
		start := 0
		for k := 0; k < startLine; k++ {
			start += len(originalLines[k]) + 1
		}
		end := start
		for k := startLine; k <= endLine; k++ {
			end += len(originalLines[k])
			if k < endLine {
				end++
			}
		}
		return content[start:end]
	}

	if len(candidates) == 1 {
		c := candidates[0]
		if calcSimilarity(c.startLine, c.endLine) >= editSingleCandidateSimilarityThreshold {
			return []string{extractSubstring(c.startLine, c.endLine)}
		}
		return nil
	}
	var best *candidate
	maxSim := -1.0
	for i := range candidates {
		sim := calcSimilarity(candidates[i].startLine, candidates[i].endLine)
		if sim > maxSim {
			maxSim = sim
			best = &candidates[i]
		}
	}
	if maxSim >= editMultipleCandidatesSimilarityThreshold && best != nil {
		return []string{extractSubstring(best.startLine, best.endLine)}
	}
	return nil
}

var editEscapePattern = regexp.MustCompile(`\\(n|t|r|'|"|` + "`" + `|\\|\n|\$)`)

// editUnescape mirrors the TS EscapeNormalizedReplacer unescapeStr.
func editUnescape(str string) string {
	return editEscapePattern.ReplaceAllStringFunc(str, func(match string) string {
		// match is the full 2-char (or "\<newline>") escape; the captured char is match[1:].
		ch := match[len(match)-1:]
		switch ch {
		case "n":
			return "\n"
		case "t":
			return "\t"
		case "r":
			return "\r"
		case "'":
			return "'"
		case `"`:
			return `"`
		case "`":
			return "`"
		case `\`:
			return `\`
		case "\n":
			return "\n"
		case "$":
			return "$"
		default:
			return match
		}
	})
}

// editEscapeNormalizedReplacer (6): handle common escape sequences.
func editEscapeNormalizedReplacer(content, find string) []string {
	unescapedFind := editUnescape(find)
	out := []string{}
	if strings.Contains(content, unescapedFind) {
		out = append(out, unescapedFind)
	}
	lines := strings.Split(content, "\n")
	findLines := strings.Split(unescapedFind, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if editUnescape(block) == unescapedFind {
			out = append(out, block)
		}
	}
	return out
}

// editContextAwareReplacer (8): anchor on first/last lines, require 50% middle match.
func editContextAwareReplacer(content, find string) []string {
	findLines := strings.Split(find, "\n")
	if len(findLines) < 3 {
		return nil
	}
	if findLines[len(findLines)-1] == "" {
		findLines = findLines[:len(findLines)-1]
	}
	if len(findLines) < 3 {
		return nil
	}
	contentLines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(findLines[0])
	lastLine := strings.TrimSpace(findLines[len(findLines)-1])
	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != firstLine {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) == lastLine {
				blockLines := contentLines[i : j+1]
				if len(blockLines) == len(findLines) {
					matching := 0
					total := 0
					for k := 1; k < len(blockLines)-1; k++ {
						bl := strings.TrimSpace(blockLines[k])
						fl := strings.TrimSpace(findLines[k])
						if len(bl) > 0 || len(fl) > 0 {
							total++
							if bl == fl {
								matching++
							}
						}
					}
					if total == 0 || float64(matching)/float64(total) >= 0.5 {
						return []string{strings.Join(blockLines, "\n")}
					}
				}
				break
			}
		}
	}
	return nil
}

// editReplacers is the ordered chain mirroring the TS REPLACERS array exactly
// (server/lib/agent/tools/edit.ts): Simple → LineTrimmed → BlockAnchor →
// WhitespaceNormalized → IndentationFlexible → EscapeNormalized → TrimmedBoundary
// → ContextAware → MultiOccurrence. Go previously did exact substring match only;
// this cascade ENABLES the same fuzzy matches TS makes (indentation/whitespace/
// escape/anchor variations) while never changing an exact match that already
// succeeded (SimpleReplacer is always tried first).
var editReplacers = []editReplacer{
	editSimpleReplacer,
	editLineTrimmedReplacer,
	editBlockAnchorReplacer,
	editWhitespaceNormalizedReplacer,
	editIndentationFlexibleReplacer,
	editEscapeNormalizedReplacer,
	editTrimmedBoundaryReplacer,
	editContextAwareReplacer,
	editMultiOccurrenceReplacer,
}

type editReplaceMatch struct {
	search string
	index  int
}

// findEditReplaceMatch mirrors the TS findReplaceMatch: try each replacer in
// order; for each yielded candidate that exists in content, accept it (when
// replaceAll) or require it to be unique (single non-overlapping occurrence).
// Returns (match, found, multiple): found=false means no replacer matched;
// found=true && !ok means matches existed but none was unique.
func findEditReplaceMatch(content, oldString string, replaceAll bool) (editReplaceMatch, bool, bool) {
	notFound := true
	for _, replacer := range editReplacers {
		for _, search := range replacer(content, oldString) {
			index := strings.Index(content, search)
			if index == -1 {
				continue
			}
			notFound = false
			if replaceAll {
				return editReplaceMatch{search: search, index: index}, true, true
			}
			if index != strings.LastIndex(content, search) {
				continue // not unique via this replacer, try next
			}
			return editReplaceMatch{search: search, index: index}, true, true
		}
	}
	if notFound {
		return editReplaceMatch{}, false, false
	}
	return editReplaceMatch{}, true, false
}
