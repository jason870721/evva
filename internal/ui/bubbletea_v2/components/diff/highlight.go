package diff

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// highlightCache avoids re-parsing the chroma style and re-matching the
// lexer on every diff render. Keys are the file extension (e.g. ".go")
// or the special sentinel "" for the plaintext fallback.
var (
	highlightCacheMu sync.RWMutex
	highlightCache   = map[string]*highlighter{}
)

type highlighter struct {
	lexer       chroma.Lexer
	chromaStyle *chroma.Style
}

func getHighlighter(filePath string) *highlighter {
	ext := strings.ToLower(filepath.Ext(filePath))

	highlightCacheMu.RLock()
	h, ok := highlightCache[ext]
	highlightCacheMu.RUnlock()
	if ok {
		return h
	}

	highlightCacheMu.Lock()
	defer highlightCacheMu.Unlock()
	// Double-check after acquiring write lock.
	if h, ok := highlightCache[ext]; ok {
		return h
	}

	h = buildHighlighter(filePath)
	highlightCache[ext] = h
	return h
}

func buildHighlighter(filePath string) *highlighter {
	lexer := lexers.Match(filePath)
	if lexer == nil {
		lexer = lexers.Fallback
	}

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	return &highlighter{lexer: lexer, chromaStyle: style}
}

// highlightLine tokenizes a single line of diff content and returns the
// styled string. Each token gets its foreground from the chroma style
// while the base lipgloss Style supplies the background (and bold, for
// add/remove rows). The result is padded to width with bg-colored
// spaces so the colored background fill extends across the column.
func highlightLine(line string, bg lipgloss.Style, h *highlighter, width int) string {
	tokens, err := chroma.Tokenise(h.lexer, nil, line)
	if err != nil || len(tokens) == 0 {
		return fillPlain(bg, line, width)
	}

	var b strings.Builder
	for _, tok := range tokens {
		s := styleForToken(tok.Type, bg, h.chromaStyle)
		if s == nil {
			b.WriteString(bg.Render(tok.Value))
		} else {
			b.WriteString(s.Render(tok.Value))
		}
	}

	return padRight(b.String(), width, bg)
}

// fillPlain is the fallback when no lexer matches or tokenisation fails.
func fillPlain(bg lipgloss.Style, s string, width int) string {
	if width <= 0 {
		return bg.Render(s)
	}
	return bg.Width(width).Render(s)
}

// styleForToken returns a lipgloss Style for one chroma token, or nil
// when the token should just use the base bg style as-is (whitespace,
// plain text, etc.).
func styleForToken(tt chroma.TokenType, bg lipgloss.Style, cs *chroma.Style) *lipgloss.Style {
	// Whitespace carries no semantic colour — let the base bg style
	// render it directly.
	if tt == chroma.Whitespace {
		return nil
	}

	se := cs.Get(tt)

	// No colour set in the style entry → fall back to base.
	if !se.Colour.IsSet() {
		return nil
	}

	s := bg.Foreground(lipgloss.Color(se.Colour.String()))
	if se.Bold == chroma.Yes {
		s = s.Bold(true)
	}
	if se.Italic == chroma.Yes {
		s = s.Italic(true)
	}
	// Underline is uncommon in terminal code views — skipped.

	return &s
}

// padRight ensures the rendered string spans at least width columns by
// appending bg-styled spaces. width <= 0 is a no-op.
func padRight(s string, width int, bg lipgloss.Style) string {
	if width <= 0 {
		return s
	}
	actual := lipgloss.Width(s)
	if actual >= width {
		return s
	}
	return s + bg.Render(strings.Repeat(" ", width-actual))
}
