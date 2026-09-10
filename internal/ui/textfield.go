package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// textField is a single-line editable field.
//
// It is hand-rolled rather than pulled from bubbles/textinput for two reasons:
// the behaviour needed here is small and fully specified, and it keeps the
// dependency list short enough that the whole tool builds from three modules.
// Everything is measured in runes, so multi-byte input behaves correctly.
type textField struct {
	value       []rune
	cursor      int // rune index, 0..len(value)
	offset      int // first visible rune, for fields narrower than their text
	width       int // visible width in cells
	placeholder string
	focused     bool
}

func newTextField(placeholder string, width int) textField {
	return textField{placeholder: placeholder, width: width}
}

func (t *textField) SetValue(s string) {
	t.value = []rune(s)
	t.cursor = len(t.value)
	t.clampOffset()
}

func (t textField) Value() string { return string(t.value) }

func (t textField) Empty() bool { return len(t.value) == 0 }

func (t *textField) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	t.width = w
	t.clampOffset()
}

func (t *textField) Focus()        { t.focused = true }
func (t *textField) Blur()         { t.focused = false }
func (t textField) Focused() bool  { return t.focused }
func (t *textField) Clear()        { t.value, t.cursor, t.offset = nil, 0, 0 }
func (t textField) CursorPos() int { return t.cursor }

// Update applies a key press. It reports whether the value changed, which the
// caller uses to decide if the result list needs re-filtering.
func (t *textField) Update(msg tea.KeyMsg) (changed bool) {
	before := len(t.value)
	beforeStr := string(t.value)

	switch msg.Type {
	case tea.KeyRunes:
		t.insert(msg.Runes)
	case tea.KeySpace:
		t.insert([]rune{' '})
	case tea.KeyBackspace:
		if t.cursor > 0 {
			t.value = append(t.value[:t.cursor-1], t.value[t.cursor:]...)
			t.cursor--
		}
	case tea.KeyDelete:
		if t.cursor < len(t.value) {
			t.value = append(t.value[:t.cursor], t.value[t.cursor+1:]...)
		}
	case tea.KeyLeft, tea.KeyCtrlB:
		if t.cursor > 0 {
			t.cursor--
		}
	case tea.KeyRight, tea.KeyCtrlF:
		if t.cursor < len(t.value) {
			t.cursor++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		t.cursor = 0
	case tea.KeyEnd:
		t.cursor = len(t.value)
	case tea.KeyCtrlU: // kill to start of line, as in a shell
		t.value = append([]rune{}, t.value[t.cursor:]...)
		t.cursor = 0
	case tea.KeyCtrlW: // delete the word before the cursor
		t.deleteWordBackward()
	}

	// Ctrl+E, Ctrl+K, Ctrl+N and the other command keys are deliberately not
	// handled here: the list view claims them, and a key cannot mean two
	// things at once. End, Ctrl+A, Ctrl+U and Ctrl+W cover line editing.

	t.clampOffset()
	return len(t.value) != before || string(t.value) != beforeStr
}

func (t *textField) insert(rs []rune) {
	// Filter control characters; a stray escape sequence must never land in a
	// hostname or an alias.
	clean := make([]rune, 0, len(rs))
	for _, r := range rs {
		if r >= 0x20 && r != 0x7f {
			clean = append(clean, r)
		}
	}
	if len(clean) == 0 {
		return
	}
	out := make([]rune, 0, len(t.value)+len(clean))
	out = append(out, t.value[:t.cursor]...)
	out = append(out, clean...)
	out = append(out, t.value[t.cursor:]...)
	t.value = out
	t.cursor += len(clean)
}

func (t *textField) deleteWordBackward() {
	i := t.cursor
	for i > 0 && t.value[i-1] == ' ' {
		i--
	}
	for i > 0 && t.value[i-1] != ' ' {
		i--
	}
	t.value = append(t.value[:i], t.value[t.cursor:]...)
	t.cursor = i
}

// clampOffset scrolls the visible window so the cursor stays inside it.
func (t *textField) clampOffset() {
	if t.cursor > len(t.value) {
		t.cursor = len(t.value)
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.width <= 0 {
		t.offset = 0
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	// Leave one cell for the cursor itself when it sits at end of text.
	if t.cursor >= t.offset+t.width {
		t.offset = t.cursor - t.width + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// View renders the field, drawing a block cursor when focused.
func (t textField) View() string {
	if len(t.value) == 0 && !t.focused && t.placeholder != "" {
		return stylePlaceholder.Render(truncate(t.placeholder, t.width))
	}

	end := t.offset + t.width
	if end > len(t.value) {
		end = len(t.value)
	}
	start := t.offset
	if start > len(t.value) {
		start = len(t.value)
	}
	visible := t.value[start:end]

	if !t.focused {
		if len(visible) == 0 && t.placeholder != "" {
			return stylePlaceholder.Render(truncate(t.placeholder, t.width))
		}
		return string(visible)
	}

	rel := t.cursor - start
	var b strings.Builder
	b.WriteString(string(visible[:rel]))
	if rel < len(visible) {
		b.WriteString(styleCursor.Render(string(visible[rel])))
		b.WriteString(string(visible[rel+1:]))
	} else {
		b.WriteString(styleCursor.Render(" "))
	}
	if len(t.value) == 0 && t.placeholder != "" {
		// Show the hint after the cursor so an empty focused field still
		// explains itself.
		b.WriteString(stylePlaceholder.Render(truncate(t.placeholder, t.width-1)))
	}
	return b.String()
}
