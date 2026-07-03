package mode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

const worktreeListDescription = `List the live git worktrees managed by evva (those under ` + "`.evva/worktrees/`" + `) so you can review parallel work before integrating it.

## When to Use

- After fanning work out into isolated worktrees (subagents spawned with ` + "`isolation: \"worktree\"`" + `, or enter_worktree sessions), to see every branch, how far ahead/behind the base each is, whether it has uncommitted changes, and which are still owned by a running subagent — before reconciling them with ` + "`exit_worktree` action: \"merge\"`" + `.

## Output

One row per managed worktree:
- ` + "`branch`" + ` — the worktree's branch (pass this to exit_worktree's merge action)
- ` + "`base`" + ` — the branch the merge would target (the main worktree's branch)
- ` + "`ahead`" + ` / ` + "`behind`" + ` — commits the branch is ahead of / behind the base
- ` + "`dirty`" + ` — present when the worktree has uncommitted changes (commit before merging)
- ` + "`owner`" + ` — the subagent daemon still writing this worktree, if any (running ⇒ not finished yet)

Read-only. Lists nothing (not an error) when there are no managed worktrees.`

const worktreeListSchema = `{"type":"object","additionalProperties":false,"properties":{}}`

// WorktreeListTool enumerates the live worktrees under .evva/worktrees/ with
// their branch / ahead-behind / dirty status and a cross-reference into the
// daemon catalog (so a worktree still owned by a running subagent is flagged).
// It is the collect surface for the fan-out → review → reconcile loop.
type WorktreeListTool struct {
	lookup  WorktreeControllerLookup
	daemons *daemon.DaemonState
}

// NewList builds the worktree_list tool. daemons may be nil (the owner column
// is simply omitted then).
func NewList(lookup WorktreeControllerLookup, daemons *daemon.DaemonState) *WorktreeListTool {
	return &WorktreeListTool{lookup: lookup, daemons: daemons}
}

func (t *WorktreeListTool) Name() string            { return string(tools.WORKTREE_LIST) }
func (t *WorktreeListTool) Description() string     { return worktreeListDescription }
func (t *WorktreeListTool) Schema() json.RawMessage { return json.RawMessage(worktreeListSchema) }

func (t *WorktreeListTool) Execute(ctx context.Context, logger *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	ctrl := resolveWorktreeController(t.lookup)
	if ctrl == nil {
		return tools.Result{
			IsError: true,
			Content: "worktree_list: no worktree controller installed (only the root agent can list worktrees)",
		}, nil
	}

	entries, err := parseWorktreeList(ctx, ctrl.Workdir())
	if err != nil {
		return tools.Result{IsError: true, Content: "worktree_list: " + err.Error()}, nil
	}
	if len(entries) == 0 || !entries[0].isMain {
		return tools.Result{Content: "no worktrees"}, nil
	}
	base := entries[0]
	baseBranchRaw, _ := runGit(ctx, base.Path, "rev-parse", "--abbrev-ref", "HEAD")
	baseBranch := strings.TrimSpace(baseBranchRaw)

	// Cross-ref: map an isolation worktree path back to the daemon writing it.
	owners := map[string]daemon.DaemonSnapshot{}
	if t.daemons != nil {
		for _, s := range t.daemons.Snapshot() {
			if s.Kind != daemon.KindLocalAgent {
				continue
			}
			if m, ok := s.Metadata.(daemon.LocalAgentMeta); ok && m.WorktreePath != "" {
				// Normalize: the daemon's path comes from filepath.Join
				// (OS-native separators) but entry paths come from
				// `git worktree list` (always forward slashes), so on
				// Windows the raw strings wouldn't match.
				owners[filepath.Clean(m.WorktreePath)] = s
			}
		}
	}

	var rows []string
	for _, e := range entries {
		if e.isMain || !isManagedWorktree(e.Path) {
			continue
		}
		ahead, behind, _ := aheadBehind(ctx, base.Path, baseBranch, e.Branch)
		dirty := false
		if st, derr := runGit(ctx, e.Path, "status", "--porcelain"); derr == nil {
			dirty = strings.TrimSpace(st) != ""
		}
		dirtyFlag := ""
		if dirty {
			dirtyFlag = " dirty"
		}
		owner := ""
		if s, ok := owners[filepath.Clean(e.Path)]; ok {
			state := "done"
			if !daemon.IsTerminal(s.Status) {
				state = "running"
			}
			owner = fmt.Sprintf(" owner=%s(%s)", s.ID, state)
		}
		rows = append(rows, fmt.Sprintf(
			"- %s [branch=%s base=%s ahead=%d behind=%d%s]%s",
			e.Path, e.Branch, baseBranch, ahead, behind, dirtyFlag, owner,
		))
	}
	if len(rows) == 0 {
		return tools.Result{Content: "no worktrees"}, nil
	}
	logger.Debug("worktree_list.ok", "count", len(rows))
	var b strings.Builder
	fmt.Fprintf(&b, "%d worktree(s) (base %s):\n", len(rows), baseBranch)
	b.WriteString(strings.Join(rows, "\n"))
	return tools.Result{Content: b.String()}, nil
}

// --- shared git helpers (used by worktree_list and the merge action) ---

// worktreeEntry is one record from `git worktree list --porcelain`.
type worktreeEntry struct {
	Path     string
	Branch   string // short name ("" when detached/bare)
	Head     string
	isMain   bool // the first entry is the repo's primary worktree
	Bare     bool
	Detached bool
}

// parseWorktreeList runs `git worktree list --porcelain` from fromDir (any
// worktree works — the list is repo-wide and the primary worktree is first)
// and parses the records. Blank lines separate entries.
func parseWorktreeList(ctx context.Context, fromDir string) ([]worktreeEntry, error) {
	out, err := runGit(ctx, fromDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %v: %s", err, strings.TrimSpace(out))
	}
	var entries []worktreeEntry
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, worktreeEntry{Path: strings.TrimPrefix(line, "worktree ")})
		case len(entries) == 0:
			continue
		case strings.HasPrefix(line, "HEAD "):
			entries[len(entries)-1].Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			entries[len(entries)-1].Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			entries[len(entries)-1].Bare = true
		case line == "detached":
			entries[len(entries)-1].Detached = true
		}
	}
	if len(entries) > 0 {
		entries[0].isMain = true
	}
	return entries, nil
}

// isManagedWorktree reports whether path is an evva-managed worktree — its
// parent directory is the `.evva/worktrees` segment.
func isManagedWorktree(path string) bool {
	parent := filepath.ToSlash(filepath.Dir(path))
	seg := filepath.ToSlash(permission.WorktreeDirSegment)
	return parent == seg || strings.HasSuffix(parent, "/"+seg)
}

// aheadBehind returns how many commits childBranch is ahead of / behind
// baseBranch via `git rev-list --left-right --count base...child` (left =
// base-only = behind, right = child-only = ahead). Run from dir.
func aheadBehind(ctx context.Context, dir, baseBranch, childBranch string) (ahead, behind int, err error) {
	out, gerr := runGit(ctx, dir, "rev-list", "--left-right", "--count", baseBranch+"..."+childBranch)
	if gerr != nil {
		return 0, 0, fmt.Errorf("git rev-list: %v: %s", gerr, strings.TrimSpace(out))
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", strings.TrimSpace(out))
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// worktreeBranchesHint lists the mergeable child branches for an error
// message when a requested branch isn't found.
func worktreeBranchesHint(entries []worktreeEntry) string {
	var bs []string
	for _, e := range entries {
		if e.isMain || e.Branch == "" || !isManagedWorktree(e.Path) {
			continue
		}
		bs = append(bs, e.Branch)
	}
	if len(bs) == 0 {
		return " No live worktrees to merge."
	}
	return " Available: " + strings.Join(bs, ", ") + "."
}

// countNonEmptyLines counts the non-blank lines of s.
func countNonEmptyLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// bulletLines renders each non-blank line of s as an indented bullet.
func bulletLines(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		b.WriteString("  - ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
