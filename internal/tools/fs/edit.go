package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnny1110/evva/internal/tools"
)

// Curly quote constants — Claude (and other LLMs) often can't reproduce
// curly quotes verbatim, so we normalize curly↔straight when matching
// old_string against the file and apply the file's original quote style
// back to new_string. Ported from ref/src/tools/FileEditTool/utils.ts.
const (
	leftSingleCurly  = "‘"
	rightSingleCurly = "’"
	leftDoubleCurly  = "“"
	rightDoubleCurly = "”"
)

// markdownExt matches .md / .mdx — markdown uses two trailing spaces as
// a hard line break, so stripTrailingWhitespacePerLine would silently
// change semantics on these files. Ref TS uses /\.(md|mdx)$/i.
var markdownExt = regexp.MustCompile(`(?i)\.(md|mdx)$`)

type EditTool struct {
	tracker *ReadTracker
	workdir string
}

func NewEdit(tracker *ReadTracker, workdir string) *EditTool {
	return &EditTool{tracker: tracker, workdir: workdir}
}

func (t *EditTool) Name() string { return string(tools.EDIT_FILE) }

func (t *EditTool) Description() string {
	return `Performs exact string replacements in files.

Usage:
- You must use your ` + "`read`" + ` tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file. Edits also fail if the file's mtime has advanced on disk since the last read, or if the prior read was a partial-view (offset/limit) — in either case, re-read the file first.
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: line number + tab. Everything after that is the actual file content to match. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if ` + "`old_string`" + ` is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use ` + "`replace_all`" + ` to change every instance of ` + "`old_string`" + `.
- Use ` + "`replace_all`" + ` for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.
- To create a new file or fully overwrite one, use ` + "`write`" + ` instead.`
}

func (t *EditTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["file_path","old_string","new_string"],
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the file to modify."},
			"old_string":{"type":"string","description":"The text to replace. Must match byte-for-byte including whitespace and newlines. Do NOT include the line-number prefix from the read tool's output."},
			"new_string":{"type":"string","description":"The text to replace it with (must be different from old_string). Do NOT include any line-number prefix."},
			"replace_all":{"type":"boolean","default":false,"description":"Replace all occurrences of old_string (default false)."}
		}
	}`)
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *EditTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "edit: decode input: " + err.Error()}, nil
	}
	logger.Debug("edit.dispatch", "path", in.FilePath, "replace_all", in.ReplaceAll, "old_bytes", len(in.OldString), "new_bytes", len(in.NewString))

	resolved, err := resolvePath(in.FilePath, t.workdir)
	if err != nil {
		return tools.Result{IsError: true, Content: "edit: " + err.Error()}, nil
	}

	if in.OldString == in.NewString {
		return tools.Result{
			IsError: true,
			Content: "edit: no changes to make — old_string and new_string are exactly the same.",
		}, nil
	}

	// Stat is the discriminator for the three high-level branches:
	// file missing (maybe creating), file is a dir (error), file exists
	// (the normal edit path).
	info, statErr := os.Stat(resolved)
	switch {
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: stat %s: %s", in.FilePath, statErr)}, nil
	case statErr == nil && info.IsDir():
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: not a regular file: %s", in.FilePath)}, nil
	}
	fileMissing := errors.Is(statErr, fs.ErrNotExist)

	// File-creation path: empty old_string targeting a nonexistent file
	// is how the ref tool spells "create this file with new_string as
	// its content." Useful for tools / agents that emit a single edit
	// to scaffold a file. Skip tracker / encoding / quote logic — there
	// is nothing to match against.
	if fileMissing {
		if in.OldString != "" {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("edit: file does not exist: %s. To create a new file, call edit with old_string=\"\" or use write instead.", in.FilePath),
			}, nil
		}
		return t.createNewFile(resolved, in)
	}

	if strings.ToLower(filepath.Ext(resolved)) == ".ipynb" {
		return tools.Result{
			IsError: true,
			Content: "edit: editing Jupyter notebooks via edit_file is not supported — notebook structure (cells, outputs, metadata) is JSON-shaped and mutating raw bytes corrupts the file. Round-trip through a notebook editor instead.",
		}, nil
	}

	mem, err := readFileWithEncoding(resolved)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not read %s: %s", in.FilePath, err)}, nil
	}

	// Empty old_string + empty file = write new_string as initial
	// content. Empty old_string + non-empty file = misuse (matches ref
	// "Cannot create new file - file already exists.").
	if in.OldString == "" {
		if strings.TrimSpace(mem.content) != "" {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("edit: cannot create new file — %s already exists with content. To overwrite it, use write_file instead.", in.FilePath),
			}, nil
		}
		return t.applyToEmptyFile(resolved, info, mem, in)
	}

	if t.tracker != nil {
		if ok, reason := t.tracker.CanEdit(resolved, info.ModTime()); !ok {
			logger.Warn("edit.fail", "path", in.FilePath, "reason", reason)
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("edit: %s — path: %s", reason, in.FilePath),
			}, nil
		}
	}

	// findActualString returns the file's exact substring (possibly
	// with curly quotes the model couldn't reproduce). actualOld is
	// what we match against the file; new_string gets curly quotes
	// re-applied via preserveQuoteStyle if normalization happened.
	actualOld, found := findActualString(mem.content, in.OldString)
	if !found {
		hint := buildNotFoundHint(in.OldString, mem.content)
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"edit: old_string not found in %s.\n%s\n"+
					"Re-read the file and copy the exact text — including whitespace — that you want to replace.",
				in.FilePath, hint,
			),
		}, nil
	}

	count := strings.Count(mem.content, actualOld)
	if count > 1 && !in.ReplaceAll {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"edit: old_string matches %d locations in %s. "+
					"Either include more surrounding context in old_string to make it unique, or set replace_all=true to replace every occurrence.",
				count, in.FilePath,
			),
		}, nil
	}

	// Apply per-language post-processing to new_string before substitution:
	//   1. Strip trailing whitespace per line (except for .md / .mdx
	//      where two trailing spaces is a hard line break).
	//   2. Re-introduce the file's curly-quote style if the original
	//      match required quote normalization (preserveQuoteStyle).
	newStr := in.NewString
	if !markdownExt.MatchString(resolved) {
		newStr = stripTrailingWhitespacePerLine(newStr)
	}
	newStr = preserveQuoteStyle(in.OldString, actualOld, newStr)

	// Apply the substitution. applyEditToFile implements ref's
	// "delete-with-trailing-newline" cleanup so removing a line
	// doesn't leave a blank one behind.
	before := mem.content
	after := applyEditToFile(before, actualOld, newStr, in.ReplaceAll)
	if after == before {
		return tools.Result{
			IsError: true,
			Content: "edit: substitution produced no change. This usually means old_string and new_string normalize to the same content after quote / whitespace handling.",
		}, nil
	}

	// Compute line numbers on the LF-normalized content, then write
	// back with the file's original encoding and line endings.
	offsets := findAllOffsets(before, actualOld)
	if !in.ReplaceAll {
		offsets = offsets[:1]
	}
	oldLineNums := make([]int, len(offsets))
	for i, off := range offsets {
		oldLineNums[i] = lineNumberOf(before, off)
	}
	diff := buildEditDiff(resolved, before, after, actualOld, newStr, oldLineNums)

	if err := writeFileWithEncoding(resolved, after, mem.enc, mem.lf == endCRLF); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not write %s: %s", in.FilePath, err)}, nil
	}

	if t.tracker != nil {
		if newInfo, statErr := os.Stat(resolved); statErr == nil {
			t.tracker.Record(resolved, newInfo.ModTime(), false)
		} else {
			t.tracker.Forget(resolved)
		}
	}

	return tools.Result{
		Content:  editSummary(resolved, oldLineNums, in.ReplaceAll),
		Metadata: diff,
	}, nil
}

// createNewFile materializes a brand-new file from an Edit call with
// empty old_string and a non-empty target path. Creates parent dirs
// (mirrors ref behavior — ref calls fs.mkdir(dirname()) before the
// atomic write section).
func (t *EditTool) createNewFile(resolved string, in editInput) (tools.Result, error) {
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not create parent dirs: %s", err)}, nil
	}
	if err := os.WriteFile(resolved, []byte(in.NewString), 0o644); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not write %s: %s", in.FilePath, err)}, nil
	}
	if t.tracker != nil {
		if newInfo, statErr := os.Stat(resolved); statErr == nil {
			t.tracker.Record(resolved, newInfo.ModTime(), false)
		}
	}
	diff := buildCreateDiff(resolved, in.NewString)
	return tools.Result{
		Content:  fmt.Sprintf("created %s (%d lines, %d bytes)", resolved, countLines(in.NewString), len(in.NewString)),
		Metadata: diff,
	}, nil
}

// applyToEmptyFile handles the empty-file-with-empty-old_string case:
// the file exists but has nothing in it, so we just write new_string
// as the initial content. No matching, no quote handling.
func (t *EditTool) applyToEmptyFile(resolved string, info os.FileInfo, mem fileInMemory, in editInput) (tools.Result, error) {
	if t.tracker != nil {
		if ok, reason := t.tracker.CanEdit(resolved, info.ModTime()); !ok {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("edit: %s — path: %s", reason, in.FilePath),
			}, nil
		}
	}
	after := in.NewString
	if err := writeFileWithEncoding(resolved, after, mem.enc, mem.lf == endCRLF); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("edit: could not write %s: %s", in.FilePath, err)}, nil
	}
	if t.tracker != nil {
		if newInfo, statErr := os.Stat(resolved); statErr == nil {
			t.tracker.Record(resolved, newInfo.ModTime(), false)
		}
	}
	diff := buildCreateDiff(resolved, after)
	return tools.Result{
		Content:  fmt.Sprintf("populated empty file %s (%d lines, %d bytes)", resolved, countLines(after), len(after)),
		Metadata: diff,
	}, nil
}

// normalizeQuotes replaces curly quotes with straight quotes. Used by
// findActualString to find a match when the model's old_string uses
// straight quotes but the file has curly, or vice versa.
func normalizeQuotes(s string) string {
	if !strings.ContainsAny(s, leftSingleCurly+rightSingleCurly+leftDoubleCurly+rightDoubleCurly) {
		return s
	}
	r := strings.NewReplacer(
		leftSingleCurly, "'",
		rightSingleCurly, "'",
		leftDoubleCurly, `"`,
		rightDoubleCurly, `"`,
	)
	return r.Replace(s)
}

// findActualString returns the file substring that matches search,
// trying exact-byte-match first and then quote-normalized match. If
// the normalized match wins, the returned string is the file's
// version (which may have curly quotes the model didn't send).
func findActualString(fileContent, search string) (string, bool) {
	if strings.Contains(fileContent, search) {
		return search, true
	}
	normSearch := normalizeQuotes(search)
	normFile := normalizeQuotes(fileContent)
	idx := strings.Index(normFile, normSearch)
	if idx < 0 {
		return "", false
	}
	// Curly→straight quotes are a 1:1 rune mapping AND each rune is
	// the same byte length on both sides (3 bytes for curly, 1 for
	// straight) — so the rune offsets won't directly line up.
	// We walk the file character-by-character to map the normalized
	// byte offset back to a real-file byte offset, then slice out
	// the corresponding substring of len(search) characters' worth.
	fileStart := normalizedByteOffsetToReal(fileContent, idx)
	if fileStart < 0 {
		return "", false
	}
	// Count runes in `search`; slice the same rune count out of the
	// real file starting at fileStart.
	searchRunes := len([]rune(search))
	realRunes := []rune(fileContent[fileStart:])
	if searchRunes > len(realRunes) {
		return "", false
	}
	actual := string(realRunes[:searchRunes])
	return actual, true
}

// normalizedByteOffsetToReal walks the original string and returns
// the byte offset corresponding to the given offset in the normalized
// (curly→straight) version. Curly quotes collapse from 3 bytes to 1
// in the normalized representation, so the byte offsets diverge
// whenever the file contains any curly quote.
func normalizedByteOffsetToReal(original string, normOffset int) int {
	walked := 0
	i := 0
	for i < len(original) {
		if walked == normOffset {
			return i
		}
		r, size := utf8.DecodeRuneInString(original[i:])
		i += size
		switch r {
		case '‘', '’', '“', '”':
			walked++
		default:
			walked += size
		}
	}
	if walked == normOffset {
		return len(original)
	}
	return -1
}

// preserveQuoteStyle reintroduces the file's curly-quote style into
// new_string when findActualString matched via quote normalization.
// Without this, an edit replacing "hello" with "world" in a file that
// uses curly quotes around hello would silently strip them.
func preserveQuoteStyle(oldString, actualOldString, newString string) string {
	if oldString == actualOldString {
		return newString
	}
	hasDouble := strings.ContainsAny(actualOldString, leftDoubleCurly+rightDoubleCurly)
	hasSingle := strings.ContainsAny(actualOldString, leftSingleCurly+rightSingleCurly)
	if !hasDouble && !hasSingle {
		return newString
	}
	if hasDouble {
		newString = applyCurlyDoubleQuotes(newString)
	}
	if hasSingle {
		newString = applyCurlySingleQuotes(newString)
	}
	return newString
}

func isOpeningContext(runes []rune, idx int) bool {
	if idx == 0 {
		return true
	}
	prev := runes[idx-1]
	switch prev {
	case ' ', '\t', '\n', '\r', '(', '[', '{', '—', '–':
		return true
	}
	return false
}

func applyCurlyDoubleQuotes(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if r == '"' {
			if isOpeningContext(runes, i) {
				b.WriteString(leftDoubleCurly)
			} else {
				b.WriteString(rightDoubleCurly)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func applyCurlySingleQuotes(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		if r != '\'' {
			b.WriteRune(r)
			continue
		}
		var prev, next rune
		if i > 0 {
			prev = runes[i-1]
		}
		if i < len(runes)-1 {
			next = runes[i+1]
		}
		// Apostrophe-in-contraction (letter-quote-letter, e.g. "don't")
		// should use the right single curly per ref behavior.
		if unicode.IsLetter(prev) && unicode.IsLetter(next) {
			b.WriteString(rightSingleCurly)
			continue
		}
		if isOpeningContext(runes, i) {
			b.WriteString(leftSingleCurly)
		} else {
			b.WriteString(rightSingleCurly)
		}
	}
	return b.String()
}

// stripTrailingWhitespacePerLine removes trailing spaces / tabs from
// each line, preserving line endings. Markdown files bypass this
// (caller's responsibility).
func stripTrailingWhitespacePerLine(s string) string {
	if s == "" {
		return s
	}
	// Split on line terminators but keep them. Go's strings.Split
	// loses the separator; we re-add by tracking positions.
	var b strings.Builder
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' {
			line := s[start:i]
			b.WriteString(strings.TrimRight(line, " \t"))
			// Handle CRLF as one terminator.
			if c == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				b.WriteString("\r\n")
				i++
			} else {
				b.WriteByte(c)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		b.WriteString(strings.TrimRight(s[start:], " \t"))
	}
	return b.String()
}

// applyEditToFile implements ref's substitution semantics:
//   - replace_all=false → replace first occurrence;
//   - replace_all=true → replace every occurrence;
//   - if new_string is empty AND old_string doesn't end with "\n" AND
//     the file contains old_string + "\n", strip the trailing newline
//     too. Keeps "delete this line" edits from leaving blank lines.
func applyEditToFile(originalContent, oldString, newString string, replaceAll bool) string {
	doReplace := func(haystack, needle, replacement string) string {
		if replaceAll {
			return strings.ReplaceAll(haystack, needle, replacement)
		}
		return strings.Replace(haystack, needle, replacement, 1)
	}

	if newString != "" {
		return doReplace(originalContent, oldString, newString)
	}
	// new_string is empty — try to consume the trailing newline so the
	// file doesn't grow a blank line where the deleted content used to
	// be.
	if !strings.HasSuffix(oldString, "\n") && strings.Contains(originalContent, oldString+"\n") {
		return doReplace(originalContent, oldString+"\n", newString)
	}
	return doReplace(originalContent, oldString, newString)
}

func findAllOffsets(haystack, needle string) []int {
	if needle == "" {
		return nil
	}
	out := make([]int, 0, 1)
	start := 0
	for {
		i := strings.Index(haystack[start:], needle)
		if i < 0 {
			return out
		}
		out = append(out, start+i)
		start += i + len(needle)
	}
}

func editSummary(path string, lineNums []int, replaceAll bool) string {
	if len(lineNums) == 0 {
		return fmt.Sprintf("edited %s", path)
	}
	parts := make([]string, len(lineNums))
	for i, n := range lineNums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	verb := "replacement"
	if len(lineNums) != 1 {
		verb += "s"
	}
	suffix := ""
	if replaceAll {
		suffix = " [replace_all]"
	}
	return fmt.Sprintf("edited %s (%d %s at line(s) %s)%s",
		path, len(lineNums), verb, strings.Join(parts, ", "), suffix)
}

func buildEditDiff(path, before, after, oldStr, newStr string, oldLineNums []int) *FileDiff {
	oldLines := splitLinesPreservingEnd(before)
	newLines := splitLinesPreservingEnd(after)

	changedOld := lineSpan(oldStr)
	changedNew := lineSpan(newStr)

	hunks := make([]DiffHunk, 0, len(oldLineNums))
	delta := 0
	for _, oldLineNum := range oldLineNums {
		newLineNum := oldLineNum + delta
		hunks = append(hunks, buildEditHunk(oldLines, newLines, oldLineNum, newLineNum, changedOld, changedNew))
		delta += changedNew - changedOld
	}

	return &FileDiff{Path: path, Op: OpEdit, Hunks: hunks}
}

func lineSpan(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	if n < 1 {
		return 1
	}
	return n
}

func buildNotFoundHint(old, fileContent string) string {
	var b strings.Builder

	if idx := strings.IndexByte(old, '\t'); idx >= 0 {
		stripped := old[idx+1:]
		if stripped != "" && strings.Contains(fileContent, stripped) {
			b.WriteString("Hint: your old_string appears to include a line-number prefix (e.g. \"    42\\thello\"). ")
			b.WriteString("Strip the `<n>\\t` prefix from the read_file output — provide only the raw line content.\n")
		}
	}

	// CRLF check — if the file is CRLF but old_string is LF, the
	// model's match will fail silently. We already normalize CRLF→LF
	// in readFileForEdit, but if the model included literal "\r\n"
	// in old_string it won't match the normalized content.
	if strings.Contains(old, "\r\n") {
		b.WriteString("Hint: your old_string contains literal CRLF (\\r\\n) sequences. The tool normalizes line endings to LF (\\n) internally, so include only \\n in old_string.\n")
	}

	// Smart-quote check.
	if strings.ContainsAny(old, leftSingleCurly+rightSingleCurly+leftDoubleCurly+rightDoubleCurly) &&
		!strings.ContainsAny(fileContent, leftSingleCurly+rightSingleCurly+leftDoubleCurly+rightDoubleCurly) {
		b.WriteString("Hint: your old_string contains curly quotes (' ' \" \") but the file uses straight quotes. The tool normalizes both directions automatically — re-check that the rest of the line matches exactly.\n")
	}

	firstLine := fileContent
	if idx := strings.IndexByte(fileContent, '\n'); idx >= 0 {
		firstLine = fileContent[:idx]
	}
	if len(firstLine) > 80 {
		firstLine = firstLine[:80] + "..."
	}
	b.WriteString("File starts with: ")
	b.WriteString(fmt.Sprintf("%q", firstLine))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("old_string: %d bytes; file: %d bytes.", len(old), len(fileContent)))
	return b.String()
}
