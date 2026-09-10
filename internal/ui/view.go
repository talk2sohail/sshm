package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sohail/sshm/internal/fuzzy"
	"github.com/sohail/sshm/internal/probe"
	"github.com/sohail/sshm/internal/search"
)

// View renders the current frame.
func (m *Model) View() string {
	switch m.mode {
	case modeForm:
		return m.form.View(m.width)
	case modeHelp:
		return m.helpView()
	default:
		return m.listView()
	}
}

func (m *Model) listView() string {
	var b strings.Builder

	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.searchView())
	b.WriteString("\n\n")
	b.WriteString(m.rowsView())
	b.WriteString("\n")
	b.WriteString(m.detailView())

	if m.mode == modeConfirmDelete {
		if hit, ok := m.selected(); ok {
			b.WriteString("\n")
			b.WriteString(styleError.Render("Delete " + hit.Host.Alias + "? "))
			b.WriteString(styleHelp.Render("y / enter to confirm, any other key to cancel"))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

func (m *Model) headerView() string {
	left := styleTitle.Render("sshm")

	var right string
	switch {
	case m.store.Len() == 0:
		right = "no hosts yet"
	case len(m.hits) == m.store.Len():
		right = fmt.Sprintf("%d hosts", m.store.Len())
	default:
		right = fmt.Sprintf("%d of %d", len(m.hits), m.store.Len())
	}
	if m.probing {
		right += " · checking"
	}
	right = styleHeader.Render(right)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) searchView() string {
	return stylePrompt.Render("› ") + m.query.View()
}

// Column widths are derived from the terminal width so the layout stays
// readable from an 80-column window up to a full-screen one.
type columns struct {
	alias, target, tags, when int
}

func (m *Model) columns() columns {
	// 2 for the status glyph and its space, 3 gaps of 2 columns each.
	avail := m.width - 2 - 6
	if avail < 30 {
		avail = 30
	}
	c := columns{
		alias:  clamp(avail*30/100, 12, 28),
		target: clamp(avail*38/100, 16, 44),
		tags:   clamp(avail*20/100, 0, 28),
		when:   clamp(avail*12/100, 0, 12),
	}
	return c
}

func (m *Model) rowsView() string {
	if m.store.Len() == 0 {
		return styleHelp.Render(
			"  No saved hosts yet.\n\n" +
				"  Press ctrl+n to add one, or run `sshm import` to pull in\n" +
				"  the hosts already in your ~/.ssh/config.")
	}
	if len(m.hits) == 0 {
		return styleHelp.Render("  Nothing matches " + strconv.Quote(m.query.Value()) +
			"\n  ctrl+n adds it as a new host, esc clears the search.")
	}

	c := m.columns()
	q := search.ParseQuery(m.query.Value())
	lo, hi := m.visibleRange()

	var b strings.Builder
	for i := lo; i < hi; i++ {
		b.WriteString(m.rowView(m.hits[i], i == m.cursor, c, q))
		b.WriteString("\n")
	}

	// Pad to a stable height so the detail pane does not jump around as the
	// result count changes while typing.
	for i := hi - lo; i < m.rowsAvailable(); i++ {
		b.WriteString("\n")
	}

	if hi < len(m.hits) {
		b.WriteString(styleHelp.Render(fmt.Sprintf("  … %d more", len(m.hits)-hi)))
	}
	return b.String()
}

func (m *Model) rowView(hit search.Hit, selected bool, c columns, q search.Query) string {
	var b strings.Builder

	b.WriteString(m.statusGlyph(hit.Host.Alias))
	b.WriteString(" ")

	if selected {
		b.WriteString(styleAliasSelected.Render("▸"))
	} else {
		b.WriteString(" ")
	}

	aliasStyle := styleAlias
	if selected {
		aliasStyle = styleAliasSelected
	}

	// Highlight only the field that actually matched, so the user can see why
	// a row is in the list.
	alias := pad(hit.Host.Alias, c.alias)
	if hit.Field == search.FieldAlias {
		b.WriteString(truncateHighlighted(alias, hit.Highlight(q), c.alias, aliasStyle))
	} else {
		b.WriteString(aliasStyle.Render(alias))
	}
	b.WriteString("  ")

	target := pad(hit.Host.Target(), c.target)
	if hit.Field == search.FieldHostname || hit.Field == search.FieldUser {
		// The hit's own offsets index the hostname or the user in isolation,
		// but this column shows "user@hostname". Re-match against the rendered
		// string so the underline lands on the right characters.
		b.WriteString(truncateHighlighted(target, fuzzy.Positions(q.Text, target), c.target, styleTarget))
	} else {
		b.WriteString(styleTarget.Render(target))
	}

	if c.tags > 0 {
		b.WriteString("  ")
		b.WriteString(styleTag.Render(pad(strings.Join(hit.Host.Tags, " "), c.tags)))
	}
	if c.when > 0 {
		b.WriteString("  ")
		b.WriteString(styleWhen.Render(pad(m.lastUsed(hit.Host.Alias), c.when)))
	}
	return strings.TrimRight(b.String(), " ")
}

func (m *Model) statusGlyph(alias string) string {
	r, ok := m.status[alias]
	if !ok {
		return styleUnknown.Render(glyphUnknown)
	}
	switch r.Status {
	case probe.Up:
		return styleUp.Render(glyphUp)
	case probe.Down:
		return styleDown.Render(glyphDown)
	case probe.Checking:
		return styleUnknown.Render(glyphPending)
	default:
		return styleUnknown.Render(glyphUnknown)
	}
}

func (m *Model) lastUsed(alias string) string {
	t, ok := m.hist.LastUsed(alias)
	if !ok {
		return ""
	}
	return humanAge(m.now().Sub(t))
}

// detailView describes the selected host: the exact command that will run, its
// reachability, and anything wrong with its key file.
func (m *Model) detailView() string {
	hit, ok := m.selected()
	if !ok {
		return ""
	}
	h := hit.Host

	var b strings.Builder
	b.WriteString(styleDivider.Render(strings.Repeat("─", max(10, m.width))))
	b.WriteString("\n")

	if h.Description != "" {
		b.WriteString(styleHeader.Render(truncate(h.Description, m.width)))
		b.WriteString("\n")
	}

	b.WriteString(styleCommand.Render(truncate(h.CommandLine(nil), m.width)))
	b.WriteString("\n")

	var facts []string
	if r, ok := m.status[h.Alias]; ok {
		switch r.Status {
		case probe.Up:
			facts = append(facts, styleOK.Render(fmt.Sprintf("reachable in %dms", r.Latency.Milliseconds())))
		case probe.Down:
			facts = append(facts, styleError.Render("port "+fmt.Sprint(h.EffectivePort())+" unreachable"))
		case probe.Checking:
			facts = append(facts, styleHelp.Render("checking…"))
		}
	}
	if n := m.hist.Count(h.Alias); n > 0 {
		if when := m.lastUsed(h.Alias); when != "" {
			facts = append(facts, styleHelp.Render(fmt.Sprintf("used %s, %s", plural(n, "time"), when)))
		}
	}
	if p := h.CheckIdentity(); p != nil {
		// The message already names the key, so it needs no prefix here.
		facts = append(facts, styleWarning.Render(p.Message))
	}
	if len(facts) > 0 {
		b.WriteString(truncate(strings.Join(facts, styleDivider.Render(" · ")), m.width*2))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) footerView() string {
	if m.message != "" {
		if m.msgErr {
			return styleError.Render("✗ " + truncate(m.message, m.width-2))
		}
		return styleOK.Render("✓ " + truncate(m.message, m.width-2))
	}
	return helpLine([][2]string{
		{"enter", "connect"},
		{"^n", "new"},
		{"^e", "edit"},
		{"^r", "recheck"},
		{"^g", "help"},
		{"esc", "quit"},
	})
}

func helpLine(pairs [][2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, styleHelpKey.Render(p[0])+styleHelp.Render(" "+p[1]))
	}
	return strings.Join(parts, styleHelp.Render("  ·  "))
}

func (m *Model) helpView() string {
	rows := [][2]string{
		{"type anything", "fuzzy search alias, hostname, tags, user and note"},
		{"#tag", "show only hosts with that tag"},
		{"!#tag", "hide hosts with that tag"},
		{"", ""},
		{"enter", "connect to the selected host"},
		{"↑ ↓ / ^k ^j", "move the selection"},
		{"pgup pgdn", "move a page at a time"},
		{"esc", "clear the search, or quit when it is empty"},
		{"ctrl+c", "quit"},
		{"", ""},
		{"ctrl+n", "add a new host"},
		{"ctrl+e", "edit the selected host"},
		{"ctrl+x", "delete the selected host"},
		{"ctrl+y", "copy the ssh command to the clipboard"},
		{"ctrl+r", "re-check reachability of the visible hosts"},
		{"ctrl+g", "this help"},
		{"", ""},
		{"in the search box", "ctrl+a start · end · ctrl+u clear · ctrl+w delete word"},
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("sshm — keys"))
	b.WriteString("\n\n")
	for _, r := range rows {
		if r[0] == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  ")
		b.WriteString(styleHelpKey.Render(pad(r[0], 18)))
		b.WriteString(styleHelp.Render(r[1]))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleHeader.Render("  hosts:   " + m.store.Path()))
	b.WriteString("\n")
	b.WriteString(styleHeader.Render("  history: " + m.hist.Path()))
	b.WriteString("\n\n")
	b.WriteString(styleHelp.Render("  press any key to go back"))
	return b.String()
}

// humanAge renders a duration the way a person would say it.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
