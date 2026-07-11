package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/doctor"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/config"
)

// runSwarm dispatches the swarm control-plane CLI — thin authenticated HTTP
// clients against the running `evva service` (process model A: the service
// builds the agents, the CLI only POSTs intent). Spaces are Docker-style: a
// stable id plus a unique human NAME (handle); every command below that takes a
// <ref> accepts either the id or the name.
//
//	evva swarm . [--name <n>]   register ./evva-swarm.yml as a new space
//	evva swarm ls               list spaces (running + stopped)
//	evva swarm run <ref>        (re)start a stopped space
//	evva swarm stop <ref>       stop a space but keep it (run restarts it)
//	evva swarm rm <ref>         forget a space entirely
//	evva swarm reset <ref>      wipe a space (fresh ledger + cleared context), same id
//	evva swarm add <ref> <m>    hot-load member <m> into a space (M3)
//	evva swarm vacuum <ref>     archive-then-delete consumed history (RP-16)
//	evva swarm send <ref> <m> <text|->  message a member as the operator (RP-27)
//
// The bare `evva` (TUI) path is untouched.
func runSwarm(args []string) {
	// A --name <value> flag may appear anywhere; it is consumed by `.`.
	name, args := extractNameFlag(args)
	// vacuum's flags, likewise position-independent.
	days, dryRun, args := extractVacuumFlags(args)
	// doctor's flags, likewise.
	strict, offline, jsonOut, args := extractDoctorFlags(args)

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	var err error
	switch sub {
	case "", "help", "-h", "-help", "--help":
		// `evva swarm help` prints to stdout (exit 0); a bare `evva swarm` with no
		// subcommand prints the same reference to stderr and exits non-zero.
		if sub == "" {
			swarmHelp(os.Stderr)
			os.Exit(2)
		}
		swarmHelp(os.Stdout)
		return
	case ".":
		err = swarmRegister(os.Stdout, name)
	case "ls":
		err = swarmLs(os.Stdout)
	case "run":
		if len(args) < 2 {
			exitf(2, "usage: evva swarm run <ref>")
		}
		err = swarmRun(os.Stdout, args[1])
	case "stop":
		if len(args) < 2 {
			exitf(2, "usage: evva swarm stop <ref>")
		}
		err = swarmStop(os.Stdout, args[1])
	case "rm":
		if len(args) < 2 {
			exitf(2, "usage: evva swarm rm <ref>")
		}
		err = swarmRm(os.Stdout, args[1])
	case "reset":
		if len(args) < 2 {
			exitf(2, "usage: evva swarm reset <ref>")
		}
		err = swarmReset(os.Stdout, args[1])
	case "add":
		if len(args) < 3 {
			exitf(2, "usage: evva swarm add <ref> <member>")
		}
		err = swarmAdd(os.Stdout, args[1], args[2])
	case "vacuum":
		if len(args) < 2 {
			exitf(2, "usage: evva swarm vacuum <ref> [--days N] [--dry-run]")
		}
		err = swarmVacuum(os.Stdout, args[1], days, dryRun)
	case "send":
		if len(args) < 4 {
			exitf(2, "usage: evva swarm send <ref> <member> <text> (use - to read the text from stdin)")
		}
		err = swarmSend(os.Stdout, args[1], args[2], args[3])
	case "doctor":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		swarmDoctor(dir, strict, offline, jsonOut) // exits itself (0/1/2)
		return
	default:
		exitf(2, "evva swarm: unknown subcommand %q — run `evva swarm help`", sub)
	}
	if err != nil {
		exitf(1, "evva swarm %s: %v", sub, err)
	}
}

// swarmHelp prints the full swarm CLI reference.
func swarmHelp(w io.Writer) {
	fmt.Fprint(w, `evva swarm — drive a multi-agent swarm workstation (talks to the running evva service)

Usage:
  evva swarm <command> [arguments]

Commands:
  .                  register ./evva-swarm.yml as a new space (and start it)
  ls                 list spaces — running and stopped (like docker ps -a)
  run   <ref>        (re)start a stopped space, under its same id / URL
  stop  <ref>        stop a space but KEEP it (run restarts it)
  rm    <ref>        forget a space entirely (its workdir data is left intact)
  reset <ref>        wipe a space — fresh ledger + cleared agent context, same id
  add   <ref> <m>    hot-load member <m> into a space
  vacuum <ref>       archive-then-delete consumed history (read mail + completed
                     tasks older than the retention window) into .vero/archive/
  send  <ref> <m> <text>
                     message member <m> as the operator (sender "user" — same as
                     the web composer). <text> of - reads the body from stdin.
                     <m> may also be "leader" (the role resolves to the member)
  doctor [dir]       read-only preflight: will register work here, and will the
                     members actually run? Checks manifest → member definitions →
                     models/efforts → provider keys → .vero state → the service.
                     Never writes, never migrates, never registers.
  help               show this help

Flags:
  --name <name>      with '.', name the new space; otherwise the manifest's
                     name: is used, or a handle is generated (e.g. swift-otter)
  --days <n>         with 'vacuum', override the retention window (default: the
                     space's settings.retention_days, or 30)
  --dry-run          with 'vacuum', only report what would be cleared
  --strict           with 'doctor', promote warnings (custom models, skews) to
                     errors; exit 2 when only promoted warnings remain
  --offline          with 'doctor', skip the service probes — never dials
  --json             with 'doctor', emit findings as a JSON array (CI use)

<ref> is a space id OR its name (the NAME column of 'evva swarm ls').
Start the service first with 'evva service start'.
`)
}

// extractVacuumFlags pulls `--days <n>` (or `--days=n`) and `--dry-run` out of
// args from any position, returning the leftovers. A non-numeric --days exits
// with usage, matching the other arg-validation paths.
func extractVacuumFlags(args []string) (days int, dryRun bool, rest []string) {
	rest = make([]string, 0, len(args))
	take := func(v string) {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			exitf(2, "evva swarm vacuum: --days wants a non-negative whole number, got %q", v)
		}
		days = n
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--days" && i+1 < len(args):
			take(args[i+1])
			i++
		case strings.HasPrefix(a, "--days="):
			take(strings.TrimPrefix(a, "--days="))
		case a == "--dry-run":
			dryRun = true
		default:
			rest = append(rest, a)
		}
	}
	return days, dryRun, rest
}

// extractDoctorFlags pulls doctor's flags out of args from any position.
func extractDoctorFlags(args []string) (strict, offline, jsonOut bool, rest []string) {
	rest = make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--strict":
			strict = true
		case "--offline":
			offline = true
		case "--json":
			jsonOut = true
		default:
			rest = append(rest, a)
		}
	}
	return strict, offline, jsonOut, rest
}

// swarmDoctor runs the read-only preflight (DR) and exits with its
// deterministic code: 0 clean, 1 any ✗, 2 when --strict promoted warnings
// and nothing failed outright. Everything except the service section works
// with no daemon; --offline skips that section entirely (doctor then never
// dials at all).
func swarmDoctor(dir string, strict, offline, jsonOut bool) {
	opts := doctor.Options{Dir: dir, Strict: strict, Config: config.Get()}
	if !offline {
		version := config.Version // ldflags-injected tag on release builds
		if version == "" {
			version = config.DefaultAppVersion
		}
		opts.Service = &doctor.ServiceTarget{Addr: targetAddr(), Token: readToken(), Version: version}
	}
	rep := doctor.Run(opts)
	if jsonOut {
		if err := rep.JSON(os.Stdout); err != nil {
			exitf(1, "evva swarm doctor: %v", err)
		}
	} else {
		rep.Render(os.Stdout)
	}
	os.Exit(rep.Exit())
}

// extractNameFlag pulls a `--name <value>` (or `--name=value`) flag out of args
// from any position, returning the value and the remaining positional args.
func extractNameFlag(args []string) (string, []string) {
	name := ""
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--name" && i+1 < len(args):
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		default:
			out = append(out, a)
		}
	}
	return name, out
}

// swarmRegister POSTs the current workdir to the service to bring up a space. It
// validates the manifest locally first so a typo fails fast with a clear message
// instead of a 400 from the server. name is the optional --name override; when
// empty the service falls back to the manifest's name, else a generated handle.
func swarmRegister(out io.Writer, name string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(wd)
	if err != nil {
		return err
	}
	manifest := filepath.Join(abs, "evva-swarm.yml")
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("no evva-swarm.yml in %s", abs)
	}
	if _, err := agentdef.LoadManifest(manifest); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}

	body := map[string]string{"workdir": abs}
	if name != "" {
		body["name"] = name
	}
	var reply struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := serviceClient("POST", "/api/swarms", body, &reply); err != nil {
		return err
	}
	label := reply.Name
	if label == "" {
		label = reply.ID
	}
	fmt.Fprintf(out, "registered space %s (id %s)\n", label, reply.ID)
	fmt.Fprintf(out, "  open: http://%s/?space=%s\n", targetAddr(), reply.ID)
	return nil
}

// swarmLs prints the space table — running AND stopped (like docker ps -a) — with
// the NAME (the handle) first.
func swarmLs(out io.Writer) error {
	var spaces []webapi.SpaceInfo
	if err := serviceClient("GET", "/api/swarms", nil, &spaces); err != nil {
		return err
	}
	if len(spaces) == 0 {
		fmt.Fprintln(out, "no spaces registered")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tMEMBERS\tID\tWORKDIR")
	for _, s := range spaces {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", s.Name, s.Status, s.Members, s.ID, s.Workdir)
	}
	return tw.Flush()
}

// swarmRun (re)starts a stopped space by id or name, rebuilt under its same id.
func swarmRun(out io.Writer, ref string) error {
	var reply struct {
		ID string `json:"id"`
	}
	if err := serviceClient("POST", "/api/swarm/"+ref+"/run", nil, &reply); err != nil {
		return err
	}
	fmt.Fprintf(out, "started space %s\n", ref)
	fmt.Fprintf(out, "  open: http://%s/?space=%s\n", targetAddr(), reply.ID)
	return nil
}

// swarmStop stops a space (by id or name) but keeps it as stopped, so it can be
// restarted with `run`. Use `rm` to forget it entirely.
func swarmStop(out io.Writer, ref string) error {
	if err := serviceClient("POST", "/api/swarm/"+ref+"/stop", nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(out, "stopped space %s (run `evva swarm run %s` to restart)\n", ref, ref)
	return nil
}

// swarmVacuum runs one RP-16 retention pass and prints the outcome. days 0
// lets the service resolve the space's configured window.
func swarmVacuum(out io.Writer, ref string, days int, dryRun bool) error {
	var stats webapi.VacuumStats
	body := map[string]any{"days": days, "dry_run": dryRun}
	if err := serviceClient("POST", "/api/swarm/"+ref+"/vacuum", body, &stats); err != nil {
		return err
	}
	verb := "archived"
	if stats.DryRun {
		verb = "would archive"
	}
	fmt.Fprintf(out, "%s %d message(s) and %d completed task(s) older than %d day(s)\n",
		verb, stats.Messages, stats.Tasks, stats.Days)
	for _, f := range stats.Files {
		fmt.Fprintf(out, "  → %s\n", f)
	}
	if stats.DryRun && stats.Messages+stats.Tasks > 0 {
		fmt.Fprintln(out, "run again without --dry-run to apply")
	}
	return nil
}

// swarmRm forgets a space entirely (by id or name). The workdir's on-disk data
// (.vero ledger, transcripts) is left intact — rm drops the registration only.
func swarmRm(out io.Writer, ref string) error {
	if err := serviceClient("DELETE", "/api/swarm/"+ref, nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed space %s\n", ref)
	return nil
}

// swarmReset wipes a space back to a blank slate — fresh task ledger and cleared
// agent context — and brings it back up under the SAME id, so the existing URL
// keeps working. Destructive: all tasks, messages, and transcripts are gone.
func swarmReset(out io.Writer, ref string) error {
	var reply struct {
		ID string `json:"id"`
	}
	if err := serviceClient("POST", "/api/swarm/"+ref+"/reset", nil, &reply); err != nil {
		return err
	}
	fmt.Fprintf(out, "reset space %s — fresh task ledger + cleared agent context\n", ref)
	fmt.Fprintf(out, "  open: http://%s/?space=%s\n", targetAddr(), reply.ID)
	return nil
}

// swarmAdd mounts an existing on-disk member (agents/sub/<name>/) into a space,
// addressed by id or name. The web form authors NEW members through the same
// endpoint with a full spec; a bare name mounts a dir that already exists (RP-8).
func swarmAdd(out io.Writer, ref, member string) error {
	if err := serviceClient("POST", "/api/members?space="+ref, map[string]string{"name": member}, nil); err != nil {
		return err
	}
	fmt.Fprintf(out, "added member %s to space %s\n", member, ref)
	return nil
}

// swarmSend messages a member as the operator (RP-27): the same
// POST /api/agents/{member}/message the web composer uses, so the member sees
// sender "user" — indistinguishable from a web-sent message. Fire-and-forget
// (an idle member wakes on it, a busy one folds it into its current run); the
// printed message id is the durable receipt. text == "-" reads the body from
// stdin, for long messages and script pipelines. member may also be the role
// "leader" (the service resolves it, §3.5). The endpoint also accepts the
// web's "all", but the CLI deliberately doesn't advertise it — the ticket
// keeps operator→member a one-to-one primitive (broadcast = tell the leader
// to relay).
func swarmSend(out io.Writer, ref, member, text string) error {
	if text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("message text is empty")
	}
	var reply struct {
		ID string `json:"id"`
	}
	if err := serviceClient("POST", "/api/agents/"+member+"/message?space="+ref,
		map[string]string{"body": text}, &reply); err != nil {
		return err
	}
	fmt.Fprintf(out, "message %s sent to %s as \"user\" (idle members wake on it; busy ones fold it into their current run)\n", reply.ID, member)
	return nil
}
