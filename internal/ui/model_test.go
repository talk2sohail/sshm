package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/talk2sohail/sshm/internal/frecency"
	"github.com/talk2sohail/sshm/internal/model"
	"github.com/talk2sohail/sshm/internal/probe"
	"github.com/talk2sohail/sshm/internal/store"
)

// newTestModel builds a model over a temporary store so tests never touch the
// user's real config.
func newTestModel(t *testing.T, hosts ...model.Host) *Model {
	t.Helper()
	dir := t.TempDir()
	st := store.New(filepath.Join(dir, "hosts.toml"))
	for _, h := range hosts {
		if err := st.Add(h); err != nil {
			t.Fatalf("seeding %s: %v", h.Alias, err)
		}
	}
	hist := frecency.New(filepath.Join(dir, "history.json"))

	m := New(Options{Store: st, History: hist})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func seed() []model.Host {
	return []model.Host{
		{Alias: "prod-api", Hostname: "10.0.4.21", User: "ubuntu", Tags: []string{"prod", "aws"}},
		{Alias: "prod-db", Hostname: "10.0.4.30", User: "postgres", Tags: []string{"prod", "db"}},
		{Alias: "staging-api", Hostname: "10.9.0.5", User: "ubuntu", Tags: []string{"staging"}},
		{Alias: "home-nas", Hostname: "192.168.1.10", Tags: []string{"home"}},
	}
}

// typeString feeds a string into the model one key at a time, the way a real
// user would.
func typeString(m *Model, s string) {
	for _, r := range s {
		if r == ' ' {
			m.Update(tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func key(m *Model, t tea.KeyType) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: t})
	return cmd
}

func visibleAliases(m *Model) []string {
	out := make([]string, 0, len(m.hits))
	for _, h := range m.hits {
		out = append(out, h.Host.Alias)
	}
	return out
}

func TestTypingFilters(t *testing.T) {
	m := newTestModel(t, seed()...)
	if len(m.hits) != 4 {
		t.Fatalf("expected all 4 hosts initially, got %v", visibleAliases(m))
	}

	typeString(m, "prod")
	got := visibleAliases(m)
	if len(got) != 2 {
		t.Fatalf("after typing 'prod' got %v, want 2 hosts", got)
	}
	for _, a := range got {
		if !strings.HasPrefix(a, "prod") {
			t.Errorf("unexpected host %q in results %v", a, got)
		}
	}
}

func TestTagFilterFromSearchBar(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "#db")
	if got := visibleAliases(m); len(got) != 1 || got[0] != "prod-db" {
		t.Errorf("got %v, want [prod-db]", got)
	}
}

func TestBackspaceWidensResults(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "prod-db")
	if len(m.hits) != 1 {
		t.Fatalf("expected 1 hit, got %v", visibleAliases(m))
	}
	for i := 0; i < 3; i++ {
		key(m, tea.KeyBackspace)
	}
	if len(m.hits) < 2 {
		t.Errorf("after backspacing got %v, expected the list to widen", visibleAliases(m))
	}
}

func TestEscClearsQueryBeforeQuitting(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "prod")

	key(m, tea.KeyEsc)
	if m.query.Value() != "" {
		t.Errorf("first esc should clear the query, got %q", m.query.Value())
	}
	if len(m.hits) != 4 {
		t.Errorf("clearing the query should restore all hosts, got %v", visibleAliases(m))
	}
	if m.Launch != nil {
		t.Error("esc must not select a host")
	}
}

func TestCursorMovementAndClamping(t *testing.T) {
	m := newTestModel(t, seed()...)

	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}
	key(m, tea.KeyUp)
	if m.cursor != 0 {
		t.Errorf("cursor went to %d above the top", m.cursor)
	}
	for i := 0; i < 20; i++ {
		key(m, tea.KeyDown)
	}
	if m.cursor != len(m.hits)-1 {
		t.Errorf("cursor = %d, want %d (clamped to the last row)", m.cursor, len(m.hits)-1)
	}
	key(m, tea.KeyCtrlK)
	if m.cursor != len(m.hits)-2 {
		t.Errorf("ctrl+k should move up, cursor = %d", m.cursor)
	}
	key(m, tea.KeyCtrlJ)
	if m.cursor != len(m.hits)-1 {
		t.Errorf("ctrl+j should move down, cursor = %d", m.cursor)
	}
}

func TestCursorClampsWhenResultsShrink(t *testing.T) {
	m := newTestModel(t, seed()...)
	for i := 0; i < 3; i++ {
		key(m, tea.KeyDown)
	}
	typeString(m, "prod-db") // narrows to a single hit
	if m.cursor >= len(m.hits) {
		t.Fatalf("cursor %d is out of range for %d hits", m.cursor, len(m.hits))
	}
	if _, ok := m.selected(); !ok {
		t.Error("selection should still be valid after the list shrank")
	}
}

func TestEnterSelectsHostAndRecordsHistory(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "nas")

	key(m, tea.KeyEnter)

	if m.Launch == nil {
		t.Fatal("enter should set Launch")
	}
	if m.Launch.Alias != "home-nas" {
		t.Errorf("launched %q, want home-nas", m.Launch.Alias)
	}
	if m.hist.Count("home-nas") != 1 {
		t.Errorf("history count = %d, want 1", m.hist.Count("home-nas"))
	}
}

func TestEnterOnEmptyResultsDoesNotLaunch(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "zzzzzzz")
	if len(m.hits) != 0 {
		t.Fatalf("expected no hits, got %v", visibleAliases(m))
	}
	key(m, tea.KeyEnter)
	if m.Launch != nil {
		t.Fatalf("launched %q with no matching host", m.Launch.Alias)
	}
	if m.message == "" {
		t.Error("expected a message explaining nothing is selected")
	}
}

func TestFrecencyLiftsRepeatedlyUsedHost(t *testing.T) {
	m := newTestModel(t, seed()...)
	now := time.Now()
	for i := 0; i < 10; i++ {
		m.hist.Record("home-nas", now)
	}
	m.refilter()
	if got := visibleAliases(m); got[0] != "home-nas" {
		t.Errorf("order = %v, want home-nas first", got)
	}
}

func TestAddHostThroughTheForm(t *testing.T) {
	m := newTestModel(t)

	key(m, tea.KeyCtrlN)
	if m.mode != modeForm {
		t.Fatal("ctrl+n should open the form")
	}

	typeString(m, "new-box")
	key(m, tea.KeyTab)
	typeString(m, "10.1.2.3")
	key(m, tea.KeyCtrlS)

	if m.mode != modeList {
		t.Fatalf("form should have closed, mode = %v", m.mode)
	}
	if m.store.Len() != 1 {
		t.Fatalf("store has %d hosts, want 1", m.store.Len())
	}
	h, _, ok := m.store.ByAlias("new-box")
	if !ok {
		t.Fatal("new-box was not saved")
	}
	if h.Hostname != "10.1.2.3" {
		t.Errorf("hostname = %q", h.Hostname)
	}
	if len(m.hits) != 1 {
		t.Errorf("the new host should appear in the list, got %v", visibleAliases(m))
	}
}

func TestFormSeedsAliasFromTheSearchQuery(t *testing.T) {
	m := newTestModel(t)
	typeString(m, "web-01")
	key(m, tea.KeyCtrlN)
	if got := m.form.fields[fAlias].Value(); got != "web-01" {
		t.Errorf("alias field = %q, want the typed query", got)
	}
}

func TestFormRejectsInvalidHostAndStaysOpen(t *testing.T) {
	m := newTestModel(t)
	key(m, tea.KeyCtrlN)
	typeString(m, "no-hostname")
	key(m, tea.KeyCtrlS)

	if m.mode != modeForm {
		t.Fatal("form should stay open when the host is invalid")
	}
	if m.form.err == nil {
		t.Error("expected a validation error to be shown")
	}
	if m.store.Len() != 0 {
		t.Error("invalid host must not be saved")
	}
}

func TestFormRejectsBadPort(t *testing.T) {
	m := newTestModel(t)
	key(m, tea.KeyCtrlN)
	typeString(m, "box")
	key(m, tea.KeyTab)
	typeString(m, "10.0.0.1")
	key(m, tea.KeyTab)
	key(m, tea.KeyTab)
	typeString(m, "99999")
	key(m, tea.KeyCtrlS)

	if m.form.err == nil {
		t.Fatal("expected a port error")
	}
	if !strings.Contains(m.form.err.Error(), "65535") {
		t.Errorf("error = %v, want it to mention the valid range", m.form.err)
	}
}

func TestFormEscCancelsWithoutSaving(t *testing.T) {
	m := newTestModel(t)
	key(m, tea.KeyCtrlN)
	typeString(m, "throwaway")
	key(m, tea.KeyEsc)

	if m.mode != modeList {
		t.Error("esc should close the form")
	}
	if m.store.Len() != 0 {
		t.Error("cancelled form must not save")
	}
}

func TestEditRenamesAndCarriesHistoryOver(t *testing.T) {
	m := newTestModel(t, model.Host{Alias: "old", Hostname: "10.0.0.1"})
	m.hist.Record("old", time.Now())

	key(m, tea.KeyCtrlE)
	if m.mode != modeForm {
		t.Fatal("ctrl+e should open the form")
	}
	// Replace the alias.
	m.form.fields[fAlias].SetValue("renamed")
	key(m, tea.KeyCtrlS)

	if _, _, ok := m.store.ByAlias("renamed"); !ok {
		t.Fatal("rename did not persist")
	}
	if _, _, ok := m.store.ByAlias("old"); ok {
		t.Error("old alias should be gone")
	}
	if m.hist.Count("renamed") != 1 {
		t.Errorf("history did not follow the rename: count = %d", m.hist.Count("renamed"))
	}
}

func TestEditRejectsDuplicateAlias(t *testing.T) {
	m := newTestModel(t,
		model.Host{Alias: "aaa", Hostname: "10.0.0.1"},
		model.Host{Alias: "bbb", Hostname: "10.0.0.2"},
	)
	key(m, tea.KeyCtrlE) // edits whichever is selected
	orig := m.form.origAlias
	other := "bbb"
	if orig == "bbb" {
		other = "aaa"
	}
	m.form.fields[fAlias].SetValue(other)
	key(m, tea.KeyCtrlS)

	if m.mode != modeForm {
		t.Error("form should stay open on a duplicate alias")
	}
	if m.form.err == nil {
		t.Error("expected a duplicate-alias error")
	}
	if m.store.Len() != 2 {
		t.Errorf("store has %d hosts, want 2", m.store.Len())
	}
}

func TestDeleteRequiresConfirmation(t *testing.T) {
	m := newTestModel(t, seed()...)
	before := m.store.Len()

	key(m, tea.KeyCtrlX)
	if m.mode != modeConfirmDelete {
		t.Fatal("ctrl+x should ask for confirmation")
	}

	// Any other key cancels.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.mode != modeList {
		t.Error("expected to return to the list")
	}
	if m.store.Len() != before {
		t.Errorf("host was deleted without confirmation: %d -> %d", before, m.store.Len())
	}
}

func TestDeleteConfirmedRemovesHostAndHistory(t *testing.T) {
	m := newTestModel(t, seed()...)
	m.hist.Record(m.hits[0].Host.Alias, time.Now())
	target := m.hits[0].Host.Alias
	before := m.store.Len()

	key(m, tea.KeyCtrlX)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if m.store.Len() != before-1 {
		t.Errorf("store has %d hosts, want %d", m.store.Len(), before-1)
	}
	if _, _, ok := m.store.ByAlias(target); ok {
		t.Errorf("%s is still in the store", target)
	}
	if m.hist.Count(target) != 0 {
		t.Errorf("history for %s survived the delete", target)
	}
}

func TestHelpOpensAndAnyKeyCloses(t *testing.T) {
	m := newTestModel(t, seed()...)
	key(m, tea.KeyCtrlG)
	if m.mode != modeHelp {
		t.Fatal("ctrl+g should open help")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.mode != modeList {
		t.Error("any key should close help")
	}
}

func TestCommandKeysAreNotTypedIntoTheSearchBox(t *testing.T) {
	m := newTestModel(t, seed()...)
	for _, k := range []tea.KeyType{tea.KeyCtrlR, tea.KeyCtrlY, tea.KeyCtrlJ, tea.KeyCtrlK} {
		key(m, k)
	}
	if got := m.query.Value(); got != "" {
		t.Errorf("query = %q, want empty; command keys leaked into the search box", got)
	}
}

// View must never panic, at any terminal size, in any mode, with or without
// results. A crash here takes the user's terminal with it.
func TestViewDoesNotPanic(t *testing.T) {
	sizes := []struct{ w, h int }{
		{100, 30}, {80, 24}, {40, 12}, {20, 6}, {10, 3}, {1, 1}, {0, 0}, {200, 60},
	}
	queries := []string{"", "prod", "#aws", "!#prod", "zzzz", "  ", "#"}

	for _, sz := range sizes {
		for _, q := range queries {
			m := newTestModel(t, seed()...)
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			typeString(m, q)
			_ = m.View()

			key(m, tea.KeyCtrlN)
			_ = m.View()
			key(m, tea.KeyEsc)

			key(m, tea.KeyCtrlG)
			_ = m.View()
			key(m, tea.KeyEsc)

			key(m, tea.KeyCtrlX)
			_ = m.View()
			key(m, tea.KeyEsc)
		}
	}

	// Also with an entirely empty store.
	m := newTestModel(t)
	for _, sz := range sizes {
		m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		_ = m.View()
	}
}

func TestScrollingKeepsCursorVisible(t *testing.T) {
	hosts := make([]model.Host, 50)
	for i := range hosts {
		hosts[i] = model.Host{
			Alias:    "host-" + string(rune('a'+i/26)) + string(rune('a'+i%26)),
			Hostname: "10.0.0.1",
		}
	}
	m := newTestModel(t, hosts...)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})

	for i := 0; i < 49; i++ {
		key(m, tea.KeyDown)
		lo, hi := m.visibleRange()
		if m.cursor < lo || m.cursor >= hi {
			t.Fatalf("cursor %d outside visible range [%d,%d)", m.cursor, lo, hi)
		}
	}
	for i := 0; i < 49; i++ {
		key(m, tea.KeyUp)
		lo, hi := m.visibleRange()
		if m.cursor < lo || m.cursor >= hi {
			t.Fatalf("cursor %d outside visible range [%d,%d)", m.cursor, lo, hi)
		}
	}
}

// Every command chord the footer and help screen advertise must actually be
// handled by the list view. It is easy to document a key and forget to wire it,
// or to rename one in only one of the two places.
func TestEveryDocumentedCommandKeyIsHandled(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyType
		want func(m *Model) bool
	}{
		{"ctrl+n opens the add form", tea.KeyCtrlN, func(m *Model) bool { return m.mode == modeForm }},
		{"ctrl+e opens the edit form", tea.KeyCtrlE, func(m *Model) bool { return m.mode == modeForm }},
		{"ctrl+x asks to confirm a delete", tea.KeyCtrlX, func(m *Model) bool { return m.mode == modeConfirmDelete }},
		{"ctrl+g opens help", tea.KeyCtrlG, func(m *Model) bool { return m.mode == modeHelp }},
	}
	for _, c := range cases {
		m := newTestModel(t, seed()...)
		key(m, c.key)
		if !c.want(m) {
			t.Errorf("%s: did not happen (mode = %v)", c.name, m.mode)
		}
	}

	// ctrl+y does not change mode, but it must still hand back work to do
	// rather than being silently dropped.
	m := newTestModel(t, seed()...)
	if cmd := key(m, tea.KeyCtrlY); cmd == nil {
		t.Error("ctrl+y returned no command")
	}

	// ctrl+r only has anything to do when a prober is configured, so give it
	// one and check the visible rows actually went back to "checking".
	p := newTestModel(t, seed()...)
	p.prober = probe.New(probe.DefaultTimeout, probe.DefaultConcurrency, probe.DefaultTTL)
	if cmd := key(p, tea.KeyCtrlR); cmd == nil {
		t.Error("ctrl+r returned no command")
	}
	if got := p.status["prod-api"].Status; got != probe.Checking {
		t.Errorf("ctrl+r left prod-api at %v, want %v", got, probe.Checking)
	}
}

// Movement has to work through both the arrow keys and the fzf-style chords,
// because the two arrive by completely different routes: arrows are escape
// sequences on Unix and virtual key codes on the Windows console, while ^j/^k
// are plain control characters everywhere.
func TestMovementBindingsAgree(t *testing.T) {
	for _, pair := range []struct {
		down, up tea.KeyType
		label    string
	}{
		{tea.KeyDown, tea.KeyUp, "arrows"},
		{tea.KeyCtrlJ, tea.KeyCtrlK, "ctrl+j/ctrl+k"},
	} {
		m := newTestModel(t, seed()...)
		key(m, pair.down)
		key(m, pair.down)
		if m.cursor != 2 {
			t.Errorf("%s: cursor = %d after two downs, want 2", pair.label, m.cursor)
		}
		key(m, pair.up)
		if m.cursor != 1 {
			t.Errorf("%s: cursor = %d after an up, want 1", pair.label, m.cursor)
		}
	}
}

// Terminals and the Windows console both deliver Tab, Enter and Backspace as
// the control characters Ctrl+I, Ctrl+M and Ctrl+H. Binding a command to one of
// those makes that command fire when the user presses Tab or Enter -- and on
// Windows, where bubbletea decodes chords from the console event's character,
// there is no way to tell them apart. Ctrl+[ is Esc for the same reason. None of
// these may be used as a command key.
func TestCommandKeysAvoidChordsTerminalsReserve(t *testing.T) {
	reserved := map[tea.KeyType]string{
		tea.KeyCtrlI:           "Tab",
		tea.KeyCtrlM:           "Enter",
		tea.KeyCtrlH:           "Backspace",
		tea.KeyCtrlOpenBracket: "Esc",
	}
	for k, why := range reserved {
		m := newTestModel(t, seed()...)
		before := m.mode
		key(m, k)
		if m.mode != before {
			t.Errorf("%v is how terminals send %s and must not be a command key; "+
				"it changed the mode to %v", k, why, m.mode)
		}
		if got := m.query.Value(); got != "" {
			t.Errorf("%v (%s) inserted %q into the search box", k, why, got)
		}
	}
}

// Ctrl+C quits from the list, and the search box must not swallow it: it is the
// only binding a user is guaranteed to reach for when something is wrong.
func TestCtrlCAlwaysQuits(t *testing.T) {
	m := newTestModel(t, seed()...)
	typeString(m, "prod")
	if cmd := key(m, tea.KeyCtrlC); cmd == nil {
		t.Fatal("ctrl+c returned no command")
	}
	if m.Launch != nil {
		t.Error("ctrl+c must not launch a connection")
	}
}
