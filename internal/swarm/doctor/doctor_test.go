package doctor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
)

// writeSpace lays down a minimal registrable workdir: a manifest with leader
// "lead" + worker "w" and their agent dirs. Callers sabotage from there.
func writeSpace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "evva-swarm.yml"), `
name: fixture-team
leader: {agent: lead}
workers:
  - agent: w
`)
	mustWrite(t, filepath.Join(dir, "agents", "main", "lead", "system_prompt.md"), "You are lead.")
	mustWrite(t, filepath.Join(dir, "agents", "sub", "w", "system_prompt.md"), "You are w.")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testCfg is a config with a priced default provider and its key set.
func testCfg(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "doctortest", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.DefaultProvider = constant.ANTHROPIC
	cfg.DefaultModel = constant.SONNET_4_6
	cfg.LLMProviderConfig[constant.ANTHROPIC.Name] = config.APIConfig{ApiURL: constant.ANTHROPIC.ApiUrl, ApiSecret: "sk-test"}
	return cfg
}

// treeHash fingerprints a directory tree (paths + contents) so tests can
// assert doctor's §4 contract: running it changes NOTHING.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		io.WriteString(h, p+"\n")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			h.Write(b)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// findingsOf filters one section's findings.
func findingsOf(r *Report, section string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Section == section {
			out = append(out, f)
		}
	}
	return out
}

func hasLevel(fs []Finding, level, contains string) bool {
	for _, f := range fs {
		if f.Level == level && strings.Contains(f.Member+": "+f.Message, contains) {
			return true
		}
	}
	return false
}

// TestDoctorCleanSpace: a registrable fixture is all-✓ offline, exit 0 —
// and the run mutates nothing (the §4 contract, hashed before/after).
func TestDoctorCleanSpace(t *testing.T) {
	dir := writeSpace(t)
	before := treeHash(t, dir)

	r := Run(Options{Dir: dir, Config: testCfg(t)})
	if got := r.Exit(); got != 0 {
		var b strings.Builder
		r.Render(&b)
		t.Fatalf("exit = %d, want 0\n%s", got, b.String())
	}
	if !hasLevel(findingsOf(r, SectionManifest), LevelOK, "fixture-team") {
		t.Fatalf("manifest finding missing: %+v", r.Findings)
	}
	if !hasLevel(findingsOf(r, SectionMembers), LevelOK, "lead") || !hasLevel(findingsOf(r, SectionMembers), LevelOK, "w") {
		t.Fatalf("member findings missing: %+v", findingsOf(r, SectionMembers))
	}
	if !hasLevel(findingsOf(r, SectionKeys), LevelOK, "anthropic") {
		t.Fatalf("key finding missing: %+v", findingsOf(r, SectionKeys))
	}
	if !hasLevel(findingsOf(r, SectionState), LevelOK, "fresh dir") {
		t.Fatalf("state finding missing: %+v", findingsOf(r, SectionState))
	}
	if len(findingsOf(r, SectionService)) != 0 {
		t.Fatalf("offline run probed the service: %+v", findingsOf(r, SectionService))
	}

	if after := treeHash(t, dir); after != before {
		t.Fatal("doctor mutated the workdir — the §4 contract is broken")
	}
}

// TestDoctorBrokenMembers: a missing prompt fails B with register's own
// wording; a missing persona fails; the manifest stays ✓; exit 1.
func TestDoctorBrokenMembers(t *testing.T) {
	dir := writeSpace(t)
	if err := os.Remove(filepath.Join(dir, "agents", "sub", "w", "system_prompt.md")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "evva-swarm.yml"), `
name: fixture-team
leader: {agent: lead}
workers:
  - agent: w
  - persona: no-such-persona
`)
	r := Run(Options{Dir: dir, Config: testCfg(t)})
	members := findingsOf(r, SectionMembers)
	if !hasLevel(members, LevelFail, "system_prompt.md") {
		t.Fatalf("missing-prompt fail absent: %+v", members)
	}
	if !hasLevel(members, LevelFail, "no such persona") {
		t.Fatalf("missing-persona fail absent: %+v", members)
	}
	if !hasLevel(findingsOf(r, SectionMembers), LevelOK, "lead") {
		t.Fatalf("healthy leader should still probe OK: %+v", members)
	}
	if r.Exit() != 1 {
		t.Fatalf("exit = %d, want 1", r.Exit())
	}
}

// TestDoctorBadManifestShortCircuits: A fails; B/C/D never probe; E still
// runs (independent).
func TestDoctorBadManifestShortCircuits(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "evva-swarm.yml"), `
workers:
  - agent: w
`) // no leader — LoadManifest rejects
	r := Run(Options{Dir: dir, Config: testCfg(t)})
	if !hasLevel(findingsOf(r, SectionManifest), LevelFail, "leader") {
		t.Fatalf("manifest fail missing: %+v", findingsOf(r, SectionManifest))
	}
	for _, sec := range []string{SectionMembers, SectionModels, SectionKeys} {
		if len(findingsOf(r, sec)) != 0 {
			t.Fatalf("section %s probed off a bad manifest: %+v", sec, findingsOf(r, sec))
		}
	}
	if len(findingsOf(r, SectionState)) == 0 {
		t.Fatal("state section should run regardless of the manifest")
	}
	if r.Exit() != 1 {
		t.Fatalf("exit = %d, want 1", r.Exit())
	}
}

// TestDoctorModelsAndStrict: a custom pin is ⚠ by contract (space.go's loose
// pin) and ✗ only under --strict, where warnings-only exits 2; a built-in
// pin notes its provider and demands that provider's key.
func TestDoctorModelsAndStrict(t *testing.T) {
	dir := writeSpace(t)
	mustWrite(t, filepath.Join(dir, "agents", "sub", "w", "profile.yml"), "model: claude-sonet-5\n") // typo'd pin
	cfg := testCfg(t)

	r := Run(Options{Dir: dir, Config: cfg})
	if !hasLevel(findingsOf(r, SectionModels), LevelWarn, "not a built-in") {
		t.Fatalf("custom pin should warn: %+v", findingsOf(r, SectionModels))
	}
	if r.Exit() != 0 {
		t.Fatalf("exit = %d, want 0 (warn only)", r.Exit())
	}

	rs := Run(Options{Dir: dir, Strict: true, Config: cfg})
	if !hasLevel(findingsOf(rs, SectionModels), LevelFail, "not a built-in") {
		t.Fatalf("--strict should promote the custom pin: %+v", findingsOf(rs, SectionModels))
	}
	if rs.Exit() != 1 {
		t.Fatalf("strict exit = %d, want 1 (promoted warns count as fails)", rs.Exit())
	}

	// A built-in pin on a SECOND provider demands that provider's key too.
	mustWrite(t, filepath.Join(dir, "agents", "sub", "w", "profile.yml"),
		"model: "+string(constant.DEEPSEEK_V4_PRO)+"\n")
	r2 := Run(Options{Dir: dir, Config: cfg})
	if !hasLevel(findingsOf(r2, SectionModels), LevelOK, "deepseek") {
		t.Fatalf("built-in pin should note the provider: %+v", findingsOf(r2, SectionModels))
	}
	if !hasLevel(findingsOf(r2, SectionKeys), LevelFail, "deepseek") {
		t.Fatalf("second provider without a key should fail D: %+v", findingsOf(r2, SectionKeys))
	}
	if !hasLevel(findingsOf(r2, SectionKeys), LevelOK, "anthropic") {
		t.Fatalf("default provider's key should stay OK: %+v", findingsOf(r2, SectionKeys))
	}
}

// TestDoctorEffortAndMissingKey: an invalid effort fails C (the construct-
// time rejection surfaced early); a missing default-provider key fails D.
func TestDoctorEffortAndMissingKey(t *testing.T) {
	dir := writeSpace(t)
	mustWrite(t, filepath.Join(dir, "agents", "sub", "w", "profile.yml"), "effort: turbo\n")
	cfg := testCfg(t)
	delete(cfg.LLMProviderConfig, constant.ANTHROPIC.Name)

	r := Run(Options{Dir: dir, Config: cfg})
	if !hasLevel(findingsOf(r, SectionModels), LevelFail, `invalid effort "turbo"`) {
		t.Fatalf("bad effort should fail: %+v", findingsOf(r, SectionModels))
	}
	if !hasLevel(findingsOf(r, SectionKeys), LevelFail, "anthropic") {
		t.Fatalf("missing key should fail: %+v", findingsOf(r, SectionKeys))
	}
}

// TestDoctorStateProbes: schema older = will-migrate ✓, newer = ⚠, corrupt
// runtime.json = ⚠ — all read-only (tree hash unchanged, .vero included).
func TestDoctorStateProbes(t *testing.T) {
	mkLedger := func(t *testing.T, dir string, version int64) {
		t.Helper()
		vero := filepath.Join(dir, ".vero")
		if err := os.MkdirAll(vero, 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", "file:"+filepath.Join(vero, "vero.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`, version); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("older_schema_will_migrate", func(t *testing.T) {
		dir := writeSpace(t)
		mkLedger(t, dir, 1)
		before := treeHash(t, dir)
		r := Run(Options{Dir: dir, Config: testCfg(t)})
		if !hasLevel(findingsOf(r, SectionState), LevelOK, fmt.Sprintf("register migrates it to %d", store.LatestMigration())) {
			t.Fatalf("older schema should read as will-migrate: %+v", findingsOf(r, SectionState))
		}
		if treeHash(t, dir) != before {
			t.Fatal("state probe mutated .vero — ro contract broken")
		}
	})

	t.Run("newer_schema_warns", func(t *testing.T) {
		dir := writeSpace(t)
		mkLedger(t, dir, store.LatestMigration()+1)
		r := Run(Options{Dir: dir, Config: testCfg(t)})
		if !hasLevel(findingsOf(r, SectionState), LevelWarn, "NEWER evva") {
			t.Fatalf("newer schema should warn: %+v", findingsOf(r, SectionState))
		}
	})

	t.Run("corrupt_runtime_json", func(t *testing.T) {
		dir := writeSpace(t)
		mustWrite(t, filepath.Join(dir, ".vero", "runtime.json"), "{not json")
		r := Run(Options{Dir: dir, Config: testCfg(t)})
		if !hasLevel(findingsOf(r, SectionState), LevelWarn, "treats it as EMPTY") {
			t.Fatalf("corrupt runtime.json should warn with the consequence: %+v", findingsOf(r, SectionState))
		}
	})
}

// doctorService builds a ServiceTarget (helper keeps the test calls short).
func doctorService(addr string) ServiceTarget {
	return ServiceTarget{Addr: addr, Token: "tok", Version: "v1.0.0"}
}

// TestDoctorServiceFindings drives the three service outcomes explicitly.
func TestDoctorServiceFindings(t *testing.T) {
	dir := writeSpace(t)
	cfg := testCfg(t)

	t.Run("skew_and_collision", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/healthz":
				fmt.Fprint(w, `{"version":"v9.9.9"}`)
			case "/api/swarms":
				fmt.Fprint(w, `[{"name":"fixture-team","status":"stopped"}]`)
			}
		}))
		defer ts.Close()
		st := doctorService(strings.TrimPrefix(ts.URL, "http://"))
		r := Run(Options{Dir: dir, Config: cfg, Service: &st})
		svc := findingsOf(r, SectionService)
		if !hasLevel(svc, LevelWarn, "≠ CLI") {
			t.Fatalf("version skew should warn: %+v", svc)
		}
		if !hasLevel(svc, LevelWarn, "already registered") {
			t.Fatalf("name collision should warn: %+v", svc)
		}
	})

	t.Run("healthy_no_collision", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/healthz":
				fmt.Fprint(w, `{"version":"v1.0.0"}`)
			case "/api/swarms":
				fmt.Fprint(w, `[]`)
			}
		}))
		defer ts.Close()
		st := doctorService(strings.TrimPrefix(ts.URL, "http://"))
		r := Run(Options{Dir: dir, Config: cfg, Service: &st})
		svc := findingsOf(r, SectionService)
		if !hasLevel(svc, LevelOK, "healthy") || !hasLevel(svc, LevelOK, "no name collision") {
			t.Fatalf("healthy service should be all-OK: %+v", svc)
		}
	})

	t.Run("service_down_warns", func(t *testing.T) {
		st := ServiceTarget{Addr: "127.0.0.1:1", Token: "tok", Version: "v1"}
		r := Run(Options{Dir: dir, Config: cfg, Service: &st})
		if !hasLevel(findingsOf(r, SectionService), LevelWarn, "evva service start") {
			t.Fatalf("down service should warn with the remedy: %+v", findingsOf(r, SectionService))
		}
	})
}

// TestDoctorJSONShape: --json emits the findings array with the stable keys.
func TestDoctorJSONShape(t *testing.T) {
	dir := writeSpace(t)
	r := Run(Options{Dir: dir, Config: testCfg(t)})
	var b strings.Builder
	if err := r.JSON(&b); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("not a JSON array: %v\n%s", err, b.String())
	}
	if len(got) == 0 || got[0]["section"] == "" || got[0]["level"] == "" {
		t.Fatalf("finding shape wrong: %+v", got)
	}
}
