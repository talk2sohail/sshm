// Package shellinit emits the shell snippets that bind sshm to a key.
//
// This is what makes the tool feel instant. Without it, using sshm means typing
// its name at a prompt; with it, one chord opens the picker in the terminal you
// are already sitting in, and the session replaces the picker in place.
//
// The snippets deliberately run sshm as an ordinary child process rather than
// in a subshell or a popup, so that when sshm execs ssh, the session inherits
// the real terminal and the shell is waiting underneath when it ends.
package shellinit

import (
	"fmt"
	"sort"
	"strings"
)

// Supported shells.
const (
	Bash = "bash"
	Zsh  = "zsh"
	Fish = "fish"
	Tmux = "tmux"
)

// Shells returns the names Snippet accepts, sorted.
func Shells() []string {
	s := []string{Bash, Zsh, Fish, Tmux}
	sort.Strings(s)
	return s
}

// Snippet returns the init snippet for a shell, binding the picker to key.
//
// key is given in the terminal's control-character notation: "^S" for Ctrl+S,
// "^G" for Ctrl+G, and so on.
func Snippet(shell, key string) (string, error) {
	ctrl, ok := parseCtrl(key)
	if !ok {
		return "", fmt.Errorf("key %q must be a control chord like ^S or ^G", key)
	}

	switch strings.ToLower(shell) {
	case Zsh:
		return zshSnippet(ctrl), nil
	case Bash:
		return bashSnippet(ctrl), nil
	case Fish:
		return fishSnippet(ctrl), nil
	case Tmux:
		return tmuxSnippet(ctrl), nil
	default:
		return "", fmt.Errorf("unknown shell %q (supported: %s)",
			shell, strings.Join(Shells(), ", "))
	}
}

// ctrlKey is a parsed control chord.
type ctrlKey struct {
	Letter rune   // uppercase letter, e.g. 'S'
	Caret  string // "^S"
	Escape string // "\C-s" for zsh/bash bind
	Fish   string // "\cs" for fish bind
}

func parseCtrl(key string) (ctrlKey, bool) {
	k := strings.TrimSpace(key)
	k = strings.TrimPrefix(strings.ToLower(k), "ctrl+")
	k = strings.TrimPrefix(k, "ctrl-")
	k = strings.TrimPrefix(k, "^")
	if len(k) != 1 {
		return ctrlKey{}, false
	}
	r := rune(strings.ToUpper(k)[0])
	if r < 'A' || r > 'Z' {
		return ctrlKey{}, false
	}
	lower := strings.ToLower(string(r))
	return ctrlKey{
		Letter: r,
		Caret:  "^" + string(r),
		Escape: `\C-` + lower,
		Fish:   `\c` + lower,
	}, true
}

// flowControlNote is emitted for Ctrl+S and Ctrl+Q, which terminals reserve for
// software flow control (XOFF/XON) unless it is turned off. Binding either one
// without this looks exactly like a frozen terminal, so the snippet disables it.
func flowControlNote(c ctrlKey) string {
	if c.Letter != 'S' && c.Letter != 'Q' {
		return ""
	}
	return `# ` + c.Caret + ` is XOFF by default, which would freeze the terminal
# instead of running this binding. Turn off terminal flow control:
stty -ixon 2>/dev/null

`
}

func zshSnippet(c ctrlKey) string {
	return flowControlNote(c) + `# sshm — press ` + c.Caret + ` to open the server picker in this terminal.
sshm-widget() {
  # zle -I redraws the prompt area so the full-screen TUI starts from a clean
  # slate; without it zsh's prompt is left interleaved with the first frame.
  zle -I
  sshm
  local ret=$?
  zle reset-prompt
  return $ret
}
zle -N sshm-widget
bindkey '` + c.Escape + `' sshm-widget
`
}

func bashSnippet(c ctrlKey) string {
	return flowControlNote(c) + `# sshm — press ` + c.Caret + ` to open the server picker in this terminal.
# bind -x runs the command in the current shell and redraws the prompt after.
bind -x '"` + c.Escape + `": sshm'
`
}

func fishSnippet(c ctrlKey) string {
	note := ""
	if c.Letter == 'S' || c.Letter == 'Q' {
		note = `# ` + c.Caret + ` is XOFF by default and would freeze the terminal.
stty -ixon 2>/dev/null

`
	}
	return note + `# sshm — press ` + c.Caret + ` to open the server picker in this terminal.
function sshm-widget
    sshm
    commandline -f repaint
end
bind ` + c.Fish + ` sshm-widget
# Also bind it in vi insert mode, if that is in use.
bind -M insert ` + c.Fish + ` sshm-widget 2>/dev/null
`
}

func tmuxSnippet(c ctrlKey) string {
	return `# sshm — add to ~/.tmux.conf.
#
# A popup is convenient for browsing, but a session started inside one dies
# with the popup, so the picker opens in a new window instead and the ssh
# session lives there for as long as you need it.
bind-key ` + strings.ToLower(string(c.Letter)) + ` new-window -n ssh 'sshm'
`
}

// InstallHint returns the one-liner telling the user where to put the snippet.
func InstallHint(shell string) string {
	switch strings.ToLower(shell) {
	case Zsh:
		return `Add to ~/.zshrc:    eval "$(sshm shell-init zsh)"`
	case Bash:
		return `Add to ~/.bashrc:   eval "$(sshm shell-init bash)"`
	case Fish:
		return `Add to ~/.config/fish/config.fish:   sshm shell-init fish | source`
	case Tmux:
		return `Add the line above to ~/.tmux.conf, then: tmux source-file ~/.tmux.conf`
	default:
		return ""
	}
}
