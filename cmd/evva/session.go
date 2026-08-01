// The session lifecycle from outside a running evva: `evva resume`,
// `evva sessions list|prune`, `evva export`.
//
// Inside evva, /resume already reaches every past conversation. From a
// fresh terminal there was no way in at all — that asymmetry is what these
// three subcommands close.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/common"
	config "github.com/johnny1110/evva/pkg/config"
)

// runResume implements `evva resume [-fork] [-all] [id] [evva flags...]`.
//
// With an id (or a unique prefix of one) it thaws that session; with none
// it shows a numbered picker and reads a choice. Either way it hands off to
// the normal bootstrap, which is why resume's OWN flags go before the id
// and everything after it belongs to `evva` proper:
//
//	evva resume -fork 4cafec5d -permission-mode plan
func runResume(args []string) {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	fork := fs.Bool("fork", false, "branch the session instead of continuing it — the original stays untouched")
	all := fs.Bool("all", false, "pick from every workdir, not just this one")
	_ = fs.Parse(args)

	cfg := config.Get()
	rest := fs.Args()

	var id string
	if len(rest) > 0 {
		var err error
		if id, err = resolveSessionRef(cfg, rest[0]); err != nil {
			exitf(1, "evva resume: %v", err)
		}
		rest = rest[1:]
	} else {
		var err error
		if id, err = pickSession(cfg, *all); err != nil {
			exitf(1, "evva resume: %v", err)
		}
		if id == "" {
			return // the operator declined the picker
		}
	}

	if *fork {
		newID, err := forkOnDisk(cfg, id)
		if err != nil {
			exitf(1, "evva resume -fork: %v", err)
		}
		fmt.Fprintf(os.Stderr, "evva: forked %s → %s\n", shortID(id), shortID(newID))
		id = newID
	}

	// Stand in the session's own directory before the agent is built, so it
	// is BORN in the right place — cfg.WorkDir is read during construction
	// and the tools bind to it. Switching afterwards would work but would
	// leave the banner and the first memory load describing the directory
	// the operator happened to launch from.
	if h, ok := findHeader(cfg, id); ok && h.Workdir != "" {
		if err := chdirTo(cfg, h.Workdir); err != nil {
			fmt.Fprintf(os.Stderr, "evva: %v — resuming in %s instead\n", err, cfg.WorkDir)
		}
	}

	bootstrap(rest, id)
}

// runSessions implements `evva sessions [list|prune]`.
func runSessions(args []string) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "ls":
		sessionsList(args)
	case "prune", "gc":
		sessionsPrune(args)
	default:
		exitf(2, "usage: evva sessions [list|prune] [flags]")
	}
}

func sessionsList(args []string) {
	fs := flag.NewFlagSet("sessions list", flag.ExitOnError)
	all := fs.Bool("all", false, "every workdir, not just this one")
	_ = fs.Parse(args)

	cfg := config.Get()
	headers, warnings, err := listSessions(cfg, *all)
	if err != nil {
		exitf(1, "evva sessions: %v", err)
	}
	if len(headers) == 0 {
		fmt.Println("no saved sessions" + scopeSuffix(cfg, *all))
		return
	}
	for _, h := range headers {
		mark := " "
		if h.Pinned {
			mark = "*"
		}
		fork := ""
		if h.ParentID != "" {
			fork = " ⑂" + shortID(h.ParentID)
		}
		where := ""
		if *all {
			where = "  " + h.Workdir
		}
		fmt.Printf("%s %-10s %s  %3d msgs  %s%s%s\n",
			mark, shortID(h.SessionID), time.Unix(0, h.MTime).Format("2006-01-02 15:04"),
			h.MessageCount, truncateLine(h.Label(), 52), fork, where)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "!", w)
	}
	fmt.Fprintln(os.Stderr, "\n* = pinned (never pruned) · ⑂ = forked from")
}

func sessionsPrune(args []string) {
	fs := flag.NewFlagSet("sessions prune", flag.ExitOnError)
	days := fs.Int("days", 0, "delete sessions untouched for more than N days (0 = no age limit)")
	keep := fs.Int("keep", 0, "keep only the N newest sessions per workdir (0 = no count limit)")
	all := fs.Bool("all", false, "prune every workdir, not just this one")
	apply := fs.Bool("apply", false, "actually delete — without this, prune only reports what it would remove")
	_ = fs.Parse(args)

	cfg := config.Get()
	ret := session.Retention{MaxCount: *keep}
	if *days > 0 {
		ret.MaxAge = time.Duration(*days) * 24 * time.Hour
	}
	// Config supplies the caps when the flags do not, so an operator who set
	// them once can just run `evva sessions prune -apply`.
	if ret.Empty() {
		ret = session.Retention{
			MaxCount: cfg.GetSessionRetentionMax(),
			MaxAge:   time.Duration(cfg.GetSessionRetentionDays()) * 24 * time.Hour,
		}
	}
	if ret.Empty() {
		fmt.Println("no retention policy: pass -days and/or -keep, or set session_retention_days / session_retention_max in /config")
		return
	}

	headers, _, err := listSessions(cfg, *all)
	if err != nil {
		exitf(1, "evva sessions prune: %v", err)
	}
	victims := session.SelectExpired(headers, ret, nil, time.Now())
	if len(victims) == 0 {
		fmt.Printf("nothing to prune under keep=%d days=%d%s\n", ret.MaxCount, int(ret.MaxAge.Hours()/24), scopeSuffix(cfg, *all))
		return
	}

	var bytes int64
	for _, h := range victims {
		fmt.Printf("  %-10s %s  %3d msgs  %s\n", shortID(h.SessionID),
			time.Unix(0, h.MTime).Format("2006-01-02 15:04"), h.MessageCount, truncateLine(h.Label(), 52))
		if fi, serr := os.Stat(session.SessionFilePath(cfg.AppHome, h.WorkdirSlug, h.SessionID)); serr == nil {
			bytes += fi.Size()
		}
	}
	if !*apply {
		fmt.Printf("\n%d session(s), %.1f KB — dry run. Re-run with -apply to delete.\n", len(victims), float64(bytes)/1024)
		return
	}
	n, err := session.DeleteAll(cfg.AppHome, victims)
	fmt.Printf("\ndeleted %d session(s), %.1f KB\n", n, float64(bytes)/1024)
	if err != nil {
		exitf(1, "evva sessions prune: %v", err)
	}
}

// runExport implements `evva export <id> [-o out.html] [-full]`.
func runExport(args []string) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: <short-id>.html in the current directory)")
	full := fs.Bool("full", false, "keep tool results at full length instead of truncating them")
	rest := parseAroundPositional(fs, args)

	if len(rest) == 0 {
		exitf(2, "usage: evva export <session-id> [-o out.html] [-full]  (ids: evva sessions list)")
	}
	cfg := config.Get()
	id, err := resolveSessionRef(cfg, rest[0])
	if err != nil {
		exitf(1, "evva export: %v", err)
	}
	h, ok := findHeader(cfg, id)
	if !ok {
		exitf(1, "evva export: no session %q", rest[0])
	}
	snap, err := session.Load(cfg.AppHome, h.WorkdirSlug, id)
	if err != nil {
		exitf(1, "evva export: %v", err)
	}

	path := *out
	if path == "" {
		path = "evva-" + shortID(id) + ".html"
	}
	f, err := os.Create(path)
	if err != nil {
		exitf(1, "evva export: %v", err)
	}
	masked, err := session.ExportHTML(f, snap, session.ExportOptions{Full: *full})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		exitf(1, "evva export: %v", err)
	}
	abs, _ := filepath.Abs(path)
	fmt.Printf("Wrote %s — %d message(s)", abs, len(snap.Session.Messages))
	if masked > 0 {
		fmt.Printf(", %d secret(s) masked", masked)
	}
	fmt.Println(".")
	if !*full {
		fmt.Println("Tool results are truncated; re-export with -full for the complete record.")
	}
}

// --- shared helpers -------------------------------------------------------

// parseAroundPositional parses fs when the positional argument may come
// either before or after the flags.
//
// Go's flag package stops at the first non-flag token, so
// `evva export <id> -o out.html` would parse zero flags and silently write
// to the default filename — the id is exactly the thing an operator types
// first. One re-parse of the tail fixes it.
func parseAroundPositional(fs *flag.FlagSet, args []string) []string {
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		return nil
	}
	head := rest[0]
	_ = fs.Parse(rest[1:])
	return append([]string{head}, fs.Args()...)
}

// listSessions returns this workdir's sessions, or every workdir's.
func listSessions(cfg *config.Config, all bool) ([]session.Header, []string, error) {
	if all {
		return session.ListAll(cfg.AppHome)
	}
	return session.List(cfg.AppHome, memdir.ProjectKey(cfg.WorkDir))
}

func scopeSuffix(cfg *config.Config, all bool) string {
	if all {
		return " on this machine"
	}
	return " in " + cfg.WorkDir
}

// mostRecentSession backs -continue: the newest session in this directory,
// or "" when there is none. Absence is not an error — starting fresh is a
// perfectly good outcome for `evva -c` in a new project.
func mostRecentSession(cfg *config.Config) (string, error) {
	headers, _, err := session.List(cfg.AppHome, memdir.ProjectKey(cfg.WorkDir))
	if err != nil || len(headers) == 0 {
		return "", err
	}
	return headers[0].SessionID, nil
}

// resolveSessionRef turns what the operator typed into a session id.
//
// Accepts a full id or any unique prefix. evva's ids are UUIDs, which no
// one is going to retype from a listing — a prefix keeps the ids stable
// (they name files, and the swarm keys transcripts by them) while making
// them usable, which is the PRD's short-id question answered without
// minting a second identifier.
func resolveSessionRef(cfg *config.Config, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty session id")
	}
	headers, _, err := session.ListAll(cfg.AppHome)
	if err != nil {
		return "", err
	}
	var matches []session.Header
	for _, h := range headers {
		if h.SessionID == ref {
			return ref, nil
		}
		if strings.HasPrefix(h.SessionID, ref) {
			matches = append(matches, h)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching %q (list them with `evva sessions list -all`)", ref)
	case 1:
		return matches[0].SessionID, nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, shortID(m.SessionID))
		}
		sort.Strings(ids)
		return "", fmt.Errorf("%q matches %d sessions (%s) — use more characters", ref, len(matches), strings.Join(ids, ", "))
	}
}

func findHeader(cfg *config.Config, id string) (session.Header, bool) {
	headers, _, err := session.ListAll(cfg.AppHome)
	if err != nil {
		return session.Header{}, false
	}
	for _, h := range headers {
		if h.SessionID == id {
			return h, true
		}
	}
	return session.Header{}, false
}

// pickSession prints a numbered list and reads a choice.
//
// The list goes to stdout and the prompt to stderr so `evva resume < /dev/null`
// still shows the operator what exists before giving up.
func pickSession(cfg *config.Config, all bool) (string, error) {
	headers, _, err := listSessions(cfg, all)
	if err != nil {
		return "", err
	}
	if len(headers) == 0 {
		if !all {
			return "", fmt.Errorf("no saved sessions in %s (try -all)", cfg.WorkDir)
		}
		return "", fmt.Errorf("no saved sessions on this machine")
	}
	if len(headers) > 20 {
		headers = headers[:20]
	}
	for i, h := range headers {
		mark := " "
		if h.Pinned {
			mark = "*"
		}
		fmt.Printf("%2d.%s %s  %3d msgs  %s\n", i+1, mark,
			time.Unix(0, h.MTime).Format("2006-01-02 15:04"), h.MessageCount, truncateLine(h.Label(), 60))
	}
	fmt.Fprintf(os.Stderr, "\nresume which? [1-%d, Enter to cancel] ", len(headers))

	line := promptFor("")
	if line == "" {
		return "", nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(headers) {
		return "", fmt.Errorf("not a listed choice: %q", line)
	}
	return headers[n-1].SessionID, nil
}

// forkOnDisk branches a session without loading an agent — the `-fork`
// half of `evva resume`. The parent file is read and never written.
func forkOnDisk(cfg *config.Config, parentID string) (string, error) {
	h, ok := findHeader(cfg, parentID)
	if !ok {
		return "", fmt.Errorf("no session %q", parentID)
	}
	snap, err := session.Load(cfg.AppHome, h.WorkdirSlug, parentID)
	if err != nil {
		return "", err
	}
	child := *snap
	child.Meta = session.ForkMeta(snap.Meta, common.GenUUID(), len(snap.Session.Messages), time.Now().UTC())
	if err := session.Save(cfg.AppHome, &child); err != nil {
		return "", err
	}
	return child.SessionID, nil
}

// chdirTo moves the process — and the config every downstream component
// reads — into dir.
func chdirTo(cfg *config.Config, dir string) error {
	if dir == "" || dir == cfg.WorkDir {
		return nil
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("cannot enter %s: %w", dir, err)
	}
	cfg.WorkDir = dir
	return nil
}

// shortID trims a UUID to the prefix the listings show and `resume` accepts.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
