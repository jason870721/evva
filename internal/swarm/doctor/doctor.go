// Package doctor is the swarm preflight (DR): `evva swarm doctor` runs the
// whole register-and-run ladder — manifest → member definitions → models /
// efforts → provider keys → .vero state → (optionally) the live service —
// and reports ✓/⚠/✗ per probe, so the expensive mistakes that today explode
// deep inside a member's first run (a typo'd model pin, a missing API key, a
// ledger written by a newer binary) surface before anything registers.
//
// The contract (PRD §4): doctor OBSERVES, never touches. Running it twice,
// or on a machine you don't own, changes nothing — no store.Open (which
// MkdirAlls and migrates), no writes, no registrations; the service section
// is GET-only. Output is the only side effect. That is also why it lives
// CLI-side rather than as a service endpoint: the moments it exists for —
// before the service knows the space, or when the service IS the broken
// part — are exactly the moments an endpoint can't help.
package doctor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"

	_ "modernc.org/sqlite" // the store's own pure-Go driver, reused read-only
)

// Levels. Strings (not iota) so the --json shape is self-describing and
// stable.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// Sections, in dependency order. A hard fail in manifest short-circuits
// members/models/keys (they need a manifest to probe) but state and service
// still run — they are independent of it.
const (
	SectionManifest = "manifest"
	SectionMembers  = "members"
	SectionModels   = "models"
	SectionKeys     = "keys"
	SectionState    = "state"
	SectionService  = "service"
)

var sectionOrder = []string{SectionManifest, SectionMembers, SectionModels, SectionKeys, SectionState, SectionService}

// sectionTag is the report's A–F label per section.
var sectionTag = map[string]string{
	SectionManifest: "A manifest", SectionMembers: "B members", SectionModels: "C models",
	SectionKeys: "D provider keys", SectionState: "E state", SectionService: "F service",
}

// Finding is one probe's verdict — the unit of both the human report and
// the --json output.
type Finding struct {
	Section string `json:"section"`
	Level   string `json:"level"`
	Member  string `json:"member,omitempty"`
	Message string `json:"message"`
}

// ServiceTarget aims the F-section probes at a running service. nil (or
// --offline) skips the section entirely — doctor never dials by surprise.
type ServiceTarget struct {
	Addr    string // host:port
	Token   string // session token ("" = unreadable/absent, probed as a finding)
	Version string // this binary's version, for the skew check
}

// Options configures one doctor run.
type Options struct {
	Dir     string         // workdir to diagnose
	Strict  bool           // promote every ⚠ to ✗ (exit 2 when only ⚠ were promoted)
	Config  *config.Config // key store + AppHome (persona registry) + model defaults
	Service *ServiceTarget // nil = offline
}

// Report is a completed run: every finding in section order.
type Report struct {
	Findings []Finding
	Strict   bool
}

// Run executes the ladder. It never returns an error — a broken probe IS a
// finding; the report is always renderable.
func Run(opts Options) *Report {
	r := &Report{Strict: opts.Strict}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	m, ok := r.probeManifest(dir)
	if ok {
		loaded := r.probeMembers(dir, m, opts.Config)
		r.probeModels(loaded, opts.Config)
		r.probeKeys(loaded, opts.Config)
	}
	r.probeState(dir)
	if opts.Service != nil {
		r.probeService(*opts.Service, m)
	}
	return r
}

func (r *Report) add(section, level, member, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		Section: section, Level: level, Member: member, Message: fmt.Sprintf(format, args...),
	})
}

// counts tallies fails and warns, with strict promoting warns.
func (r *Report) counts() (fails, warns int) {
	for _, f := range r.Findings {
		switch f.Level {
		case LevelFail:
			fails++
		case LevelWarn:
			warns++
		}
	}
	return fails, warns
}

// Exit is the deterministic script contract: 0 clean, 1 any ✗, 2 when
// --strict promoted warnings and nothing failed outright — so a pipeline can
// tell "broken" from "merely unusual".
func (r *Report) Exit() int {
	fails, warns := r.counts()
	switch {
	case fails > 0:
		return 1
	case r.Strict && warns > 0:
		return 2
	default:
		return 0
	}
}

// Render writes the human report: sections in ladder order, one glyphed
// line per finding, and a footer with the totals and the boundary of what
// doctor can promise.
func (r *Report) Render(w io.Writer) {
	glyph := map[string]string{LevelOK: "✓", LevelWarn: "⚠", LevelFail: "✗"}
	for _, sec := range sectionOrder {
		var lines []string
		for _, f := range r.Findings {
			if f.Section != sec {
				continue
			}
			msg := f.Message
			if f.Member != "" {
				msg = f.Member + ": " + msg
			}
			lines = append(lines, fmt.Sprintf("%s %s", glyph[f.Level], msg))
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(w, "  %-16s%s\n", sectionTag[sec], lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(w, "  %-16s%s\n", "", l)
		}
	}
	fails, warns := r.counts()
	verdict := "register would fail."
	switch {
	case fails == 0 && warns == 0:
		verdict = "all clear."
	case fails == 0:
		verdict = "looks registrable — read the warnings."
	}
	fmt.Fprintf(w, "%d error(s), %d warning(s) — %s   exit %d\n", fails, warns, verdict, r.Exit())
	fmt.Fprintln(w, "(doctor is preflight, not warranty: keys are checked for presence, not validity; custom models resolve at client build.)")
}

// JSON writes the findings as a machine-readable array (experimental shape
// for one minor, then frozen).
func (r *Report) JSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r.Findings)
}

// --- A: manifest -------------------------------------------------------------

func (r *Report) probeManifest(dir string) (agentdef.Manifest, bool) {
	path := filepath.Join(dir, "evva-swarm.yml")
	if _, err := os.Stat(path); err != nil {
		r.add(SectionManifest, LevelFail, "", "no evva-swarm.yml in %s", dir)
		return agentdef.Manifest{}, false
	}
	m, err := agentdef.LoadManifest(path)
	if err != nil {
		// LoadManifest's own fail-fast IS the finding — same text register shows.
		r.add(SectionManifest, LevelFail, "", "%v", err)
		return agentdef.Manifest{}, false
	}
	name := m.Name
	if name == "" {
		name = "(unnamed — service assigns a handle)"
	}
	r.add(SectionManifest, LevelOK, "", "evva-swarm.yml — space %q, leader %q, %d worker(s)", name, m.Leader.Agent, len(m.Workers))
	return m, true
}

// --- B: members ----------------------------------------------------------

// memberProbe carries what C/D need from each successfully-loaded member.
type memberProbe struct {
	name   string
	model  string // effective pin after manifest override; "" = config default
	effort string
}

func (r *Report) probeMembers(dir string, m agentdef.Manifest, cfg *config.Config) []memberProbe {
	// One persona registry for every persona member — the same source the
	// space builds its copy from (built-ins + <appHome>/agents).
	var reg *agent.AgentRegistry
	if cfg != nil {
		reg, _ = agent.BuildAgentRegistry(cfg.AppHome)
	}
	loader := agentdef.NewLoader()
	shared := agentdef.SharedSkillsDir(dir)

	probe := func(mem agentdef.Member, role agentdef.Role, agentsDir string) (memberProbe, bool) {
		if mem.FromPersona {
			// Mirror of the space's register-time check (registerPersonaDef) —
			// same conditions, same wording, run before anything registers.
			if reg == nil {
				r.add(SectionMembers, LevelWarn, mem.Agent, "persona member — no config loaded, registry unchecked")
				return memberProbe{name: mem.Agent, model: mem.Model, effort: mem.Effort}, true
			}
			base, ok := reg.Get(mem.Agent)
			if !ok {
				r.add(SectionMembers, LevelFail, mem.Agent, "no such persona %q in the registry (built-ins + <appHome>/agents)", mem.Agent)
				return memberProbe{}, false
			}
			if !base.IsMain() {
				r.add(SectionMembers, LevelFail, mem.Agent, "persona %q is not main-tier", mem.Agent)
				return memberProbe{}, false
			}
			r.add(SectionMembers, LevelOK, mem.Agent, "persona, main-tier")
			model := mem.Model
			if model == "" {
				model = base.Model
			}
			return memberProbe{name: mem.Agent, model: model, effort: mem.Effort}, true
		}

		// Dir member: the real Loader.Build — the highest-fidelity offline
		// probe; its error is exactly what register would say.
		one, err := loader.Build(filepath.Join(dir, "agents", agentsDir, mem.Agent), role, shared)
		if err != nil {
			r.add(SectionMembers, LevelFail, mem.Agent, "%v", err)
			return memberProbe{}, false
		}
		for _, w := range one.Skills.Warnings {
			r.add(SectionMembers, LevelWarn, mem.Agent, "skill: %s", w)
		}
		r.add(SectionMembers, LevelOK, mem.Agent, "dir member (agents/%s/%s)", agentsDir, mem.Agent)
		// Manifest overrides are authoritative (the BuildAll precedence).
		model := one.Def.Model
		if mem.Model != "" {
			model = mem.Model
		}
		effort := one.Effort
		if mem.Effort != "" {
			effort = mem.Effort
		}
		return memberProbe{name: mem.Agent, model: model, effort: effort}, true
	}

	var out []memberProbe
	if p, ok := probe(m.Leader, agentdef.RoleLeader, "main"); ok {
		out = append(out, p)
	}
	for _, wk := range m.Workers {
		if p, ok := probe(wk, agentdef.RoleWorker, "sub"); ok {
			out = append(out, p)
		}
	}
	return out
}

// --- C: models + efforts ---------------------------------------------------

func (r *Report) probeModels(loaded []memberProbe, cfg *config.Config) {
	warnLevel := LevelWarn
	if r.Strict {
		warnLevel = LevelFail
	}
	defaulted := 0
	for _, p := range loaded {
		if p.effort != "" && llm.ParseEffort(p.effort) == 0 {
			// The construct-time rejection (space.constructMember), surfaced early.
			r.add(SectionModels, LevelFail, p.name, "invalid effort %q (want low|medium|high|ultra)", p.effort)
		}
		if p.model == "" {
			defaulted++
			continue
		}
		if prov, ok := constant.ProviderOfModel(constant.Model(p.model)); ok {
			r.add(SectionModels, LevelOK, p.name, "model %s (built-in, provider %s)", p.model, prov.Name)
		} else {
			// Deliberately NOT an error (space.go's loose-pin contract: SDK
			// hosts register custom models that resolve at client build) —
			// unless the operator asked for --strict.
			r.add(SectionModels, warnLevel, p.name, "model %q is not a built-in — custom model? resolves (or fails) at client build", p.model)
		}
	}
	if defaulted > 0 && cfg != nil {
		model := string(cfg.DefaultModel)
		if _, ok := constant.ProviderOfModel(cfg.DefaultModel); ok {
			r.add(SectionModels, LevelOK, "", "%d member(s) on the configured default %s (provider %s)", defaulted, model, cfg.DefaultProvider.Name)
		} else {
			r.add(SectionModels, warnLevel, "", "%d member(s) on the configured default %q — not a built-in model", defaulted, model)
		}
	}
}

// --- D: provider keys --------------------------------------------------------

func (r *Report) probeKeys(loaded []memberProbe, cfg *config.Config) {
	if cfg == nil {
		r.add(SectionKeys, LevelWarn, "", "no config loaded — key presence unchecked")
		return
	}
	// The implied provider set: the configured default (for every unpinned
	// member) plus each built-in pin's provider. Custom pins imply no known
	// provider — their client build is the authority.
	providers := map[string]bool{}
	for _, p := range loaded {
		if p.model == "" {
			providers[cfg.DefaultProvider.Name] = true
			continue
		}
		if prov, ok := constant.ProviderOfModel(constant.Model(p.model)); ok {
			providers[prov.Name] = true
		}
	}
	for name := range providers {
		if name == "" {
			continue
		}
		if name == constant.OLLAMA.Name {
			// Keyless local provider: presence of a base URL is the whole
			// check — doctor makes no network calls.
			url := cfg.GetProviderAPIURL(name)
			if url == "" {
				url = constant.OLLAMA.ApiUrl + " (default)"
			}
			r.add(SectionKeys, LevelOK, "", "ollama — keyless local provider (%s)", url)
			continue
		}
		if cfg.GetProviderAPIKey(name) != "" {
			// Presence, never validity — and never the value.
			r.add(SectionKeys, LevelOK, "", "%s — API key configured", name)
		} else {
			r.add(SectionKeys, LevelFail, "", "%s — no API key configured (members on this provider fail at their first LLM call)", name)
		}
	}
}

// --- E: state (.vero) --------------------------------------------------------

func (r *Report) probeState(dir string) {
	vero := filepath.Join(dir, ".vero")
	if _, err := os.Stat(vero); os.IsNotExist(err) {
		r.add(SectionState, LevelOK, "", ".vero absent (fresh dir — created at register)")
		return
	}

	// vero.db: read-only open (mode=ro — the driver refuses writes and never
	// creates), schema version vs this binary's embedded set.
	dbPath := filepath.Join(vero, "vero.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		r.add(SectionState, LevelOK, "", ".vero present, no vero.db yet")
	} else {
		r.probeLedger(dbPath)
	}

	// runtime.json: the register path treats a corrupt file as EMPTY silently
	// (loadRuntime) — losing membership/meter state. Give that a voice.
	rtPath := filepath.Join(vero, "runtime.json")
	if b, err := os.ReadFile(rtPath); err == nil {
		var probeShape map[string]any
		if json.Unmarshal(b, &probeShape) != nil {
			r.add(SectionState, LevelWarn, "", "runtime.json is corrupt — register treats it as EMPTY (frozen membership, budget meter, and spawned members reset)")
		} else {
			r.add(SectionState, LevelOK, "", "runtime.json parses")
		}
	}

	if fi, err := os.Stat(filepath.Join(vero, "events")); err == nil && !fi.IsDir() {
		r.add(SectionState, LevelWarn, "", ".vero/events exists but is not a directory — the event log cannot write")
	}
}

func (r *Report) probeLedger(dbPath string) {
	// mode=ro (not immutable): refuses every write, works alongside a live
	// service holding the WAL.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(2000)")
	if err == nil {
		err = db.Ping()
	}
	if err != nil {
		r.add(SectionState, LevelWarn, "", "vero.db unreadable (%v) — register may fail to open the ledger", err)
		return
	}
	defer db.Close()

	var current int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		r.add(SectionState, LevelWarn, "", "vero.db has no schema_migrations table — not an evva ledger?")
		return
	}
	latest := store.LatestMigration()
	switch {
	case current < latest:
		r.add(SectionState, LevelOK, "", "vero.db at schema %d — register migrates it to %d", current, latest)
	case current == latest:
		r.add(SectionState, LevelOK, "", "vero.db at schema %d (current)", current)
	default:
		r.add(SectionState, LevelWarn, "", "vero.db at schema %d, this binary knows %d — written by a NEWER evva; register here may misread it", current, latest)
	}
}

// --- F: service --------------------------------------------------------------

func (r *Report) probeService(t ServiceTarget, m agentdef.Manifest) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + t.Addr + "/healthz")
	if err != nil {
		r.add(SectionService, LevelWarn, "", "no service at %s (`evva service start`) — register will fail until one runs", t.Addr)
		return
	}
	defer resp.Body.Close()
	var health struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&health)
	if health.Version != "" && t.Version != "" && health.Version != t.Version {
		r.add(SectionService, LevelWarn, "", "%s healthy but version %s ≠ CLI %s — upgrade one side", t.Addr, health.Version, t.Version)
	} else {
		r.add(SectionService, LevelOK, "", "%s healthy (%s)", t.Addr, health.Version)
	}

	if t.Token == "" {
		r.add(SectionService, LevelWarn, "", "no session token readable — authenticated calls (register included) will 401")
		return
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+t.Addr+"/api/swarms", nil)
	req.Header.Set("Authorization", "Bearer "+t.Token)
	resp2, err := client.Do(req)
	if err != nil {
		r.add(SectionService, LevelWarn, "", "GET /api/swarms failed: %v", err)
		return
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		r.add(SectionService, LevelWarn, "", "GET /api/swarms returned %d — token stale?", resp2.StatusCode)
		return
	}
	var spaces []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&spaces)
	if m.Name != "" {
		for _, s := range spaces {
			if strings.EqualFold(s.Name, m.Name) {
				r.add(SectionService, LevelWarn, "", "name %q already registered (%s) — register needs --name or `evva swarm rm`", m.Name, s.Status)
				return
			}
		}
	}
	r.add(SectionService, LevelOK, "", "token accepted; no name collision (%d space(s) registered)", len(spaces))
}
