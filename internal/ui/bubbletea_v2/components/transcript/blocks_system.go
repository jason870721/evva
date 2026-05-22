package transcript

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/internal/ui/bubbletea_v2/theme"
)

// ============================================================================
// BannerBlock — welcome box at the top of the transcript.
// ============================================================================

// BannerSpec captures the per-session welcome data: the ASCII art,
// the greeting line, and a list of labeled info rows the App
// populates after Attach (agent ID, model, started-at).
type BannerSpec struct {
	Art      string
	Greeting string
	Info     []BannerInfo
}

// BannerInfo is a single labeled row in the banner footer.
type BannerInfo struct {
	Label string
	Value string
}

// BannerBlock renders the spec as a centered double-bordered box.
// On resize the cache invalidates (width changed) and the block
// re-renders with the new centering. No special path needed — this
// is the M3 simplification vs. v1's reflowBanner.
type BannerBlock struct {
	id   uint64
	rev  uint64
	spec BannerSpec
}

func NewBannerBlock(spec BannerSpec) *BannerBlock {
	return &BannerBlock{id: allocID(), rev: 1, spec: spec}
}

func (b *BannerBlock) ID() uint64  { return b.id }
func (b *BannerBlock) Rev() uint64 { return b.rev }
func (b *BannerBlock) Kind() Kind  { return KindBanner }

// PlainText returns the art + greeting + info rows as plain text,
// useful for the M8 yank-mode copy of the welcome block.
func (b *BannerBlock) PlainText() string {
	var sb strings.Builder
	if art := strings.TrimRight(b.spec.Art, "\n"); art != "" {
		sb.WriteString(art)
	}
	if g := strings.TrimSpace(b.spec.Greeting); g != "" {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(g)
	}
	for _, r := range b.spec.Info {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.ToUpper(r.Label))
		sb.WriteString("  ")
		sb.WriteString(r.Value)
	}
	return sb.String()
}

// SetSpec replaces the spec and bumps Rev. Used by App.Attach when
// the controller's metadata (agent ID, model) becomes known after
// initial construction.
func (b *BannerBlock) SetSpec(spec BannerSpec) {
	b.spec = spec
	b.rev++
}

func (b *BannerBlock) Render(ctx RenderContext) string {
	art := strings.TrimRight(b.spec.Art, "\n")
	greeting := strings.TrimSpace(b.spec.Greeting)
	rows := b.spec.Info

	if art == "" && greeting == "" && len(rows) == 0 {
		return ""
	}

	sepLen := bannerSepWidth(art, greeting, rows)
	separator := ctx.Theme.Timeline.Render(strings.Repeat("─", sepLen/2)) +
		ctx.Theme.TimelineCut.Render(" ◆ ") +
		ctx.Theme.Timeline.Render(strings.Repeat("─", sepLen-sepLen/2-3))

	var inner strings.Builder
	if art != "" {
		inner.WriteString(ctx.Theme.Banner.Render(art))
	}
	if greeting != "" {
		if inner.Len() > 0 {
			inner.WriteString("\n")
			inner.WriteString(separator)
			inner.WriteString("\n")
		}
		inner.WriteString(ctx.Theme.Greeting.Render(greeting))
	}
	if len(rows) > 0 {
		if inner.Len() > 0 {
			inner.WriteString("\n")
			inner.WriteString(separator)
			inner.WriteString("\n")
		}
		inner.WriteString(renderBannerInfo(rows, ctx.Theme))
	}

	box := ctx.Theme.BannerBox.Render(inner.String())
	if ctx.Width <= 0 {
		// No width known yet (pre-first-WindowSizeMsg) — emit the
		// box without horizontal placement. Matches v1's defensive
		// fallback.
		return box
	}
	return lipgloss.PlaceHorizontal(ctx.Width, lipgloss.Center, box)
}

// bannerSepWidth picks a separator length proportional to the
// widest section so the scanline reads as "in line with everything
// else" instead of floating. Clamped to a sane window so very tall
// ASCII art doesn't blow out the box.
func bannerSepWidth(art, greeting string, rows []BannerInfo) int {
	max := 0
	for _, line := range strings.Split(art, "\n") {
		if n := lipgloss.Width(line); n > max {
			max = n
		}
	}
	if n := lipgloss.Width(greeting); n > max {
		max = n
	}
	for _, r := range rows {
		// Approximate: arrow + label + 2 + value.
		n := 2 + len(r.Label) + 2 + len(r.Value)
		if n > max {
			max = n
		}
	}
	switch {
	case max < 24:
		return 24
	case max > 80:
		return 80
	default:
		return max
	}
}

func renderBannerInfo(rows []BannerInfo, th *theme.Theme) string {
	maxLabel := 0
	for _, r := range rows {
		if len(r.Label) > maxLabel {
			maxLabel = len(r.Label)
		}
	}
	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		label := strings.ToUpper(r.Label) + strings.Repeat(" ", maxLabel-len(r.Label))
		b.WriteString(th.UserPrompt.Render("▸ "))
		b.WriteString(th.BannerInfo.Render(label))
		b.WriteString("  ")
		b.WriteString(th.StatusValue.Render(r.Value))
	}
	return b.String()
}

// ============================================================================
// UserPromptBlock — the diamond scanline + prompt body cutting the timeline.
// ============================================================================

type UserPromptBlock struct {
	id   uint64
	rev  uint64
	text string
}

func newUserPromptBlock(text string) *UserPromptBlock {
	return &UserPromptBlock{id: allocID(), rev: 1, text: text}
}

func (b *UserPromptBlock) ID() uint64        { return b.id }
func (b *UserPromptBlock) Rev() uint64       { return b.rev }
func (b *UserPromptBlock) Kind() Kind        { return KindUserPrompt }
func (b *UserPromptBlock) PlainText() string { return b.text }

func (b *UserPromptBlock) Render(ctx RenderContext) string {
	body := styleUserPromptLines(b.text, ctx.Theme)
	return renderUserPromptHeader(body, ctx.Width, ctx.Theme)
}

// styleUserPromptLines applies UserPrompt styling line-by-line so
// the `▶ ` head sits on row 0 and any lines that already carry ANSI
// codes (paste boundary chips, etc.) flow through without re-styling
// that would clobber their colors.
func styleUserPromptLines(text string, th *theme.Theme) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.ContainsRune(line, 0x1b) {
			if i == 0 {
				lines[i] = th.UserPrompt.Render("▶ ") + line
			}
			continue
		}
		if i == 0 {
			lines[i] = th.UserPrompt.Render("▶ " + line)
		} else {
			lines[i] = th.UserPrompt.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// ============================================================================
// ErrorBlock — KindError red banner.
// ============================================================================

type ErrorBlock struct {
	id    uint64
	rev   uint64
	stage string
	err   string
}

func newErrorBlock(stage, err string) *ErrorBlock {
	return &ErrorBlock{id: allocID(), rev: 1, stage: stage, err: err}
}

func (b *ErrorBlock) ID() uint64  { return b.id }
func (b *ErrorBlock) Rev() uint64 { return b.rev }
func (b *ErrorBlock) Kind() Kind  { return KindError }
func (b *ErrorBlock) PlainText() string {
	return fmt.Sprintf("✘ [%s] %s", strings.ToUpper(b.stage), b.err)
}

func (b *ErrorBlock) Render(ctx RenderContext) string {
	styled := ctx.Theme.ErrorBanner.Render(fmt.Sprintf("✘ [%s] %s", strings.ToUpper(b.stage), b.err))
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ============================================================================
// SystemBlock — generic chrome rows (draining, cancelled, iter-limit).
// ============================================================================

type SystemBlock struct {
	id      uint64
	rev     uint64
	text    string
	styleFn func(*theme.Theme) lipgloss.Style
}

func newDrainingBlock() *SystemBlock {
	return &SystemBlock{
		id: allocID(), rev: 1,
		text:    "◈ DRAINING async subagent results",
		styleFn: func(th *theme.Theme) lipgloss.Style { return th.Draining },
	}
}

func newCancelledBlock() *SystemBlock {
	return &SystemBlock{
		id: allocID(), rev: 1,
		text:    "◇ CANCELLED",
		styleFn: func(th *theme.Theme) lipgloss.Style { return th.DimText },
	}
}

func newIterLimitBlock(reached int) *SystemBlock {
	return &SystemBlock{
		id: allocID(), rev: 1,
		text:    fmt.Sprintf("⏸ ITER-LIMIT %d — press Enter to continue", reached),
		styleFn: func(th *theme.Theme) lipgloss.Style { return th.Compacting },
	}
}

func (b *SystemBlock) ID() uint64        { return b.id }
func (b *SystemBlock) Rev() uint64       { return b.rev }
func (b *SystemBlock) Kind() Kind        { return KindSystem }
func (b *SystemBlock) PlainText() string { return b.text }

func (b *SystemBlock) Render(ctx RenderContext) string {
	styled := b.styleFn(ctx.Theme).Render(b.text)
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ============================================================================
// CompactingBlock — animated inflight compaction row.
// ============================================================================

// CompactingBlock animates a spinner while a compaction is in
// flight. The transcript drives SetFrame from the App's
// SpinnerTickMsg handler; the frame index bumps Rev so the cache
// invalidates and the row re-renders.
type CompactingBlock struct {
	id    uint64
	rev   uint64
	kind  string // "micro" | "full" (matches event.CompactingPayload.Type)
	frame int
}

func newCompactingBlock(kind string) *CompactingBlock {
	return &CompactingBlock{id: allocID(), rev: 1, kind: kind}
}

func (b *CompactingBlock) ID() uint64  { return b.id }
func (b *CompactingBlock) Rev() uint64 { return b.rev }
func (b *CompactingBlock) Kind() Kind  { return KindCompacting }

func (b *CompactingBlock) PlainText() string {
	kind := strings.ToUpper(b.kind)
	if kind == "" {
		kind = "?"
	}
	return fmt.Sprintf("⠋ Compacting… [%s]", kind)
}

// SetFrame updates the spinner index for the next render. Cheap; no
// allocation. Bumps Rev so the cache invalidates one row.
func (b *CompactingBlock) SetFrame(frame int) {
	if frame == b.frame {
		return
	}
	b.frame = frame
	b.rev++
}

// SetKind updates the compaction type label. Called when the agent
// emits a second KindCompacting (rare — happens when the manual
// chooser fires while auto-compact is mid-flight).
func (b *CompactingBlock) SetKind(kind string) {
	if kind == b.kind {
		return
	}
	b.kind = kind
	b.rev++
}

func (b *CompactingBlock) Render(ctx RenderContext) string {
	frame := theme.SpinnerFrame(b.frame)
	kind := strings.ToUpper(b.kind)
	if kind == "" {
		kind = "?"
	}
	styled := ctx.Theme.Compacting.Render(fmt.Sprintf("%s Compacting… [%s]", frame, kind))
	return applyLineGutter(styled, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ============================================================================
// SyntheticBlock — pre-styled block injected by the UI (M6+ for the
// all-tasks-complete snapshot, etc.). Stored verbatim, rendered with
// the standard line gutter.
// ============================================================================

type SyntheticBlock struct {
	id   uint64
	rev  uint64
	text string
}

func newSyntheticBlock(text string) *SyntheticBlock {
	return &SyntheticBlock{id: allocID(), rev: 1, text: text}
}

func (b *SyntheticBlock) ID() uint64        { return b.id }
func (b *SyntheticBlock) Rev() uint64       { return b.rev }
func (b *SyntheticBlock) Kind() Kind        { return KindSynthetic }
func (b *SyntheticBlock) PlainText() string { return stripANSI(b.text) }

func (b *SyntheticBlock) Render(ctx RenderContext) string {
	return applyLineGutter(b.text, ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ============================================================================
// TurnEndBlock — faint separator between agent loop iterations.
// ============================================================================

// TurnEndBlock draws a barely-there horizontal rule between agent loop
// iterations so the user can scan the transcript and spot turn boundaries
// without the separator competing with real content.
type TurnEndBlock struct {
	id   uint64
	rev  uint64
	iter int // 0-indexed iteration from the agent loop
}

func newTurnEndBlock(iter int) *TurnEndBlock {
	return &TurnEndBlock{id: allocID(), rev: 1, iter: iter}
}

func (b *TurnEndBlock) ID() uint64        { return b.id }
func (b *TurnEndBlock) Rev() uint64       { return b.rev }
func (b *TurnEndBlock) Kind() Kind        { return KindSystem }
func (b *TurnEndBlock) PlainText() string { return "" }

func (b *TurnEndBlock) Render(ctx RenderContext) string {
	const margin = 4
	label := fmt.Sprintf(" # iter %d ", b.iter+1)
	dashCount := ctx.Width - margin - len(label)
	if dashCount < 4 {
		dashCount = 4
	}
	left := dashCount / 2
	right := dashCount - left
	line := strings.Repeat("─", left) + label + strings.Repeat("─", right)
	return applyLineGutter(ctx.Theme.TurnSep.Render(line), ctx.Width, ctx.Theme, ctx.Opts.Focused, len(ctx.Opts.Highlights) > 0)
}

// ============================================================================
// BgResultBlock — background task completion notification.
// ============================================================================

func newBgResultBlock(p *event.BgResultPayload) *SystemBlock {
	return &SystemBlock{
		id:   allocID(),
		rev:  1,
		text: fmt.Sprintf("task-%s %s", p.TaskID, p.Status),
		styleFn: func(th *theme.Theme) lipgloss.Style {
			return th.DimText
		},
	}
}

// ============================================================================
// MonitorEventBlock — streamed line from a running monitor.
// ============================================================================

func newMonitorEventBlock(p *event.MonitorEventPayload) *SystemBlock {
	var text string
	if p.Closing {
		text = fmt.Sprintf("monitor-%s closed", p.MonitorID)
	} else {
		line := p.Line
		if len(line) > 50 {
			line = line[:50] + "..."
		}
		text = fmt.Sprintf("monitor-%s: %s", p.MonitorID, line)
	}
	return &SystemBlock{
		id:   allocID(),
		rev:  1,
		text: text,
		styleFn: func(th *theme.Theme) lipgloss.Style {
			return th.DimText
		},
	}
}
