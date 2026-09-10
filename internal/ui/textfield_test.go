package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeInto(f *textField, s string) {
	for _, r := range s {
		if r == ' ' {
			f.Update(tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestTextFieldTypingAndReporting(t *testing.T) {
	f := newTextField("hint", 20)
	if changed := f.Update(runes("a")); !changed {
		t.Error("typing should report a change")
	}
	typeInto(&f, "bc")
	if f.Value() != "abc" {
		t.Errorf("value = %q, want abc", f.Value())
	}
	if changed := f.Update(tea.KeyMsg{Type: tea.KeyLeft}); changed {
		t.Error("cursor movement is not a value change")
	}
}

func TestTextFieldInsertAtCursor(t *testing.T) {
	f := newTextField("", 20)
	typeInto(&f, "ac")
	f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	typeInto(&f, "b")
	if f.Value() != "abc" {
		t.Errorf("value = %q, want abc", f.Value())
	}
}

func TestTextFieldBackspaceAndDelete(t *testing.T) {
	f := newTextField("", 20)
	typeInto(&f, "abcd")
	f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.Value() != "abc" {
		t.Errorf("after backspace: %q", f.Value())
	}

	f.Update(tea.KeyMsg{Type: tea.KeyHome})
	f.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if f.Value() != "bc" {
		t.Errorf("after delete: %q", f.Value())
	}

	// Backspace at the start and delete at the end must be no-ops, not panics.
	f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	f.Update(tea.KeyMsg{Type: tea.KeyEnd})
	f.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if f.Value() != "bc" {
		t.Errorf("value = %q, want bc", f.Value())
	}
}

func TestTextFieldKillLineAndWord(t *testing.T) {
	f := newTextField("", 40)
	typeInto(&f, "hello world")

	f.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if f.Value() != "hello " {
		t.Errorf("ctrl+w gave %q, want %q", f.Value(), "hello ")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if f.Value() != "" {
		t.Errorf("ctrl+u gave %q, want empty", f.Value())
	}
	if f.CursorPos() != 0 {
		t.Errorf("cursor = %d, want 0", f.CursorPos())
	}
}

func TestTextFieldCtrlUKeepsTextAfterCursor(t *testing.T) {
	f := newTextField("", 40)
	typeInto(&f, "abcdef")
	for i := 0; i < 3; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if f.Value() != "def" {
		t.Errorf("value = %q, want def", f.Value())
	}
}

func TestTextFieldRejectsControlCharacters(t *testing.T) {
	f := newTextField("", 20)
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0x1b, 'a', 0x07, 'b', 0x7f}})
	if f.Value() != "ab" {
		t.Errorf("value = %q, want ab; control characters must be dropped", f.Value())
	}
}

func TestTextFieldCursorBounds(t *testing.T) {
	f := newTextField("", 20)
	typeInto(&f, "ab")
	for i := 0; i < 10; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	if f.CursorPos() != 0 {
		t.Errorf("cursor = %d, want 0", f.CursorPos())
	}
	for i := 0; i < 10; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if f.CursorPos() != 2 {
		t.Errorf("cursor = %d, want 2", f.CursorPos())
	}
}

func TestTextFieldMultibyte(t *testing.T) {
	f := newTextField("", 20)
	typeInto(&f, "héllo")
	if f.Value() != "héllo" {
		t.Fatalf("value = %q", f.Value())
	}
	if f.CursorPos() != 5 {
		t.Errorf("cursor = %d, want 5 runes (not bytes)", f.CursorPos())
	}
	f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.Value() != "héll" {
		t.Errorf("value = %q, want héll", f.Value())
	}
}

func TestTextFieldSetValueAndClear(t *testing.T) {
	f := newTextField("", 20)
	f.SetValue("preset")
	if f.Value() != "preset" || f.CursorPos() != 6 {
		t.Errorf("value = %q cursor = %d", f.Value(), f.CursorPos())
	}
	f.Clear()
	if !f.Empty() || f.CursorPos() != 0 {
		t.Errorf("clear left value = %q cursor = %d", f.Value(), f.CursorPos())
	}
}

// A field narrower than its content must scroll rather than panic, at any
// cursor position.
func TestTextFieldScrollsWithoutPanicking(t *testing.T) {
	for _, w := range []int{1, 2, 5, 80} {
		f := newTextField("hint", w)
		f.Focus()
		typeInto(&f, "the-quick-brown-fox-jumps-over-the-lazy-dog")
		_ = f.View()

		for i := 0; i < 60; i++ {
			f.Update(tea.KeyMsg{Type: tea.KeyLeft})
			_ = f.View()
		}
		for i := 0; i < 60; i++ {
			f.Update(tea.KeyMsg{Type: tea.KeyRight})
			_ = f.View()
		}
	}
}

func TestTextFieldViewWithZeroWidth(t *testing.T) {
	f := newTextField("hint", 0)
	f.Focus()
	typeInto(&f, "abc")
	_ = f.View() // must not panic
	f.SetWidth(-5)
	_ = f.View()
}

func TestTextFieldViewShowsItsValue(t *testing.T) {
	f := newTextField("type here", 20)
	f.Focus()
	typeInto(&f, "prod-api")
	// The cursor sits past the end here, so the whole value is plain text.
	if got := f.View(); !strings.Contains(got, "prod-api") {
		t.Errorf("View = %q, want it to contain the typed value", got)
	}
}

func TestTextFieldViewScrollsToTheCursor(t *testing.T) {
	f := newTextField("", 6)
	f.Focus()
	typeInto(&f, "abcdefghij")
	// Only the tail is visible, so the start must have scrolled off.
	got := f.View()
	if strings.Contains(got, "abc") {
		t.Errorf("View = %q, expected the start to be scrolled out of view", got)
	}
	if !strings.Contains(got, "fgh") {
		t.Errorf("View = %q, expected the text around the cursor", got)
	}
}
