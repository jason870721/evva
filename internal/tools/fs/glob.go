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
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/johnny1110/evva/internal/tools"
)

// Default timeout for glob walks — prevents a broad pattern on a large
// filesystem (e.g. path="/", pattern="**/*.go") from walking indefinitely.
const defaultGlobTimeout = 2 * time.Minute

// globResultLimit caps how many entries a Glob call returns. Matches
// ref's 100-file cap; the model gets a `(Results are truncated. ...)`
// footer when more matches exist.
const globResultLimit = 100

// truncationNote — the verbatim string ref appends to the filename
// list when results were truncated to globResultLimit.
const globTruncationNote = "(Results are truncated. Consider using a more specific path or pattern.)"

// emptyResultMessage — the verbatim string ref returns for zero
// matches.
const globEmptyMessage = "No files found"

type GlobTool struct {
	workdir string
}

func NewGlob(workdir string) *GlobTool { return &GlobTool{workdir: workdir} }

func (t *GlobTool) Name() string { return string(tools.GLOB) }

func (t *GlobTool) Description() string {
	return `- Fast file pattern matching tool that works with any codebase size
- Supports glob patterns like "**/*.js" or "src/**/*.ts"
- Returns matching file paths sorted by modification time
- Use this tool when you need to find files by name patterns
- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the agent tool instead`
}

func (t *GlobTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["pattern"],
		"properties":{
			"pattern":{"type":"string","description":"The glob pattern to match files against (e.g. \"**/*.go\", \"src/**/*.ts\")."},
			"path":{"type":"string","description":"The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided."}
		}
	}`)
}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type globMatch struct {
	Path  string
	Mtime time.Time
}

func (t *GlobTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in globInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: "glob: decode input: " + err.Error()}, nil
	}
	if in.Pattern == "" {
		return tools.Result{IsError: true, Content: "glob: pattern is required"}, nil
	}
	logger.Debug("glob.dispatch", "pattern", in.Pattern, "path", in.Path)

	// Validate `path` first if supplied — ref does this in
	// validateInput before any glob work begins.
	var searchDir string
	if in.Path != "" {
		resolved, err := resolvePath(in.Path, t.workdir)
		if err != nil {
			return tools.Result{IsError: true, Content: "glob: " + err.Error()}, nil
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return tools.Result{
					IsError: true,
					Content: fmt.Sprintf("glob: directory does not exist: %s.", in.Path),
				}, nil
			}
			return tools.Result{IsError: true, Content: fmt.Sprintf("glob: stat search root %s: %s", in.Path, statErr)}, nil
		}
		if !info.IsDir() {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("glob: path is not a directory: %s.", in.Path),
			}, nil
		}
		searchDir = resolved
	} else {
		// Default search dir = configured workdir (same behavior as
		// resolvePath("") would conceptually give us).
		cwd, err := resolvePath(".", t.workdir)
		if err != nil {
			return tools.Result{IsError: true, Content: "glob: " + err.Error()}, nil
		}
		searchDir = cwd
	}

	searchPattern := in.Pattern

	// Absolute-pattern handling — ref's extractGlobBaseDirectory. A
	// pattern like "/abs/path/**/*.go" is split into searchDir="/abs/path"
	// + searchPattern="**/*.go" so doublestar matches against paths
	// relative to that root. Without this, absolute patterns silently
	// match nothing.
	if filepath.IsAbs(in.Pattern) {
		baseDir, relPattern := extractGlobBaseDirectory(in.Pattern)
		if baseDir != "" {
			searchDir = baseDir
			searchPattern = relPattern
		}
	}

	if !doublestar.ValidatePattern(searchPattern) {
		return tools.Result{IsError: true, Content: fmt.Sprintf("glob: invalid pattern %q", in.Pattern)}, nil
	}

	walkCtx, cancel := context.WithTimeout(ctx, defaultGlobTimeout)
	defer cancel()

	// Run the walk in a separate goroutine so a hard timeout (or parent
	// cancellation) can return early even if the walk is stuck in ReadDir
	// on a slow mount. The callback still checks walkCtx.Err() for a
	// clean early exit when matching entries arrive.
	type walkResult struct {
		matches []globMatch
		err     error
	}
	var matches []globMatch
	ch := make(chan walkResult, 1)
	go func() {
		var m []globMatch
		fsys := os.DirFS(searchDir)
		err := doublestar.GlobWalk(fsys, searchPattern, func(rel string, d fs.DirEntry) error {
			select {
			case <-walkCtx.Done():
				return walkCtx.Err()
			default:
			}
			if d.IsDir() {
				return nil
			}
			if len(m) > globResultLimit {
				return fs.SkipAll
			}
			absPath := filepath.Join(searchDir, rel)
			info, statErr := d.Info()
			if statErr != nil {
				return nil
			}
			m = append(m, globMatch{Path: absPath, Mtime: info.ModTime()})
			return nil
		})
		ch <- walkResult{matches: m, err: err}
	}()

	var walkErr error
	select {
	case <-walkCtx.Done():
		return tools.Result{IsError: true, Content: "glob: timed out after " + defaultGlobTimeout.String()}, nil
	case res := <-ch:
		matches = res.matches
		walkErr = res.err
	}
	if walkErr != nil && !errorIsContextCancelled(walkErr) && !errors.Is(walkErr, fs.SkipAll) {
		return tools.Result{IsError: true, Content: fmt.Sprintf("glob: walk failed: %s", walkErr)}, nil
	}

	// Ascending sort (oldest first) — matches ref's ripgrep
	// `--sort=modified` invocation. Truncation then takes the head of
	// the sorted list (the globResultLimit oldest matches if there
	// are more than that).
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Mtime.Before(matches[j].Mtime)
	})

	total := len(matches)
	truncated := false
	if total > globResultLimit {
		matches = matches[:globResultLimit]
		truncated = true
	}
	logger.Debug("glob.result", "matches", total, "truncated", truncated)

	if total == 0 {
		return tools.Result{Content: globEmptyMessage}, nil
	}

	// Ref relativizes paths under cwd to save tokens. We do the same:
	// if the match lives under the configured workdir, return the
	// relative form; otherwise keep absolute.
	relRoot := t.workdir
	if relRoot == "" {
		relRoot, _ = os.Getwd()
	}
	var out strings.Builder
	for i, m := range matches {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(toRelativeOrAbs(m.Path, relRoot))
	}
	if truncated {
		out.WriteByte('\n')
		out.WriteString(globTruncationNote)
	}
	return tools.Result{Content: out.String()}, nil
}

// extractGlobBaseDirectory finds the longest static prefix of an
// absolute pattern (everything before the first glob special char)
// and returns it as the search root with the rest as the relative
// pattern.
//
// Examples (Unix):
//
//	"/abs/path/**/*.go" → ("/abs/path", "**/*.go")
//	"/abs/file.go"       → ("/abs", "file.go")
//	"/*.go"              → ("/", "*.go")
//	"/abs/dir/*"         → ("/abs/dir", "*")
//
// Ported from ref/src/utils/glob.ts:extractGlobBaseDirectory.
func extractGlobBaseDirectory(pattern string) (baseDir, relativePattern string) {
	idx := strings.IndexAny(pattern, "*?[{")
	if idx < 0 {
		// No glob chars — pattern is a literal path. Split into
		// dirname / basename so the caller still searches the right
		// directory.
		return filepath.Dir(pattern), filepath.Base(pattern)
	}
	staticPrefix := pattern[:idx]
	lastSep := strings.LastIndexByte(staticPrefix, '/')
	if filepath.Separator != '/' {
		if alt := strings.LastIndexByte(staticPrefix, filepath.Separator); alt > lastSep {
			lastSep = alt
		}
	}
	if lastSep < 0 {
		return "", pattern
	}
	baseDir = staticPrefix[:lastSep]
	relativePattern = pattern[lastSep+1:]
	// Root path: "/" stripped to "" — restore so we don't end up
	// searching the cwd by accident.
	if baseDir == "" && lastSep == 0 {
		baseDir = "/"
	}
	return baseDir, relativePattern
}

// toRelativeOrAbs returns the path relative to workdir if it's under
// workdir, otherwise the original absolute path. Mirrors ref's
// toRelativePath behavior for output trimming.
func toRelativeOrAbs(absPath, workDir string) string {
	if workDir == "" {
		return absPath
	}
	rel, err := filepath.Rel(workDir, absPath)
	if err != nil {
		return absPath
	}
	// If the relative path climbs out of workdir (.. prefix), fall
	// back to absolute — the path isn't really under cwd.
	if strings.HasPrefix(rel, "..") {
		return absPath
	}
	return rel
}

func errorIsContextCancelled(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
