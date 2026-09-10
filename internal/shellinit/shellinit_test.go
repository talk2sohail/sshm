package shellinit

import (
	"strings"
	"testing"
)

func TestSnippetForEveryShell(t *testing.T) {
	for _, sh := range Shells() {
		s, err := Snippet(sh, "^G")
		if err != nil {
			t.Fatalf("%s: %v", sh, err)
		}
		if !strings.Contains(s, "sshm") {
			t.Errorf("%s snippet does not mention sshm:\n%s", sh, s)
		}
		if strings.TrimSpace(s) == "" {
			t.Errorf("%s snippet is empty", sh)
		}
	}
}

func TestKeyChordParsing(t *testing.T) {
	for _, form := range []string{"^S", "ctrl+s", "Ctrl-S", "s", "S"} {
		if _, err := Snippet(Zsh, form); err != nil {
			t.Errorf("Snippet(zsh, %q) = %v, want it to parse", form, err)
		}
	}
	for _, bad := range []string{"", "shift+s", "abc", "^1", "^"} {
		if _, err := Snippet(Zsh, bad); err == nil {
			t.Errorf("Snippet(zsh, %q) should have been rejected", bad)
		}
	}
}

func TestBindingUsesTheRequestedKey(t *testing.T) {
	zsh, err := Snippet(Zsh, "^G")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(zsh, `bindkey '\C-g'`) {
		t.Errorf("zsh snippet does not bind \\C-g:\n%s", zsh)
	}

	bash, err := Snippet(Bash, "^T")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bash, `"\C-t"`) {
		t.Errorf("bash snippet does not bind \\C-t:\n%s", bash)
	}

	fish, err := Snippet(Fish, "^O")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fish, `\co`) {
		t.Errorf("fish snippet does not bind \\co:\n%s", fish)
	}
}

// Ctrl+S is XOFF. Binding it without disabling flow control looks exactly like
// a hung terminal, so the snippet must turn it off.
func TestFlowControlIsDisabledForCtrlS(t *testing.T) {
	for _, sh := range []string{Bash, Zsh, Fish} {
		s, err := Snippet(sh, "^S")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, "stty -ixon") {
			t.Errorf("%s snippet for ^S must disable flow control:\n%s", sh, s)
		}
	}
}

func TestFlowControlNotDisabledForOtherKeys(t *testing.T) {
	s, err := Snippet(Zsh, "^G")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "stty -ixon") {
		t.Errorf("^G does not need flow control disabled:\n%s", s)
	}
}

func TestZshWidgetRedrawsPrompt(t *testing.T) {
	s, _ := Snippet(Zsh, "^G")
	// Without zle -I the TUI's first frame is drawn over the prompt; without
	// reset-prompt the shell prompt is missing after ssh exits.
	for _, want := range []string{"zle -I", "zle reset-prompt", "zle -N"} {
		if !strings.Contains(s, want) {
			t.Errorf("zsh snippet missing %q:\n%s", want, s)
		}
	}
}

func TestTmuxUsesAWindowNotAPopup(t *testing.T) {
	s, err := Snippet(Tmux, "^S")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "display-popup") {
		t.Error("a popup dies with the session it launches; use a window")
	}
	if !strings.Contains(s, "new-window") {
		t.Errorf("tmux snippet should open a window:\n%s", s)
	}
}

func TestUnknownShell(t *testing.T) {
	if _, err := Snippet("csh", "^S"); err == nil {
		t.Error("expected an error for an unsupported shell")
	} else if !strings.Contains(err.Error(), "bash") {
		t.Errorf("error should list the supported shells, got: %v", err)
	}
}

func TestInstallHint(t *testing.T) {
	for _, sh := range Shells() {
		if InstallHint(sh) == "" {
			t.Errorf("no install hint for %s", sh)
		}
	}
}
