package ui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/talk2sohail/sshm/internal/model"
)

// Form field order. Alias and hostname come first because they are the only
// two that are required.
const (
	fAlias = iota
	fHostname
	fUser
	fPort
	fIdentity
	fProxyJump
	fTags
	fDescription
	fieldCount
)

var fieldLabels = [fieldCount]string{
	fAlias:       "Alias",
	fHostname:    "Hostname",
	fUser:        "User",
	fPort:        "Port",
	fIdentity:    "Key file",
	fProxyJump:   "Jump host",
	fTags:        "Tags",
	fDescription: "Note",
}

var fieldHints = [fieldCount]string{
	fAlias:       "short name you will type, e.g. prod-api",
	fHostname:    "IP or DNS name",
	fUser:        "login user (optional)",
	fPort:        "22 if blank",
	fIdentity:    "~/.ssh/keys/prod.pem (optional)",
	fProxyJump:   "bastion host to tunnel through (optional)",
	fTags:        "comma separated, e.g. prod, aws, db",
	fDescription: "anything that helps you recognise this box",
}

// form is the add/edit host editor.
type form struct {
	fields [fieldCount]textField
	focus  int

	editIndex int    // index into the store, or -1 when adding
	origAlias string // for history rename on save
	err       error
}

func newForm(width int) form {
	f := form{editIndex: -1}
	for i := range f.fields {
		f.fields[i] = newTextField(fieldHints[i], width)
	}
	f.fields[0].Focus()
	return f
}

// editing populates the form from an existing host.
func (f *form) editing(h model.Host, index int) {
	f.editIndex = index
	f.origAlias = h.Alias
	f.fields[fAlias].SetValue(h.Alias)
	f.fields[fHostname].SetValue(h.Hostname)
	f.fields[fUser].SetValue(h.User)
	if h.Port > 0 {
		f.fields[fPort].SetValue(strconv.Itoa(h.Port))
	} else {
		f.fields[fPort].SetValue("")
	}
	f.fields[fIdentity].SetValue(h.IdentityFile)
	f.fields[fProxyJump].SetValue(h.ProxyJump)
	f.fields[fTags].SetValue(strings.Join(h.Tags, ", "))
	f.fields[fDescription].SetValue(h.Description)
	f.setFocus(0)
	f.err = nil
}

// adding resets the form for a new host, optionally seeding the alias from
// whatever the user had typed in the search bar.
func (f *form) adding(seed string) {
	f.editIndex = -1
	f.origAlias = ""
	for i := range f.fields {
		f.fields[i].SetValue("")
	}
	if seed != "" {
		f.fields[fAlias].SetValue(seed)
	}
	f.setFocus(0)
	f.err = nil
}

func (f *form) setFocus(i int) {
	if i < 0 {
		i = fieldCount - 1
	}
	if i >= fieldCount {
		i = 0
	}
	for j := range f.fields {
		f.fields[j].Blur()
	}
	f.focus = i
	f.fields[i].Focus()
}

func (f *form) setWidth(w int) {
	for i := range f.fields {
		f.fields[i].SetWidth(w)
	}
}

// formAction is what the parent model should do after a key press.
type formAction int

const (
	formNone formAction = iota
	formCancel
	formSubmit
)

// Update handles a key press. Tab and the arrow keys move between fields;
// Enter on the last field, or Ctrl+S anywhere, submits.
func (f *form) Update(msg tea.KeyMsg) formAction {
	switch msg.Type {
	case tea.KeyEsc:
		return formCancel
	case tea.KeyCtrlC:
		return formCancel
	case tea.KeyCtrlS:
		return formSubmit
	case tea.KeyTab, tea.KeyDown:
		f.setFocus(f.focus + 1)
		return formNone
	case tea.KeyShiftTab, tea.KeyUp:
		f.setFocus(f.focus - 1)
		return formNone
	case tea.KeyEnter:
		if f.focus == fieldCount-1 {
			return formSubmit
		}
		f.setFocus(f.focus + 1)
		return formNone
	}

	if f.fields[f.focus].Update(msg) {
		f.err = nil // typing clears a stale validation error
	}
	return formNone
}

// host builds a Host from the current field values.
func (f form) host() (model.Host, error) {
	h := model.Host{
		Alias:        strings.TrimSpace(f.fields[fAlias].Value()),
		Hostname:     strings.TrimSpace(f.fields[fHostname].Value()),
		User:         strings.TrimSpace(f.fields[fUser].Value()),
		IdentityFile: strings.TrimSpace(f.fields[fIdentity].Value()),
		ProxyJump:    strings.TrimSpace(f.fields[fProxyJump].Value()),
		Description:  strings.TrimSpace(f.fields[fDescription].Value()),
	}

	if p := strings.TrimSpace(f.fields[fPort].Value()); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return h, errPort
		}
		if n < 1 || n > 65535 {
			return h, errPortRange
		}
		h.Port = n
	}

	for _, t := range strings.Split(f.fields[fTags].Value(), ",") {
		if t = strings.TrimSpace(t); t != "" {
			h.Tags = append(h.Tags, t)
		}
	}

	h.Normalize()
	return h, h.Validate()
}

type formErr string

func (e formErr) Error() string { return string(e) }

const (
	errPort      = formErr("port must be a number")
	errPortRange = formErr("port must be between 1 and 65535")
)

// View renders the form.
func (f form) View(width int) string {
	var b strings.Builder

	title := "Add host"
	if f.editIndex >= 0 {
		title = "Edit " + f.origAlias
	}
	b.WriteString(styleTitle.Render(title))
	b.WriteString("\n\n")

	labelWidth := 10
	for i := range f.fields {
		label := fieldLabels[i]
		if i == fAlias || i == fHostname {
			label += " *"
		}
		style := styleFieldName
		marker := "  "
		if i == f.focus {
			style = styleFocused
			marker = styleFocused.Render("› ")
		}
		b.WriteString(marker)
		b.WriteString(style.Render(pad(label, labelWidth)))
		b.WriteString("  ")
		b.WriteString(f.fields[i].View())
		b.WriteString("\n")
	}

	// Live preview of the command this host will run: the fastest way to see
	// that the form is filled in correctly.
	if h, err := f.host(); err == nil {
		b.WriteString("\n")
		b.WriteString(styleCommand.Render(truncate(h.CommandLine(nil), width-2)))
		b.WriteString("\n")
	} else {
		b.WriteString("\n\n")
	}

	if f.err != nil {
		b.WriteString("\n")
		b.WriteString(styleError.Render(f.err.Error()))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpLine([][2]string{
		{"tab", "next field"},
		{"enter", "next / save"},
		{"ctrl+s", "save"},
		{"esc", "cancel"},
	}))
	return b.String()
}
