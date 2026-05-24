package lsp

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

const (
	defaultDiagCacheCapacity = 500
	defaultMaxPerFile        = 10
	defaultMaxTotal          = 30
)

// PendingDiagnostic holds diagnostics queued for delivery for a single file.
type PendingDiagnostic struct {
	ServerName  string
	FileURI     string
	FilePath    string
	Diagnostics []protocol.Diagnostic
}

// DiagnosticRegistry collects, deduplicates, and drains LSP diagnostics.
type DiagnosticRegistry struct {
	mu         sync.Mutex
	pending    []PendingDiagnostic
	delivered  *diagnosticKeySet
	maxPerFile int
	maxTotal   int
}

// NewDiagnosticRegistry creates a registry with default limits.
func NewDiagnosticRegistry() *DiagnosticRegistry {
	return &DiagnosticRegistry{
		delivered:  newDiagnosticKeySet(defaultDiagCacheCapacity),
		maxPerFile: defaultMaxPerFile,
		maxTotal:   defaultMaxTotal,
	}
}

// Register adds diagnostics for a file. Already-delivered diagnostics
// (matched by identity key) are dropped. Per-file and total caps are enforced.
func (r *DiagnosticRegistry) Register(serverName, fileURI string, diags []protocol.Diagnostic) {
	if len(diags) == 0 {
		return
	}

	// Convert URI to a display path.
	filePath := fileURIToPath(fileURI)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Filter out already-delivered diagnostics.
	fresh := make([]protocol.Diagnostic, 0, len(diags))
	for _, d := range diags {
		key := diagKey(fileURI, d)
		if r.delivered.Contains(key) {
			continue
		}
		fresh = append(fresh, d)
	}
	if len(fresh) == 0 {
		return
	}

	// Per-file cap.
	if len(fresh) > r.maxPerFile {
		fresh = fresh[:r.maxPerFile]
	}

	// Track delivered keys for the diagnostics we're keeping.
	for _, d := range fresh {
		r.delivered.Add(diagKey(fileURI, d))
	}

	// Total cap — drop oldest entries if we'd exceed.
	existing := 0
	for _, p := range r.pending {
		existing += len(p.Diagnostics)
	}
	for existing+len(fresh) > r.maxTotal {
		if len(r.pending) == 0 {
			break
		}
		oldest := r.pending[0]
		removed := len(oldest.Diagnostics)
		existing -= removed
		r.pending = r.pending[1:]
	}
	if existing+len(fresh) > r.maxTotal && len(r.pending) == 0 && len(fresh) > r.maxTotal {
		fresh = fresh[:r.maxTotal]
	}

	r.pending = append(r.pending, PendingDiagnostic{
		ServerName:  serverName,
		FileURI:     fileURI,
		FilePath:    filePath,
		Diagnostics: fresh,
	})
}

// Drain returns all pending diagnostics and clears the queue.
func (r *DiagnosticRegistry) Drain() []PendingDiagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.pending
	r.pending = nil
	return out
}

// ClearFile removes pending diagnostics for a file and marks their identity
// keys as delivered (so they don't re-appear if the server re-sends them).
func (r *DiagnosticRegistry) ClearFile(fileURI string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove pending entries for this file.
	filtered := r.pending[:0]
	for _, p := range r.pending {
		if p.FileURI != fileURI {
			filtered = append(filtered, p)
		} else {
			// Mark as delivered so the server doesn't re-deliver stale diagnostics.
			for _, d := range p.Diagnostics {
				r.delivered.Add(diagKey(fileURI, d))
			}
		}
	}
	r.pending = filtered
}

// ── diagnostic identity key ────────────────────────────────────────────

// diagKey builds a stable identity key for a diagnostic.
// Two diagnostics with the same (message, severity, range, source, code)
// are considered identical. Uses sha256 truncated to 16 hex chars — the
// full sha256 over a small payload is overkill; 64 bits of identity is
// more than enough for the 500-entry LRU.
func diagKey(fileURI string, d protocol.Diagnostic) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d:%d-%d:%d|%s|%s",
		d.Message, d.Severity,
		d.Range.Start.Line, d.Range.Start.Character,
		d.Range.End.Line, d.Range.End.Character,
		d.Source, d.Code)
	sum := fmt.Sprintf("%x", h.Sum(nil))
	return fileURI + "|" + sum[:16]
}

// ── bounded LRU key set ────────────────────────────────────────────────

// diagnosticKeySet is a bounded, thread-safe set with LRU eviction.
// Built on container/list + map — the same pattern used by Go's own
// groupcache/lru. No external dependencies.
type diagnosticKeySet struct {
	capacity int
	items    map[string]*list.Element
	order    *list.List
}

func newDiagnosticKeySet(capacity int) *diagnosticKeySet {
	return &diagnosticKeySet{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// Contains checks if key is in the set without affecting recency.
func (s *diagnosticKeySet) Contains(key string) bool {
	_, ok := s.items[key]
	return ok
}

// Add inserts key. If at capacity, evicts the least recently used key.
// If key already exists, it is moved to the back (most recent).
func (s *diagnosticKeySet) Add(key string) {
	if elem, ok := s.items[key]; ok {
		s.order.MoveToBack(elem)
		return
	}
	if s.order.Len() >= s.capacity {
		oldest := s.order.Front()
		s.order.Remove(oldest)
		delete(s.items, oldest.Value.(string))
	}
	elem := s.order.PushBack(key)
	s.items[key] = elem
}

// ── formatting ─────────────────────────────────────────────────────────

// FormatDiagnosticsReminder renders diagnostics as a <system-reminder> block.
func FormatDiagnosticsReminder(diags []PendingDiagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>")
	for _, pd := range diags {
		fmt.Fprintf(&b, "\nLSP diagnostics from %s for %s:\n", pd.ServerName, pd.FilePath)
		for _, d := range pd.Diagnostics {
			line := d.Range.Start.Line + 1
			char := d.Range.Start.Character + 1
			fmt.Fprintf(&b, "  [%s] Line %d:%d: %s\n", d.Severity, line, char, d.Message)
		}
	}
	b.WriteString("</system-reminder>")
	return b.String()
}

// fileURIToPath extracts a filesystem path from a file:// URI.
func fileURIToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return uri[7:]
	}
	return uri
}
