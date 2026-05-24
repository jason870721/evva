# fs Edit/Write Gate — Ref Parity & Partial-Read Fix — Development Plan

> **Status:** Implemented (2026-05-24) — all parts landed; `go test ./pkg/tools/fs/...` green, `go vet` clean.
> **Date:** 2026-05-24
> **Author:** evva (coding agent)
> **Target:** evva v0.2.x (current branch)
> **Scope:** `pkg/tools/fs/{edit,write,read,tracker,fs}.go`

---

## Table of Contents

1. [Background](#1-background)
2. [Root Cause: the partial-view block](#2-root-cause-the-partial-view-block)
3. [Decisions](#3-decisions)
4. [Implementation Plan](#4-implementation-plan)
5. [Out of Scope](#5-out-of-scope)
6. [Sequencing & Test Strategy](#6-sequencing--test-strategy)

---

## 1. Background

evva's write-allow judgment splits across two layers:

- **Layer A — policy/safety gate** (`pkg/permission/Decide`, runs in the agent loop, outside the tool): mode + rule based.
- **Layer B — correctness pre-flight** (`EditTool.Execute` / `WriteTool.Execute`, via `ReadTracker`): "is this edit well-formed and safe to apply against current disk state."

This plan addresses **Layer B** only. It (a) fixes a divergence where evva is *stricter* than ref and blocks the agent in normal operation, and (b) ports the Layer-B items evva is currently missing relative to `ref/src/tools/FileEditTool`.

## 2. Root Cause: the partial-view block

**Symptom:** `edit: File was only partially read (offset/limit). Re-read the full file before editing.` fires constantly on real files.

**Why evva triggers it:**

- `read.go:242` marks a read `partial` whenever it didn't reach EOF *or* an explicit offset was given:
  ```go
  partial := explicitOffset || endIdx < totalLines
  ```
- `tracker.go:107` (`CanEdit`) then **hard-blocks** any edit on a partial entry.
- Compounding factors:
  - Default no-arg read caps at **2000 lines** (`read.go:234`), so *any file > 2000 lines* read normally is flagged partial → un-editable.
  - `explicitOffset` alone marks partial, so even `read(offset=1, limit=9999)` covering the whole file is flagged partial.
  - The only non-partial path for a large file is `no offset + limit ≥ totalLines`; re-reading without that re-truncates and re-blocks → the agent loops.

**How ref avoids it:**

- ref's reader stores `offset`/`limit` but **never sets `isPartialView`** on a normal read (`FileReadTool.ts:1032`). The flag is set in exactly one place — synthetic memory-file attachments whose in-context content differs from disk (`attachments.ts:1749`).
- The edit gate only blocks on *never-read* or that narrow `isPartialView` (`FileEditTool.ts:275`), so truncated and explicit-range reads still permit edits.
- ref also reads the **whole file by default** (`limit === undefined → maxSizeBytes`, `FileReadTool.ts:1026`); no 2000-line cap.
- **Safety model:** the edit re-reads the full file at apply time and requires `old_string` to match uniquely (`FileEditTool.ts:316,332`). "Did the model see the whole file" is *not* a gate.

evva's `edit.go` already re-reads the full file (`edit.go:134`) and enforces unique-match (`edit.go:166,180`), so the partial-block adds nothing but friction.

## 3. Decisions

| # | Decision | Choice |
|---|----------|--------|
| D1 | Partial-read gate | **Match ref exactly** — drop the `IsPartialView` block; rely on unique `old_string` match. Keep evva's 2000-line default read cap (intentional context-economy divergence). |
| D2 | Scope of missing Layer-B items | **All four:** file-size cap on edit, TOCTOU re-stat before write, staleness content-fallback, UNC-path guard. |

## 4. Implementation Plan

### Part 0 — Drop the partial-view over-block (D1)

- `tracker.go` — remove the `IsPartialView` branch from `CanEdit` (`tracker.go:107-109`) and therefore `CanWrite` (`tracker.go:119-121`). Gate keeps: *never-read* → block; *mtime advanced* → block (subject to Part 3).
- `read.go:242` — stop computing/recording `partial` for gating purposes. `IsPartialView` is no longer load-bearing; either drop the field or retain it solely for the synthetic-content case (none today). Simplest: always `RecordRead(..., false)` for real reads.
- Update tool descriptions: remove the "partial-view (offset/limit) → re-read" sentence from `edit.go:51` and `write.go:31`.
- **Safety unchanged:** `edit.go` re-reads full content and enforces unique match; multi-match still requires `replace_all`.

### Part 1 — File-size cap on edit

- Mirror ref `MAX_EDIT_FILE_SIZE` (`FileEditTool.ts:84,186-200`). Define `const MaxEditFileSize = 1 << 30 // 1 GiB`.
- In `edit.go`, after the stat (`edit.go:103`) and before `readFileWithEncoding` (`edit.go:134`): if `info.Size() > MaxEditFileSize`, return an `IsError` result explaining the cap. Prevents OOM from reading a multi-GB file into memory.
- Scope to edit only (ref does the same); `write` overwrites and its size is bounded by model output.

### Part 2 — TOCTOU re-stat before write

- ref re-checks mtime in the atomic section right before writing (`FileEditTool.ts:451-468`).
- **Implemented as** `fileChangedSince(path, baseline)` in `fs.go`, called immediately before each `writeFileWithEncoding` (edit main path, edit empty-file path, write). Aborts with a "modified … mid-edit/mid-write, re-read" error if the file's mtime advanced past the initial stat. Extracting the helper (rather than inlining the re-stat) keeps it deterministically unit-testable without threading a test-seam through `Execute`.
- Low-probability with a single-threaded agent (only user/external interference in a sub-millisecond window), but cheap and matches ref.

### Part 3 — Staleness content-fallback

- ref skips the "modified since read" error when, for a *full* read, current content equals the stored content (`FileEditTool.ts:296-309`) — absorbs touch/formatter/cloud-sync false positives.
- **Implemented as** `ContentHash [32]byte` (SHA-256) on `readEntry`, computed by the exported `HashContent(string)` helper. Because evva's reader loads the whole file into memory even for a truncated view (`read.go` slices for display only), the hash always covers the **full** file content — so the fallback works even after a partial read (better than ref, which only has the slice). Storing a hash rather than ref's full content bounds memory — a deliberate evva divergence. PDF/notebook reads store a zero hash (their view isn't the raw bytes), which disables the fallback for those entries.
- `Record`/`RecordRead` take the hash; `CanEdit`/`CanWrite` take the current content hash and, when mtime advanced, allow the edit iff the stored hash is non-zero and equals it.
- `CanEdit` — when mtime advanced **and** the entry has a full-content hash **and** the current full-file content hashes equal → allow. Otherwise keep blocking. Edit already reads current content at `edit.go:134`, so the comparison is local.

### Part 4 — UNC-path guard

- ref short-circuits `\\` / `//` paths to avoid NTLM credential leaks (`FileEditTool.ts:179-181`).
- `fs.go` `resolvePath` (`fs.go:36`) — reject raw `pathStr` beginning with `\\` or `//` *before* `expandHome`/`Clean` (Clean collapses `//`). Covers read/edit/write/glob uniformly.
- Windows-only relevance; harmless and trivial on darwin.

## 5. Out of Scope

- **Team-memory secret guard** (`checkTeamMemSecrets`, `FileEditTool.ts:144-147`) — evva has no team-memory-sync feature; nothing to port.
- **Settings-schema validation** (`validateEditTool.ts`) — only meaningful if evva wants to protect its own `.evva/*.json` configs from corruption; speculative, deferred.
- **Layer A** (dangerous-file/dir lists, working-dir containment, symlink dual-path checks, acceptEdits path safety) — tracked separately; not part of this Layer-B plan.

## 6. Sequencing & Test Strategy

Order: **Part 0 → 1 → 4 → 2 → 3** (impact-first; Part 3 is the most code).

Tests (extend `edit_test.go`, `write_test.go`, `tracker_test.go`, `read_test.go`):

- **Part 0:** `TestReadTracker_PartialViewAllowed`, `TestEdit_AllowedAfterPartialView`, `TestWrite_OverwriteAllowedAfterPartialView` — editing/overwriting after a partial-view read now succeeds. Multi-match without `replace_all` still errors (existing tests).
- **Part 1:** `TestEdit_RejectsOversizeFile` — sparse file (`Truncate` to 1 GiB+1) → "too large", asserted to fire before the read-tracker check.
- **Part 2:** `TestFileChangedSince` — unchanged file → false; advanced mtime → true; missing file → false.
- **Part 3:** `TestReadTracker_MtimeDriftSameContentAllowed` / `…ChangedContentRejected` / `…ZeroHashRejected`, plus end-to-end `TestEdit_AllowedOnMtimeDriftSameContent` and the updated `TestEdit_BlockedOnMtimeDrift` (now changes bytes so the drift is real).
- **Part 4:** `TestResolvePathRejectsUNC` — `//server/share` and `\\server\share` rejected.

**Result:** `go test ./pkg/tools/fs/...` green, `go vet` clean, `gopls` diagnostics clean on changed files. (Two failures in `internal/agent/sysprompt` are pre-existing and unrelated — they fail on the base branch too.)
