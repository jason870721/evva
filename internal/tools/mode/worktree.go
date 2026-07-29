package mode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
)

// maxSlugLen mirrors the ref source: 64 chars total across all segments,
// long enough for a descriptive name without blowing past filesystem
// limits when combined with the worktree directory prefix.
const maxSlugLen = 64

// slugSegmentRE accepts one segment between the slash separators: letters,
// digits, dot, underscore, dash. Forbids "." / ".." entirely (the
// validateSlug check rejects them after splitting).
var slugSegmentRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// --- EnterWorktree -----------------------------------------------------

const enterWorktreeDescription = `Use this tool ONLY when the user explicitly asks to work in a worktree. This tool creates an isolated git worktree and switches the current session into it.

## When to Use

- The user explicitly says "worktree" (e.g., "start a worktree", "work in a worktree", "create a worktree", "use a worktree")

## When NOT to Use

- The user asks to create a branch, switch branches, or work on a different branch — use git commands instead
- The user asks to fix a bug or work on a feature — use normal git workflow unless they specifically mention worktrees
- Never use this tool unless the user explicitly mentions "worktree"

## Requirements

- Must be in a git repository
- Must not already be in a worktree session created by this tool

## Behavior

- Creates a new git worktree inside ` + "`.evva/worktrees/`" + ` at the repository root, on a new branch ` + "`worktree-<slug>`" + ` based on HEAD
- Switches the agent's working directory to the new worktree — subsequent file reads, edits, and bash commands run there
- Use exit_worktree to leave the worktree mid-session (keep or remove). The exit tool is a no-op when no worktree session is active

## Parameters

- ` + "`name`" + ` (optional): A name for the worktree. Each "/"-separated segment may contain only letters, digits, dots, underscores, and dashes; max 64 chars total. If omitted, a random name is generated.
`

const enterWorktreeSchema = `{
	"type":"object",
	"additionalProperties":false,
	"properties":{
		"name":{"type":"string","description":"Optional name for the new worktree. Each \"/\"-separated segment may contain only letters, digits, dots, underscores, and dashes; max 64 chars total."}
	}
}`

type enterWorktreeInput struct {
	Name string `json:"name"`
}

// EnterWorktreeTool creates `.evva/worktrees/<slug>/` on a new branch
// `worktree-<slug>` and switches the agent's workdir into it.
type EnterWorktreeTool struct {
	lookup WorktreeControllerLookup
}

func NewEnterWorktree(lookup WorktreeControllerLookup) *EnterWorktreeTool {
	return &EnterWorktreeTool{lookup: lookup}
}

func (t *EnterWorktreeTool) Name() string            { return string(tools.ENTER_WORKTREE) }
func (t *EnterWorktreeTool) Description() string     { return enterWorktreeDescription }
func (t *EnterWorktreeTool) Schema() json.RawMessage { return json.RawMessage(enterWorktreeSchema) }

func (t *EnterWorktreeTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	ctrl := resolveWorktreeController(t.lookup)
	if ctrl == nil {
		return tools.Result{
			IsError: true,
			Content: "enter_worktree: no worktree controller installed (only the root agent may enter a worktree)",
		}, nil
	}
	if ctrl.WorktreeSession() != nil {
		return tools.Result{
			IsError: true,
			Content: "enter_worktree: already in a worktree session — call exit_worktree first",
		}, nil
	}

	var in enterWorktreeInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return tools.Result{IsError: true, Content: fmt.Sprintf("enter_worktree: decode input: %v", err)}, nil
		}
	}

	slug, err := resolveSlug(in.Name)
	if err != nil {
		return tools.Result{IsError: true, Content: "enter_worktree: " + err.Error()}, nil
	}

	original := ctrl.Workdir()
	if original == "" {
		return tools.Result{IsError: true, Content: "enter_worktree: agent has no working directory"}, nil
	}
	repoRoot, err := gitTopLevel(ctx, original)
	if err != nil {
		return tools.Result{IsError: true, Content: "enter_worktree: not in a git repository (" + err.Error() + ")"}, nil
	}

	flat := flattenSlug(slug)
	worktreePath := worktreeDirFor(repoRoot, flat)
	branch := branchNameFor(flat)

	if _, statErr := os.Stat(worktreePath); statErr == nil {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("enter_worktree: %s already exists — pick a different name or remove it first", worktreePath),
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return tools.Result{IsError: true, Content: "enter_worktree: cannot create worktree parent dir: " + err.Error()}, nil
	}

	if out, gErr := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD"); gErr != nil {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("enter_worktree: git worktree add failed: %v\n%s", gErr, out),
		}, nil
	}

	if err := ctrl.SwitchWorkdir(worktreePath); err != nil {
		// Roll back the worktree on switch failure so a half-created session
		// doesn't strand the workdir; best-effort, log on failure.
		if rmOut, rmErr := runGit(ctx, repoRoot, "worktree", "remove", "--force", worktreePath); rmErr != nil {
			logger.Warn("enter_worktree: rollback failed", "err", rmErr, "out", rmOut)
		}
		return tools.Result{IsError: true, Content: "enter_worktree: switch workdir failed: " + err.Error()}, nil
	}

	ctrl.BeginWorktreeSession(WorktreeSession{
		OriginalWorkdir: original,
		Path:            worktreePath,
		Branch:          branch,
		Slug:            flat,
		CreatedAt:       time.Now(),
	})

	logger.Info("enter_worktree", "path", worktreePath, "branch", branch, "slug", flat)
	return tools.Result{
		Content: fmt.Sprintf(
			"Entered worktree.\n  path:   %s\n  branch: %s\n\nSubsequent file reads, edits, and bash commands run in the worktree. Use exit_worktree (action=\"keep\" or \"remove\") to leave.",
			worktreePath, branch,
		),
	}, nil
}

// --- ExitWorktree ------------------------------------------------------

const exitWorktreeDescription = `Exit a worktree session created by enter_worktree and return the session to the original working directory.

## Scope

This tool ONLY operates on worktrees created by enter_worktree in this session. It will NOT touch:
- Worktrees you created manually with ` + "`git worktree add`" + `
- Worktrees from a previous session
- The directory you're in if enter_worktree was never called

If called outside an enter_worktree session, the tool is a **no-op**: it reports that no worktree session is active and takes no action. Filesystem state is unchanged.

## When to Use

- The user explicitly asks to "exit the worktree", "leave the worktree", "go back", or otherwise end the worktree session
- Do NOT call this proactively — only when the user asks

## Parameters

- ` + "`action`" + ` (required): ` + "`\"keep\"`" + `, ` + "`\"remove\"`" + `, or ` + "`\"merge\"`" + `
  - ` + "`\"keep\"`" + ` — leave the worktree directory and branch intact on disk. Use this if the user wants to come back to the work later, or if there are changes to preserve.
  - ` + "`\"remove\"`" + ` — delete the worktree directory and its branch. Use this for a clean exit when the work is done or abandoned.
  - ` + "`\"merge\"`" + ` — integrate the worktree branch back into the base branch (the branch checked out in the main worktree), then remove the worktree on success. The worker must have committed its work first. Conflicts are reported and the merge is aborted — nothing is half-applied. This is how you reconcile work done in an isolated worktree (including one left by a finished ` + "`isolation:\"worktree\"`" + ` subagent — list candidates with worktree_list).
- ` + "`discard_changes`" + ` (optional, default false): only meaningful with ` + "`action: \"remove\"`" + `. If the worktree has uncommitted files or commits not on the original branch, the tool will REFUSE to remove it unless this is set to ` + "`true`" + `. If the tool returns an error listing changes, confirm with the user before re-invoking with ` + "`discard_changes: true`" + `.
- ` + "`branch`" + ` (optional): only meaningful with ` + "`action: \"merge\"`" + `. The branch of a specific live worktree to merge (e.g. ` + "`worktree-explore-ab12`" + `, as shown by worktree_list). Omit to merge the worktree of the active enter_worktree session. Provide it to merge a worktree you are NOT currently inside — e.g. one a finished subagent left behind.

## Behavior

- ` + "`keep`" + ` / ` + "`remove`" + ` restore the agent's working directory to where it was before enter_worktree, reload the EVVA.md / USER_PROFILE.md snapshot, and rebuild the active filesystem + bash tools against the original workdir
- On ` + "`action: \"remove\"`" + ` with no pending changes (or with ` + "`discard_changes: true`" + `): runs ` + "`git worktree remove --force <path>`" + ` and deletes the worktree branch
- On ` + "`action: \"merge\"`" + `: runs ` + "`git merge --no-ff <branch>`" + ` from the base checkout. On success: removes the worktree + branch (and, for the active session, returns to the original workdir). On conflict: runs ` + "`git merge --abort`" + ` and reports the conflicted paths, leaving the worktree intact. Refuses if the worktree has uncommitted changes; reports a no-op when there is nothing to integrate
- Once exited, enter_worktree can be called again to create a fresh worktree
`

const exitWorktreeSchema = `{
	"type":"object",
	"additionalProperties":false,
	"required":["action"],
	"properties":{
		"action":{"type":"string","enum":["keep","remove","merge"],"description":"\"keep\" leaves the worktree and branch on disk; \"remove\" deletes both; \"merge\" integrates the worktree branch into the base branch, then removes the worktree on success."},
		"discard_changes":{"type":"boolean","description":"Required true when action is \"remove\" and the worktree has uncommitted files or unmerged commits."},
		"branch":{"type":"string","description":"Only for action \"merge\": the branch of a specific live worktree to merge (see worktree_list). Omit to merge the active session's worktree."}
	}
}`

type exitWorktreeInput struct {
	Action         string `json:"action"`
	DiscardChanges bool   `json:"discard_changes"`
	Branch         string `json:"branch"`
}

// ExitWorktreeTool tears down (or keeps) a worktree session opened by
// EnterWorktree, restoring the agent's original workdir.
type ExitWorktreeTool struct {
	lookup WorktreeControllerLookup
}

func NewExitWorktree(lookup WorktreeControllerLookup) *ExitWorktreeTool {
	return &ExitWorktreeTool{lookup: lookup}
}

func (t *ExitWorktreeTool) Name() string            { return string(tools.EXIT_WORKTREE) }
func (t *ExitWorktreeTool) Description() string     { return exitWorktreeDescription }
func (t *ExitWorktreeTool) Schema() json.RawMessage { return json.RawMessage(exitWorktreeSchema) }

func (t *ExitWorktreeTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	ctrl := resolveWorktreeController(t.lookup)
	if ctrl == nil {
		return tools.Result{
			IsError: true,
			Content: "exit_worktree: no worktree controller installed",
		}, nil
	}

	var in exitWorktreeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("exit_worktree: decode input: %v", err)}, nil
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))

	// merge can target a specific worktree by branch, so it is valid even
	// with no active session (e.g. integrating a finished subagent's
	// worktree). Handle it before the no-active-session no-op below.
	if action == "merge" {
		return t.executeMerge(ctx, ctrl, logger, in)
	}

	sess := ctrl.WorktreeSession()
	if sess == nil {
		// Per ref spec: no-op when no session is active.
		return tools.Result{
			Content: "exit_worktree: no worktree session active — nothing to do.",
		}, nil
	}

	if action != "keep" && action != "remove" {
		return tools.Result{IsError: true, Content: `exit_worktree: action must be "keep", "remove", or "merge"`}, nil
	}

	if action == "keep" {
		if err := ctrl.SwitchWorkdir(sess.OriginalWorkdir); err != nil {
			return tools.Result{IsError: true, Content: "exit_worktree: switch back failed: " + err.Error()}, nil
		}
		ctrl.EndWorktreeSession()
		logger.Info("exit_worktree", "action", "keep", "path", sess.Path, "branch", sess.Branch)
		return tools.Result{
			Content: fmt.Sprintf(
				"Worktree kept on disk.\n  path:   %s\n  branch: %s\n\nReturned to %s.",
				sess.Path, sess.Branch, sess.OriginalWorkdir,
			),
		}, nil
	}

	// action == "remove"
	dirty, summary := worktreeHasChanges(ctx, sess.Path)
	if dirty && !in.DiscardChanges {
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf(
				"exit_worktree: worktree has uncommitted changes — refusing to remove without explicit confirmation.\n%s\n\nConfirm with the user, then re-invoke with discard_changes=true.",
				summary,
			),
		}, nil
	}

	// Switch back BEFORE removing so the agent isn't sitting in a directory
	// we're about to delete.
	if err := ctrl.SwitchWorkdir(sess.OriginalWorkdir); err != nil {
		return tools.Result{IsError: true, Content: "exit_worktree: switch back failed: " + err.Error()}, nil
	}

	repoRoot, err := gitTopLevel(ctx, sess.OriginalWorkdir)
	if err != nil {
		// We're not in a git repo from the restored workdir — leave the
		// worktree on disk, end the session, surface the warning.
		ctrl.EndWorktreeSession()
		return tools.Result{
			IsError: true,
			Content: "exit_worktree: cannot resolve repo root after switch back: " + err.Error() + ". Worktree left on disk at " + sess.Path,
		}, nil
	}

	rmOut, rmErr := runGit(ctx, repoRoot, "worktree", "remove", "--force", sess.Path)
	if rmErr != nil {
		ctrl.EndWorktreeSession()
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("exit_worktree: git worktree remove failed: %v\n%s\nWorktree may still be on disk at %s", rmErr, rmOut, sess.Path),
		}, nil
	}
	// Best-effort branch delete. -D forces removal even if the branch isn't
	// merged into the original branch — by this point the user already
	// agreed to discard, or there were no changes to merge.
	if brOut, brErr := runGit(ctx, repoRoot, "branch", "-D", sess.Branch); brErr != nil {
		logger.Warn("exit_worktree: branch delete failed (worktree removed successfully)", "branch", sess.Branch, "err", brErr, "out", brOut)
	}

	ctrl.EndWorktreeSession()
	logger.Info("exit_worktree", "action", "remove", "path", sess.Path, "branch", sess.Branch, "discarded", dirty)
	msg := fmt.Sprintf(
		"Worktree removed.\n  path:   %s\n  branch: %s\n\nReturned to %s.",
		sess.Path, sess.Branch, sess.OriginalWorkdir,
	)
	if dirty {
		msg += "\n\n" + summary + "\n(discarded per discard_changes=true)"
	}
	return tools.Result{Content: msg}, nil
}

// executeMerge integrates a worktree branch back into the repo's base
// branch, then tears the worktree down. With no `branch` argument it
// operates on the active session (the reconcile counterpart to keep/remove);
// with `branch` it targets any live worktree under .evva/worktrees/, which
// works even when no session is active — e.g. merging a worktree a finished
// isolation:"worktree" subagent left behind.
//
// Safety contract: the merge runs from the base checkout (pulling the child
// branch in, never the reverse), refuses an unclean source, no-ops when
// there is nothing to integrate, and on conflict runs `git merge --abort`
// and reports the conflicted paths — the base branch is never left in a
// half-merged state.
func (t *ExitWorktreeTool) executeMerge(ctx context.Context, ctrl WorktreeController, logger *slog.Logger, in exitWorktreeInput) (tools.Result, error) {
	target := strings.TrimSpace(in.Branch)

	// --- resolve target: (childPath, childBranch) onto basePath ---
	var childPath, childBranch, basePath string
	var fromSession bool
	var sess *WorktreeSession

	if target == "" {
		sess = ctrl.WorktreeSession()
		if sess == nil {
			return tools.Result{
				IsError: true,
				Content: `exit_worktree: no active worktree session — pass "branch" to merge a specific worktree (run worktree_list to see candidates).`,
			}, nil
		}
		childPath, childBranch, basePath, fromSession = sess.Path, sess.Branch, sess.OriginalWorkdir, true
	} else {
		entries, err := parseWorktreeList(ctx, ctrl.Workdir())
		if err != nil {
			return tools.Result{IsError: true, Content: "exit_worktree: cannot enumerate worktrees: " + err.Error()}, nil
		}
		if len(entries) == 0 || !entries[0].isMain {
			return tools.Result{IsError: true, Content: "exit_worktree: cannot resolve the main worktree"}, nil
		}
		var match *worktreeEntry
		for i := range entries {
			e := &entries[i]
			if e.isMain || !isManagedWorktree(e.Path) {
				continue
			}
			if e.Branch == target || e.Branch == branchNameFor(target) {
				match = e
				break
			}
		}
		if match == nil {
			return tools.Result{
				IsError: true,
				Content: fmt.Sprintf("exit_worktree: no live worktree on branch %q.%s", target, worktreeBranchesHint(entries)),
			}, nil
		}
		childPath, childBranch, basePath = match.Path, match.Branch, entries[0].Path
	}

	// --- merge core (shared with the swarm's worktree_merge, SWT-1) ---
	// MergeBranch runs the guards (dirty base / unclean source), the no-op
	// check, and the `--no-ff` merge with the abort-on-conflict contract. It
	// deliberately does NOT tear the worktree down — the teardown below is the
	// tool's own concern (the swarm keeps the worktree for the member's reuse).
	report, mErr := MergeBranch(ctx, basePath, childBranch)
	if mErr != nil {
		return tools.Result{IsError: true, Content: "exit_worktree: " + mErr.Error()}, nil
	}
	baseBranch := report.BaseBranch
	if report.NoOp {
		return tools.Result{
			Content: fmt.Sprintf("No changes to integrate: branch %q has no commits beyond %q. Worktree left untouched at %s.", childBranch, baseBranch, childPath),
		}, nil
	}
	if len(report.Conflicts) > 0 {
		logger.Info("exit_worktree", "action", "merge", "branch", childBranch, "base", baseBranch, "result", "conflict")
		return tools.Result{
			// Not IsError: a conflict is an actionable outcome, not a tool failure.
			Content: fmt.Sprintf(
				"MERGE CONFLICT — aborted, nothing applied. Branch %q conflicts with %q in:\n%s\n\nThe worktree is intact at %s. Resolve by re-spawning the worker with conflict context, merging another branch first, or asking the user.",
				childBranch, baseBranch, bulletLines(strings.Join(report.Conflicts, "\n")), childPath,
			),
		}, nil
	}

	// --- success: tear the worktree down ---
	repoRoot, err := gitTopLevel(ctx, basePath)
	if err != nil {
		repoRoot = basePath
	}
	var teardown string
	if rmOut, rmErr := runGit(ctx, repoRoot, "worktree", "remove", "--force", childPath); rmErr != nil {
		teardown = fmt.Sprintf("\n(warning: worktree removal failed: %v: %s — remove it manually at %s)", rmErr, strings.TrimSpace(rmOut), childPath)
	} else if _, brErr := runGit(ctx, repoRoot, "branch", "-D", childBranch); brErr != nil {
		logger.Warn("exit_worktree merge: branch delete failed (worktree removed)", "branch", childBranch, "err", brErr)
	}

	// For the active-session form, restore the agent's workdir + end the
	// session (mirror keep/remove).
	if fromSession && sess != nil {
		if serr := ctrl.SwitchWorkdir(sess.OriginalWorkdir); serr != nil {
			teardown += "\n(warning: switch back to base workdir failed: " + serr.Error() + ")"
		}
		ctrl.EndWorktreeSession()
	}

	logger.Info("exit_worktree", "action", "merge", "branch", childBranch, "base", baseBranch, "commits", report.Ahead, "files", report.FilesChanged)
	return tools.Result{
		Content: fmt.Sprintf(
			"Merged %q into %q: %d commit(s), %d file(s) changed. Worktree removed: %s.%s",
			childBranch, baseBranch, report.Ahead, report.FilesChanged, childPath, teardown,
		),
	}, nil
}

// --- helpers -----------------------------------------------------------

// resolveSlug returns the validated (still slash-bearing) slug. Empty
// input generates a random one. The caller flattens with flattenSlug
// before composing the worktree path or branch name.
func resolveSlug(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return randomSlug(), nil
	}
	return validateSlug(name)
}

func randomSlug() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "wt-" + hex.EncodeToString(b[:])
}

// validateSlug enforces the same shape as the ref source:
// segments separated by "/", each segment matches [A-Za-z0-9._-]+,
// no "." or ".." segments, total length <= 64.
func validateSlug(slug string) (string, error) {
	if len(slug) > maxSlugLen {
		return "", fmt.Errorf("slug too long (max %d chars, got %d)", maxSlugLen, len(slug))
	}
	if strings.HasPrefix(slug, "/") || strings.HasSuffix(slug, "/") {
		return "", errors.New("slug must not begin or end with '/'")
	}
	parts := strings.Split(slug, "/")
	for _, p := range parts {
		if p == "" {
			return "", errors.New("slug must not contain empty segments")
		}
		if p == "." || p == ".." {
			return "", fmt.Errorf("slug segment %q is not allowed", p)
		}
		if !slugSegmentRE.MatchString(p) {
			return "", fmt.Errorf("slug segment %q contains invalid characters (allowed: letters, digits, dot, underscore, dash)", p)
		}
	}
	return slug, nil
}

// flattenSlug collapses "/" separators to "+" so a slash-bearing slug
// names a single directory and a single git ref. Ref source uses the
// same flattening character.
func flattenSlug(slug string) string {
	return strings.ReplaceAll(slug, "/", "+")
}

func branchNameFor(flatSlug string) string {
	return "worktree-" + flatSlug
}

func worktreeDirFor(repoRoot, flatSlug string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(permission.WorktreeDirSegment), flatSlug)
}

// gitTopLevel returns the canonical repository root for `dir`. Used to
// anchor worktrees at the main repo even when EnterWorktree is invoked
// from a nested directory.
func gitTopLevel(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runGit invokes `git <args...>` with cmd.Dir = dir and returns the
// combined stdout+stderr output. Bypasses the Bash tool deliberately:
// these are internal tool side effects (mirroring how EnterPlanMode
// writes its plan file via os.WriteFile), not user-issued shell.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// worktreeUncommitted returns the count of uncommitted working-tree changes
// in the worktree at path (0 ⇒ everything is committed). This is the signal
// the merge action's source-clean guard wants: committed work is fine to
// merge; only uncommitted work blocks it.
func worktreeUncommitted(ctx context.Context, path string) (int, error) {
	out, err := runGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return 0, nil
	}
	return strings.Count(out, "\n") + 1, nil
}

// worktreeCommitsAhead counts commits on the worktree's branch that are not
// reachable from the repo's base branch (the branch checked out in the
// primary worktree). Best-effort: returns 0 if the base can't be resolved, so
// a clean worktree with no base context never blocks auto-cleanup.
func worktreeCommitsAhead(ctx context.Context, path string) int {
	entries, err := parseWorktreeList(ctx, path)
	if err != nil || len(entries) == 0 || entries[0].Branch == "" {
		return 0
	}
	ahead, _, aErr := aheadBehind(ctx, path, entries[0].Branch, "HEAD")
	if aErr != nil {
		return 0
	}
	return ahead
}

// worktreeHasChanges returns (true, summary) when the worktree at `path` holds
// work that would be lost if it were removed: uncommitted files OR commits on
// its branch beyond the base branch. This is the auto-cleanup / discard guard
// (so a worker that COMMITTED its slice is preserved for later merge, not
// silently dropped). Fail-closed: any git error treats the worktree as dirty
// so we never auto-remove a worktree we can't inspect.
func worktreeHasChanges(ctx context.Context, path string) (bool, string) {
	uncommitted, err := worktreeUncommitted(ctx, path)
	if err != nil {
		return true, "could not inspect worktree status: " + err.Error()
	}
	commitsAhead := worktreeCommitsAhead(ctx, path)
	if uncommitted == 0 && commitsAhead == 0 {
		return false, ""
	}
	return true, fmt.Sprintf(
		"%d file(s) uncommitted, %d commit(s) beyond base branch",
		uncommitted, commitsAhead,
	)
}

// --- AgentTool isolation helpers --------------------------------------

// CreateForSubagent provisions a worktree for an AgentTool isolation
// spawn. It is callable from internal/agent/spawn.go (no controller
// involved — the child agent's workdir is fixed at construction).
// Returns the worktree session metadata so spawn.go can:
//   - construct the child with cfg.WorkDir = session.Path
//   - tear down the worktree after the child exits (clean-exit path) or
//     surface its path back to the parent (dirty-exit path).
//
// agentName is folded into the slug so panel rows / logs / branch names
// stay readable; if the child name is empty, a random suffix is used.
func CreateForSubagent(ctx context.Context, parentWorkdir, agentName string) (WorktreeSession, error) {
	if parentWorkdir == "" {
		return WorktreeSession{}, errors.New("parent workdir is empty")
	}
	repoRoot, err := gitTopLevel(ctx, parentWorkdir)
	if err != nil {
		return WorktreeSession{}, fmt.Errorf("not in a git repository: %w", err)
	}

	base := sanitizeForSlug(agentName)
	if base == "" {
		base = "agent"
	}
	// Append a short random suffix so concurrent isolation spawns with the
	// same agent name don't collide on directory or branch name.
	var b [3]byte
	_, _ = rand.Read(b[:])
	flat := base + "-" + hex.EncodeToString(b[:])
	if len(flat) > maxSlugLen {
		flat = flat[:maxSlugLen]
	}

	worktreePath := worktreeDirFor(repoRoot, flat)
	branch := branchNameFor(flat)

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return WorktreeSession{}, fmt.Errorf("create worktree parent dir: %w", err)
	}
	if out, gErr := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, worktreePath, "HEAD"); gErr != nil {
		return WorktreeSession{}, fmt.Errorf("git worktree add: %v: %s", gErr, out)
	}

	return WorktreeSession{
		OriginalWorkdir:   parentWorkdir,
		Path:              worktreePath,
		Branch:            branch,
		Slug:              flat,
		CreatedBySubagent: true,
		CreatedAt:         time.Now(),
	}, nil
}

// CleanupSubagentWorktree tears down a worktree created by
// CreateForSubagent. If the worktree has any uncommitted changes the
// caller can decide to keep it (return the path to the user) instead of
// removing it. RemoveAlways=true forces removal regardless.
//
// Returns (wasRemoved, summary). On wasRemoved=false the worktree is
// still on disk; summary describes why (e.g. "had 3 uncommitted file(s)").
func CleanupSubagentWorktree(ctx context.Context, sess WorktreeSession, removeAlways bool) (bool, string) {
	if !sess.CreatedBySubagent || sess.Path == "" {
		return false, "no subagent worktree to clean up"
	}
	dirty, summary := worktreeHasChanges(ctx, sess.Path)
	if dirty && !removeAlways {
		return false, summary
	}
	repoRoot, err := gitTopLevel(ctx, sess.OriginalWorkdir)
	if err != nil {
		return false, "cannot resolve repo root: " + err.Error()
	}
	if out, rmErr := runGit(ctx, repoRoot, "worktree", "remove", "--force", sess.Path); rmErr != nil {
		return false, fmt.Sprintf("git worktree remove failed: %v: %s", rmErr, out)
	}
	// Best-effort branch delete; ignore failures so cleanup never
	// half-completes.
	_, _ = runGit(ctx, repoRoot, "branch", "-D", sess.Branch)
	return true, "removed"
}

// sanitizeForSlug strips a free-form string down to characters allowed
// in a slug segment. Used to fold an agent name into the worktree
// directory / branch name.
func sanitizeForSlug(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	out = strings.Trim(out, "-")
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
