package fs

import "strings"

// matchStrategy identifies which pass of resolveOldString located the text the
// model meant to replace. It exists so the success summary and debug log can
// distinguish a byte-exact edit from one rescued by the whitespace-tolerant
// fallback.
type matchStrategy int

const (
	matchExact       matchStrategy = iota // strings.Contains hit
	matchCurly                            // curly↔straight quote normalization hit
	matchLineTrimmed                      // whitespace/indentation-tolerant line match
)

func (s matchStrategy) String() string {
	switch s {
	case matchExact:
		return "exact"
	case matchCurly:
		return "curly-normalized"
	case matchLineTrimmed:
		return "whitespace-tolerant"
	default:
		return "unknown"
	}
}

// matchResult is what resolveOldString hands back to the edit path.
//
// actualOld is always the file's VERBATIM substring (never the model's
// reconstruction), so the downstream count / substitution / diff path operates
// on real file bytes regardless of which strategy matched — the same contract
// findActualString already honors for curly-quote matches.
type matchResult struct {
	actualOld string
	strategy  matchStrategy
	found     bool
	ambiguous bool                // a fallback matched >1 region — reject, don't guess
	count     int                 // number of fallback regions when ambiguous
	reindent  func(string) string // re-indent new_string to the file's indentation; nil when N/A
}

// resolveOldString locates the file substring the model meant to replace,
// trying byte-exact and curly-quote normalization first (via findActualString)
// and a whitespace/indentation-tolerant line match only as a last resort.
//
// The fallback is deliberately conservative: it runs only after the exact
// passes miss, resolves only when it matches exactly one region (otherwise it
// reports ambiguity rather than guessing), and returns the file's verbatim
// bytes. It is a silent recovery net for indentation drift — not advertised in
// the tool description, so the model keeps aiming for byte-exact matches.
func resolveOldString(content, old string) matchResult {
	if a, ok := findActualString(content, old); ok {
		strat := matchExact
		if a != old {
			strat = matchCurly
		}
		return matchResult{actualOld: a, strategy: strat, found: true}
	}

	span, reindent, n := lineTrimmedMatch(content, old)
	switch {
	case n == 1:
		return matchResult{actualOld: span, strategy: matchLineTrimmed, found: true, reindent: reindent}
	case n > 1:
		return matchResult{strategy: matchLineTrimmed, ambiguous: true, count: n}
	default:
		return matchResult{strategy: matchExact} // not found by any strategy
	}
}

// lineByteSpan locates one logical line within the LF-normalized file content.
type lineByteSpan struct {
	start      int // byte offset of the line's first char
	contentEnd int // byte offset just past the line content (before its '\n')
	fullEnd    int // byte offset just past the line's '\n' (== contentEnd at EOF w/o newline)
}

// indexLines returns the byte spans of every logical line in content. content
// is assumed LF-normalized (the edit path normalizes line endings on read).
// A file ending in '\n' yields no phantom trailing empty line.
func indexLines(content string) []lineByteSpan {
	if content == "" {
		return nil
	}
	var spans []lineByteSpan
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			spans = append(spans, lineByteSpan{start: start, contentEnd: i, fullEnd: i + 1})
			start = i + 1
		}
	}
	if start < len(content) {
		spans = append(spans, lineByteSpan{start: start, contentEnd: len(content), fullEnd: len(content)})
	}
	return spans
}

// trimHoriz strips a line's leading and trailing spaces and tabs (not its
// content) so two lines compare equal when they differ only in indentation or
// trailing whitespace.
func trimHoriz(s string) string { return strings.Trim(s, " \t") }

// leadingWS returns the leading run of spaces/tabs in s.
func leadingWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// lineTrimmedMatch finds contiguous runs of file lines whose horizontally
// trimmed content equals the trimmed lines of old, returning the file's
// verbatim substring for a single match and the count of distinct matches.
//
//   - count == 1 → actualOld is the matched span; reindent (possibly nil)
//     re-indents new_string to the file's indentation.
//   - count >= 2 → ambiguous; caller rejects (actualOld empty).
//   - count == 0 → no match; caller falls back to the not-found hint.
//
// It refuses signatures with no content-bearing line (they anchor nothing and
// would match everywhere); single content lines are allowed but, like any
// signature, are subject to the uniqueness requirement.
func lineTrimmedMatch(content, old string) (actualOld string, reindent func(string) string, count int) {
	// A trailing "\n" in old is a terminator artifact, not a blank line to
	// match: drop one trailing empty element and remember to include the
	// matched file line's terminator in the returned span.
	oldLines := strings.Split(old, "\n")
	includeTerminator := false
	if len(oldLines) > 1 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
		includeTerminator = true
	}
	if len(oldLines) == 0 {
		return "", nil, 0
	}

	sig := make([]string, len(oldLines))
	nonEmpty := 0
	for i, l := range oldLines {
		sig[i] = trimHoriz(l)
		if sig[i] != "" {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		return "", nil, 0
	}

	fileLines := indexLines(content)
	if len(sig) > len(fileLines) {
		return "", nil, 0
	}

	var matches []int
	for i := 0; i+len(sig) <= len(fileLines); i++ {
		ok := true
		for k := range sig {
			sp := fileLines[i+k]
			if trimHoriz(content[sp.start:sp.contentEnd]) != sig[k] {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, i)
		}
	}
	if len(matches) != 1 {
		return "", nil, len(matches)
	}

	first := fileLines[matches[0]]
	last := fileLines[matches[0]+len(sig)-1]
	end := last.contentEnd
	if includeTerminator {
		end = last.fullEnd
	}
	actualOld = content[first.start:end]

	fileBlock := make([]string, len(sig))
	for k := range sig {
		sp := fileLines[matches[0]+k]
		fileBlock[k] = content[sp.start:sp.contentEnd]
	}
	return actualOld, buildReindent(fileBlock, oldLines), 1
}

// buildReindent returns a function that re-indents new_string so its block
// adopts the file block's indentation — but only when the file block and the
// model's old block differ by a single CONSISTENT leading-whitespace delta.
// Otherwise it returns nil and the caller uses new_string verbatim; we never
// guess a per-line indent.
//
// Two consistent shapes are reconciled (others → nil):
//   - file uniformly MORE indented (fileWS == add + oldWS on every non-blank
//     line) → prepend add to each non-blank new line.
//   - file uniformly LESS indented (oldWS == drop + fileWS) → strip the drop
//     prefix from each non-blank new line that carries it.
//
// Blank lines carry no indentation signal and are skipped on both sides. A
// tabs-vs-spaces difference with no string-prefix relationship is treated as
// inconsistent (→ verbatim), since reconciling it would need width-aware logic.
func buildReindent(fileBlock, oldBlock []string) func(string) string {
	if len(fileBlock) != len(oldBlock) {
		return nil
	}
	var add, drop string
	haveDelta := false
	for i := range fileBlock {
		fl, ol := fileBlock[i], oldBlock[i]
		if strings.TrimSpace(fl) == "" || strings.TrimSpace(ol) == "" {
			continue
		}
		fws, ows := leadingWS(fl), leadingWS(ol)
		var a, d string
		switch {
		case fws == ows:
			// no delta on this line
		case strings.HasSuffix(fws, ows): // fws = a + ows
			a = fws[:len(fws)-len(ows)]
		case strings.HasSuffix(ows, fws): // ows = d + fws
			d = ows[:len(ows)-len(fws)]
		default:
			return nil
		}
		if !haveDelta {
			add, drop, haveDelta = a, d, true
			continue
		}
		if a != add || d != drop {
			return nil
		}
	}
	if !haveDelta || (add == "" && drop == "") {
		return nil
	}
	return func(newString string) string {
		lines := strings.Split(newString, "\n")
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			switch {
			case add != "":
				lines[i] = add + l
			case drop != "" && strings.HasPrefix(leadingWS(l), drop):
				lines[i] = l[len(drop):]
			}
		}
		return strings.Join(lines, "\n")
	}
}
