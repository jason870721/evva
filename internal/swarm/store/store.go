package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

// We use modernc.org/sqlite (pure Go, no cgo) rather than mattn/go-sqlite3 so
// the binary keeps cross-compiling with CGO_ENABLED=0 — evva's release.yml
// builds darwin/linux × amd64/arm64 with a plain `go build`, which a cgo
// driver would break.

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	veroDir = ".vero"
	dbFile  = "vero.db"
)

// Store is the single per-space data layer over <workdir>/.vero/vero.db. All
// access is guarded by a sync.RWMutex: writes take Lock, reads take RLock. The
// task ledger is single-writer (the Leader) by design; the mutex's real job is
// the multi-writer `messages` table.
type Store struct {
	db  *sql.DB
	dir string // <workdir>/.vero — the db's home, where archive/ lives (RP-16)
	mu  sync.RWMutex
}

// Open creates <workdir>/.vero/ if needed, opens vero.db with WAL +
// busy_timeout + foreign keys, runs forward-only migrations, and returns a
// ready Store. One db file per workdir = one space (invariant #2).
func Open(workdir string) (*Store, error) {
	dir := filepath.Join(workdir, veroDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create %s: %w", dir, err)
	}
	path := filepath.Join(dir, dbFile)

	// Per-connection pragmas via the DSN so every pooled connection gets them
	// (busy_timeout + foreign_keys are connection-scoped). journal_mode is set
	// here and re-asserted below since WAL is a persistent file-level mode.
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: enable WAL: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	s := &Store{db: db, dir: dir}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	// Log the resolved DB path: this is the single most useful line when a
	// ledger "looks empty" — it pins exactly which vero.db the space writes to,
	// so an operator inspecting the wrong file (or a stale copy) sees the truth.
	slog.Info("swarm store opened", "path", path, "journal_mode", "WAL")
	return s, nil
}

// Close checkpoints the WAL into the main db file, then releases the handle.
// The explicit TRUNCATE checkpoint matters for debugging: WAL mode writes the
// schema + rows to vero.db-wal and only folds them into vero.db on a checkpoint
// (auto-checkpoint needs ~1000 pages, which a small ledger never reaches), so
// without this an operator who opens vero.db after shutdown can see no schema
// and no data even though everything was committed. A checkpoint failure is
// non-fatal — the data is still durable in the WAL.
func (s *Store) Close() error {
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		slog.Warn("swarm store: wal checkpoint on close failed", "err", err)
	}
	return s.db.Close()
}

// RemoveData deletes a space's entire on-disk data dir (<workdir>/.vero): the
// vero.db ledger and its WAL sidecars, plus runtime.json. A swarm reset calls
// this after closing the store; the next Open recreates a fresh, migrated db.
// Best-effort/idempotent — a missing dir is not an error.
func RemoveData(workdir string) error {
	return os.RemoveAll(filepath.Join(workdir, veroDir))
}

// migrate applies any embedded migrations whose version is greater than the
// highest already recorded in schema_migrations. Forward-only for v1; each
// file runs in its own transaction.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}

	var current int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			return fmt.Errorf("bad migration name %q: %w", name, err)
		}
		if v <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
		slog.Debug("swarm store: migration applied", "version", v, "name", name)
	}
	return nil
}

// migrationVersion parses the leading integer of a migration filename
// ("0001_init.sql" -> 1).
func migrationVersion(name string) (int64, error) {
	base := name
	if i := strings.IndexByte(base, '_'); i > 0 {
		base = base[:i]
	} else {
		base = strings.TrimSuffix(base, ".sql")
	}
	return strconv.ParseInt(base, 10, 64)
}

// nullableStr returns nil for an empty string so it stores as SQL NULL.
func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableInt returns nil for a nil pointer so it stores as SQL NULL.
func nullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
