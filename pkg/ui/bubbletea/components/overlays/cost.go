package overlays

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// Cost is the /cost panel: a read-only breakdown of the session's
// cumulative token spend, priced at the current model's rate card. Like
// /mcp it snapshots its data at construction and has no actions —
// Esc / Enter / q close it. The always-on status bar shows the running
// total; this overlay shows where it came from.
type Cost struct {
	model string
	usage llm.Usage
}

// NewCost snapshots the controller's current model and cumulative usage.
// ctrl may be nil (pre-Attach), in which case it returns nil so the App
// hints "no controller attached" instead of opening an empty overlay.
func NewCost(ctrl ui.Controller) *Cost {
	if ctrl == nil {
		return nil
	}
	return &Cost{model: ctrl.Model(), usage: ctrl.Usage()}
}

func (c *Cost) Key() string  { return "cost" }
func (c *Cost) Modal() bool  { return true }
func (c *Cost) Hint() string { return "[Esc] close" }

func (c *Cost) Update(msg tea.Msg) (bool, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "ctrl+c", "enter", "q":
			return true, nil
		}
	}
	return false, nil
}

func (c *Cost) View(width int, th *theme.Theme) string {
	var b strings.Builder
	b.WriteString(th.PanelHeader.Render("▰ /COST — session spend"))
	b.WriteByte('\n')
	b.WriteString(th.DimText.Render("model · ") + th.StatusValue.Render(costModelOrDash(c.model)))
	b.WriteString("\n\n")

	pricing, priced := constant.MODEL_PRICING[constant.Model(c.model)]
	switch {
	case !priced:
		b.WriteString(th.DimText.Render("No rate card for this model — tokens shown, cost unpriced."))
		b.WriteString("\n\n")
		costTokenOnly(&b, th, c.usage)
	case pricing == (constant.Pricing{}):
		b.WriteString(th.ToolOK.Render("Local model — no per-token charge."))
		b.WriteString("\n\n")
		costTokenOnly(&b, th, c.usage)
	default:
		costPricedTable(&b, th, c.usage, pricing)
	}

	b.WriteString("\n\n")
	b.WriteString(th.DimText.Render("Rates are list-price estimates (USD per 1M tokens), not a billing statement."))
	b.WriteByte('\n')
	b.WriteString(th.FooterHint.Render("[Esc] close"))
	return th.InputBorder.Render(strings.TrimRight(b.String(), "\n"))
}

// costPricedTable renders the four-column breakdown (category / tokens /
// $·per·1M / cost). Cache rows appear only when the session actually
// accrued cache tokens (Anthropic / GLM); the input row shows the
// UNCACHED remainder so the token column sums to the total.
func costPricedTable(b *strings.Builder, th *theme.Theme, u llm.Usage, p constant.Pricing) {
	const perM = 1.0 / 1_000_000.0
	uncached := max(u.InputTokens-u.CacheReadTokens-u.CacheCreationTokens, 0)

	b.WriteString(costHeader(th))
	b.WriteByte('\n')
	b.WriteString(costRow(th, "input", uncached, costRate(p.Input), float64(uncached)*p.Input*perM, false))
	b.WriteByte('\n')
	if u.CacheReadTokens > 0 {
		b.WriteString(costRow(th, "cache read", u.CacheReadTokens, costRate(p.CacheRead), float64(u.CacheReadTokens)*p.CacheRead*perM, false))
		b.WriteByte('\n')
	}
	if u.CacheCreationTokens > 0 {
		b.WriteString(costRow(th, "cache write", u.CacheCreationTokens, costRate(p.CacheWrite), float64(u.CacheCreationTokens)*p.CacheWrite*perM, false))
		b.WriteByte('\n')
	}
	b.WriteString(costRow(th, "output", u.OutputTokens, costRate(p.Output), float64(u.OutputTokens)*p.Output*perM, false))
	b.WriteByte('\n')
	b.WriteString(costSep(th, 45))
	b.WriteByte('\n')
	total := p.CostUSD(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens)
	b.WriteString(costRow(th, "total", u.InputTokens+u.OutputTokens, "", total, true))
}

// costTokenOnly renders just the category / tokens columns — used when
// the model is unpriced (unknown) or free (local Ollama), so the rate /
// cost columns would be meaningless.
func costTokenOnly(b *strings.Builder, th *theme.Theme, u llm.Usage) {
	b.WriteString(costHeaderTokens(th))
	b.WriteByte('\n')
	b.WriteString(costRowTokens(th, "input", u.InputTokens, false))
	b.WriteByte('\n')
	b.WriteString(costRowTokens(th, "output", u.OutputTokens, false))
	b.WriteByte('\n')
	b.WriteString(costSep(th, 24))
	b.WriteByte('\n')
	b.WriteString(costRowTokens(th, "total", u.InputTokens+u.OutputTokens, true))
}

func costHeader(th *theme.Theme) string {
	return "  " + th.DimText.Render(fmt.Sprintf("%-11s%13s%9s%12s", "category", "tokens", "$/1M", "cost"))
}

func costHeaderTokens(th *theme.Theme) string {
	return "  " + th.DimText.Render(fmt.Sprintf("%-11s%13s", "category", "tokens"))
}

func costSep(th *theme.Theme, width int) string {
	return "  " + th.DimText.Render(strings.Repeat("─", width))
}

// costRow renders a full four-column row. Plain text is padded to width
// BEFORE styling so the ANSI escapes never throw off column alignment
// (same discipline as the /mcp panel). total=true highlights the label.
func costRow(th *theme.Theme, label string, tokens int, rate string, cost float64, total bool) string {
	lbl := th.PanelRow
	if total {
		lbl = th.StatusValue
	}
	return "  " +
		lbl.Render(fmt.Sprintf("%-11s", label)) +
		th.StatusValue.Render(fmt.Sprintf("%13s", costGroup(tokens))) +
		th.DimText.Render(fmt.Sprintf("%9s", rate)) +
		th.TasksDone.Render(fmt.Sprintf("%12s", costMoney(cost)))
}

// costRowTokens renders a row with the rate / cost columns blank.
func costRowTokens(th *theme.Theme, label string, tokens int, total bool) string {
	lbl := th.PanelRow
	if total {
		lbl = th.StatusValue
	}
	return "  " +
		lbl.Render(fmt.Sprintf("%-11s", label)) +
		th.StatusValue.Render(fmt.Sprintf("%13s", costGroup(tokens)))
}

// costRate formats a per-1M rate. Two decimals is enough for every rate
// in MODEL_PRICING; a zero rate (free) reads as "$0.00".
func costRate(r float64) string { return fmt.Sprintf("$%.2f", r) }

// costMoney formats a dollar amount with four decimals so sub-cent
// per-category costs (e.g. a cache-read line) stay visible.
func costMoney(usd float64) string { return fmt.Sprintf("$%.4f", usd) }

// costGroup renders an int with thousands separators: 1234567 → "1,234,567".
func costGroup(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// costModelOrDash returns "-" for an empty model id so the model cell
// never renders as "model · " with nothing after it.
func costModelOrDash(m string) string {
	if strings.TrimSpace(m) == "" {
		return "-"
	}
	return m
}
