package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Palette. Adaptive colours are used throughout so the tool is legible on both
// light and dark terminals without the user configuring anything.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0550AE", Dark: "#7AA2F7"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#787C99"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#8C959F", Dark: "#565A6E"}
	colUp     = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#9ECE6A"}
	colDown   = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F7768E"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E0AF68"}
	colTag    = lipgloss.AdaptiveColor{Light: "#8250DF", Dark: "#BB9AF7"}
	colText   = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#C0CAF5"}
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleHeader = lipgloss.NewStyle().Foreground(colMuted)

	stylePrompt      = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stylePlaceholder = lipgloss.NewStyle().Foreground(colFaint)
	styleCursor      = lipgloss.NewStyle().Reverse(true)

	styleAlias         = lipgloss.NewStyle().Foreground(colText)
	styleAliasSelected = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleMatch         = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Underline(true)
	styleTarget        = lipgloss.NewStyle().Foreground(colMuted)
	styleTag           = lipgloss.NewStyle().Foreground(colTag)
	styleWhen          = lipgloss.NewStyle().Foreground(colFaint)

	styleUp      = lipgloss.NewStyle().Foreground(colUp)
	styleDown    = lipgloss.NewStyle().Foreground(colDown)
	styleUnknown = lipgloss.NewStyle().Foreground(colFaint)

	styleHelp    = lipgloss.NewStyle().Foreground(colFaint)
	styleHelpKey = lipgloss.NewStyle().Foreground(colMuted).Bold(true)

	styleError   = lipgloss.NewStyle().Foreground(colDown).Bold(true)
	styleWarning = lipgloss.NewStyle().Foreground(colWarn)
	styleOK      = lipgloss.NewStyle().Foreground(colUp)

	styleDivider   = lipgloss.NewStyle().Foreground(colFaint)
	styleFieldName = lipgloss.NewStyle().Foreground(colMuted)
	styleFocused   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleCommand   = lipgloss.NewStyle().Foreground(colMuted).Italic(true)
)

// Status glyphs. Each state gets a distinct shape, not just a distinct colour,
// so the list still reads correctly for colour-blind users and on terminals
// with a mangled palette. All four are single-width.
const (
	glyphUp      = "●" // port open
	glyphDown    = "×" // refused or timed out
	glyphPending = "◌" // probe in flight
	glyphUnknown = "·" // not checked
)

// truncate shortens s to at most width display cells, adding an ellipsis when
// it cuts. Width is counted in runes, which is right for the ASCII-dominated
// content here and degrades gracefully otherwise.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

// pad right-pads s with spaces to exactly width cells, truncating if needed.
func pad(s string, width int) string {
	s = truncate(s, width)
	n := width - utf8.RuneCountInString(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

// highlight renders text with the byte offsets in positions emphasised. It is
// what makes it obvious why a given host matched the query.
func highlight(text string, positions []int, base lipgloss.Style) string {
	if len(positions) == 0 {
		return base.Render(text)
	}
	set := make(map[int]struct{}, len(positions))
	for _, p := range positions {
		set[p] = struct{}{}
	}

	var b strings.Builder
	var run strings.Builder
	runHighlighted := false

	flush := func() {
		if run.Len() == 0 {
			return
		}
		if runHighlighted {
			b.WriteString(styleMatch.Render(run.String()))
		} else {
			b.WriteString(base.Render(run.String()))
		}
		run.Reset()
	}

	for i, r := range text {
		_, hit := set[i]
		if hit != runHighlighted {
			flush()
			runHighlighted = hit
		}
		run.WriteRune(r)
	}
	flush()
	return b.String()
}

// truncateHighlighted truncates text to width before highlighting, dropping any
// positions that fall outside the visible part.
func truncateHighlighted(text string, positions []int, width int, base lipgloss.Style) string {
	if utf8.RuneCountInString(text) <= width {
		return highlight(text, positions, base)
	}
	cut := truncate(text, width)
	limit := len(cut)
	kept := positions[:0:0]
	for _, p := range positions {
		if p < limit {
			kept = append(kept, p)
		}
	}
	return highlight(cut, kept, base)
}
