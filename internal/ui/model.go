// Package ui is the Bubble Tea front end.
//
// The whole program is one model with a small mode enum rather than a stack of
// nested components. At this size that is simpler to follow and makes the key
// handling explicit: every key press has exactly one place it can be handled.
package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/talk2sohail/sshm/internal/frecency"
	"github.com/talk2sohail/sshm/internal/model"
	"github.com/talk2sohail/sshm/internal/probe"
	"github.com/talk2sohail/sshm/internal/search"
	"github.com/talk2sohail/sshm/internal/store"
)

type mode int

const (
	modeList mode = iota
	modeForm
	modeConfirmDelete
	modeHelp
)

// statusTTL is how long a transient message stays on screen.
const statusTTL = 4 * time.Second

// Model is the root Bubble Tea model.
type Model struct {
	store  *store.Store
	hist   *frecency.Store
	index  *search.Index
	prober *probe.Prober

	query  textField
	hits   []search.Hit
	cursor int // index into hits
	top    int // first visible row, for scrolling

	width, height int

	status  map[string]probe.Result
	probeCh <-chan probe.Result
	cancel  context.CancelFunc
	probing bool

	mode    mode
	form    form
	message string
	msgErr  bool
	msgAt   time.Time

	// Launch is set when the user picks a host. main reads it after the
	// program exits and execs ssh, so the TUI is fully torn down first.
	Launch *model.Host

	autoProbe bool
	now       func() time.Time
}

// Options configures a new Model.
type Options struct {
	Store     *store.Store
	History   *frecency.Store
	Prober    *probe.Prober
	Query     string // pre-seeded search text
	AutoProbe bool   // probe visible hosts on start
}

// New builds the root model.
func New(opts Options) *Model {
	m := &Model{
		store:     opts.Store,
		hist:      opts.History,
		prober:    opts.Prober,
		index:     search.NewIndex(opts.Store.Hosts()),
		query:     newTextField("search servers, #tag to filter", 40),
		status:    map[string]probe.Result{},
		width:     80,
		height:    24,
		autoProbe: opts.AutoProbe,
		now:       time.Now,
		form:      newForm(40),
	}
	m.query.Focus()
	if opts.Query != "" {
		m.query.SetValue(opts.Query)
	}
	m.refilter()
	return m
}

// Init starts the first render and, when enabled, the first probe sweep.
func (m *Model) Init() tea.Cmd {
	if !m.autoProbe {
		return nil
	}
	return m.startProbe(false)
}

// ---- messages -------------------------------------------------------------

type probeResultMsg probe.Result
type probeDoneMsg struct{}
type clearStatusMsg struct{ at time.Time }

// waitForProbe reads one result off the probe channel. Re-issuing it after each
// result is the standard Bubble Tea way to stream from a channel without
// blocking the update loop.
func waitForProbe(ch <-chan probe.Result) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return probeDoneMsg{}
		}
		return probeResultMsg(r)
	}
}

// startProbe checks the hosts currently on screen. Probing only what is visible
// is what keeps this usable with hundreds of hosts: the user gets answers for
// the rows they are looking at, immediately.
func (m *Model) startProbe(force bool) tea.Cmd {
	if m.prober == nil || len(m.hits) == 0 {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}

	lo, hi := m.visibleRange()
	targets := make([]probe.Target, 0, hi-lo)
	for _, h := range m.hits[lo:hi] {
		targets = append(targets, probe.Target{Alias: h.Host.Alias, Addr: h.Host.Addr()})
		if force {
			m.prober.Invalidate(h.Host.Alias)
		}
		m.status[h.Host.Alias] = probe.Result{Alias: h.Host.Alias, Status: probe.Checking}
	}
	if len(targets) == 0 {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.probeCh = m.prober.Run(ctx, targets, force)
	m.probing = true
	return waitForProbe(m.probeCh)
}

// ---- update ---------------------------------------------------------------

// Update routes a message. Non-key messages are handled first so that probe
// results keep flowing regardless of which mode the UI is in.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.query.SetWidth(max(10, m.width-12))
		m.form.setWidth(max(10, m.width-20))
		m.clampCursor()
		return m, nil

	case probeResultMsg:
		m.status[msg.Alias] = probe.Result(msg)
		return m, waitForProbe(m.probeCh)

	case probeDoneMsg:
		m.probing = false
		return m, nil

	case clearStatusMsg:
		if msg.at.Equal(m.msgAt) {
			m.message, m.msgErr = "", false
		}
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeForm:
			return m.updateForm(msg)
		case modeConfirmDelete:
			return m.updateConfirm(msg)
		case modeHelp:
			m.mode = modeList
			return m, nil
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		// Esc clears a non-empty query first, and only quits on an empty one,
		// so it never throws away typing by surprise.
		if !m.query.Empty() {
			m.query.Clear()
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyEnter:
		return m.connect()
	// Movement follows fzf: arrows plus Ctrl+J/Ctrl+K, which is what anyone
	// who uses a fuzzy finder already has in their fingers.
	case tea.KeyUp, tea.KeyCtrlK:
		m.move(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlJ:
		m.move(1)
		return m, nil
	case tea.KeyPgUp:
		m.move(-m.rowsAvailable())
		return m, nil
	case tea.KeyPgDown:
		m.move(m.rowsAvailable())
		return m, nil

	// Commands use Ctrl rather than plain letters, because the search bar is
	// always live: a tool whose list swallows "a" would be useless. They avoid
	// Alt on purpose, since macOS terminals send Option as a compose key by
	// default and Alt bindings silently do nothing there.
	case tea.KeyCtrlN: // new
		m.form.adding(m.query.Value())
		m.mode = modeForm
		return m, nil
	case tea.KeyCtrlE: // edit
		if h, ok := m.selected(); ok {
			m.form.editing(h.Host, h.Index)
			m.mode = modeForm
		}
		return m, nil
	case tea.KeyCtrlX: // delete
		if _, ok := m.selected(); ok {
			m.mode = modeConfirmDelete
		}
		return m, nil
	case tea.KeyCtrlR: // re-check reachability
		return m, m.startProbe(true)
	case tea.KeyCtrlY: // yank the ssh command
		return m.copyCommand()
	case tea.KeyCtrlG: // help
		m.mode = modeHelp
		return m, nil
	}

	if m.query.Update(msg) {
		m.refilter()
		// Re-probe what scrolled into view, using the cache so this is nearly
		// free for hosts already checked.
		if m.autoProbe {
			return m, m.startProbe(false)
		}
	}
	return m, nil
}

func (m *Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.form.Update(msg) {
	case formCancel:
		m.mode = modeList
		return m, nil

	case formSubmit:
		h, err := m.form.host()
		if err != nil {
			m.form.err = err
			return m, nil
		}
		if m.form.editIndex >= 0 {
			if err := m.store.Update(m.form.editIndex, h); err != nil {
				m.form.err = err
				return m, nil
			}
			if m.form.origAlias != h.Alias {
				m.hist.Rename(m.form.origAlias, h.Alias)
			}
		} else if err := m.store.Add(h); err != nil {
			m.form.err = err
			return m, nil
		}
		if err := m.store.Save(); err != nil {
			return m, m.notifyErr("could not save: " + err.Error())
		}
		m.reindex()
		m.mode = modeList
		return m, m.notify("saved " + h.Alias)
	}
	return m, nil
}

func (m *Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEnter,
		len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y'):
		hit, ok := m.selected()
		if !ok {
			m.mode = modeList
			return m, nil
		}
		h, err := m.store.Delete(hit.Index)
		if err != nil {
			m.mode = modeList
			return m, m.notifyErr(err.Error())
		}
		m.hist.Forget(h.Alias)
		if err := m.store.Save(); err != nil {
			m.mode = modeList
			return m, m.notifyErr("could not save: " + err.Error())
		}
		m.reindex()
		m.mode = modeList
		return m, m.notify("deleted " + h.Alias)
	default:
		m.mode = modeList
		return m, nil
	}
}

// connect records the choice and quits. The actual exec happens in main, once
// Bubble Tea has restored the terminal.
func (m *Model) connect() (tea.Model, tea.Cmd) {
	hit, ok := m.selected()
	if !ok {
		return m, m.notifyErr("no host selected")
	}
	h := hit.Host
	m.hist.Record(h.Alias, m.now())
	if err := m.hist.Save(); err != nil {
		// History is a convenience; losing it must not block the connection.
		_ = err
	}
	m.Launch = &h
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
}

func (m *Model) copyCommand() (tea.Model, tea.Cmd) {
	hit, ok := m.selected()
	if !ok {
		return m, nil
	}
	// Two paths, because neither covers every terminal on its own. OSC 52 works
	// anywhere the terminal forwards it and costs nothing where it does not;
	// nativeCopy is the platform fallback for hosts whose console ignores the
	// sequence entirely, which is the normal case in conhost on Windows.
	cmdLine := hit.Host.CommandLine(nil)
	return m, tea.Batch(
		setClipboardOSC52(cmdLine),
		nativeCopyCmd(cmdLine),
		m.notify("copied ssh command for "+hit.Host.Alias),
	)
}

// setClipboardOSC52 writes the OSC 52 escape sequence that asks the terminal
// to put s on the system clipboard. bubbletea v1 (the version this module is
// pinned to) doesn't ship a SetClipboard command like v2 does, so we emit the
// sequence ourselves; terminals without OSC 52 support just ignore it.
// nativeCopyCmd runs the platform clipboard helper off the update loop. A
// failure is deliberately swallowed: OSC 52 may well have succeeded, and a
// clipboard that did not take is not worth an error over the host list.
func nativeCopyCmd(s string) tea.Cmd {
	return func() tea.Msg {
		_ = nativeCopy(s)
		return nil
	}
}

func setClipboardOSC52(s string) tea.Cmd {
	return func() tea.Msg {
		enc := base64.StdEncoding.EncodeToString([]byte(s))
		fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", enc)
		return nil
	}
}

// ---- state helpers --------------------------------------------------------

func (m *Model) refilter() {
	q := search.ParseQuery(m.query.Value())
	m.hits = m.index.Filter(q, m.hist, m.now())
	m.clampCursor()
}

// reindex rebuilds the search index after the host list changed.
func (m *Model) reindex() {
	m.index = search.NewIndex(m.store.Hosts())
	m.refilter()
}

func (m *Model) selected() (search.Hit, bool) {
	if m.cursor < 0 || m.cursor >= len(m.hits) {
		return search.Hit{}, false
	}
	return m.hits[m.cursor], true
}

func (m *Model) move(delta int) {
	m.cursor += delta
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if len(m.hits) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.hits) {
		m.cursor = len(m.hits) - 1
	}

	rows := m.rowsAvailable()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+rows {
		m.top = m.cursor - rows + 1
	}
	if m.top < 0 {
		m.top = 0
	}
	if maxTop := len(m.hits) - rows; m.top > maxTop {
		m.top = max(0, maxTop)
	}
}

// visibleRange returns the half-open range of hits currently on screen.
func (m *Model) visibleRange() (int, int) {
	lo := m.top
	hi := lo + m.rowsAvailable()
	if hi > len(m.hits) {
		hi = len(m.hits)
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

// chromeHeight is everything that is not a host row: title, search bar,
// blank lines, the detail pane and the help line.
const chromeHeight = 11

func (m *Model) rowsAvailable() int {
	n := m.height - chromeHeight
	if n < 1 {
		return 1
	}
	if n > 40 {
		return 40
	}
	return n
}

func (m *Model) notify(s string) tea.Cmd {
	m.message, m.msgErr, m.msgAt = s, false, m.now()
	return m.expireStatus(m.msgAt)
}

func (m *Model) notifyErr(s string) tea.Cmd {
	m.message, m.msgErr, m.msgAt = s, true, m.now()
	return m.expireStatus(m.msgAt)
}

func (m *Model) expireStatus(at time.Time) tea.Cmd {
	return tea.Tick(statusTTL, func(time.Time) tea.Msg {
		return clearStatusMsg{at: at}
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
