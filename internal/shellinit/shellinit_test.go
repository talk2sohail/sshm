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

func TestPowerShellBindsTheRequestedChord(t *testing.T) {
	s, err := Snippet(PowerShell, "^G")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "-Chord 'Ctrl+g'") {
		t.Errorf("powershell snippet does not bind Ctrl+g:\n%s", s)
	}
	if !strings.Contains(s, "Set-PSReadLineKeyHandler") {
		t.Errorf("powershell snippet must go through PSReadLine:\n%s", s)
	}
}

// The names people actually type. "powershell" is the documented one, but
// anyone on PowerShell 7 knows the binary as pwsh.
func TestPowerShellAliases(t *testing.T) {
	for _, name := range []string{"powershell", "PowerShell", "pwsh", "ps", "ps1"} {
		s, err := Snippet(name, "^S")
		if err != nil {
			t.Fatalf("Snippet(%q) = %v, want a snippet", name, err)
		}
		if !strings.Contains(s, "Set-PSReadLineKeyHandler") {
			t.Errorf("Snippet(%q) is not the powershell snippet:\n%s", name, s)
		}
		if InstallHint(name) == "" {
			t.Errorf("no install hint for %q", name)
		}
	}
}

// PSReadLine is drawing the prompt line when the handler fires. Without
// RevertLine the old prompt sits under the picker; without InvokePrompt there
// is no prompt at all after the picker or the ssh session exits.
func TestPowerShellHandsThePromptBackAndRedrawsIt(t *testing.T) {
	s, _ := Snippet(PowerShell, "^S")
	for _, want := range []string{"RevertLine()", "InvokePrompt()", "sshm"} {
		if !strings.Contains(s, want) {
			t.Errorf("powershell snippet missing %q:\n%s", want, s)
		}
	}
}

// Windows consoles do not implement XOFF flow control, so ^S needs no
// workaround there -- and stty does not exist to run one.
func TestPowerShellNeverEmitsStty(t *testing.T) {
	for _, key := range []string{"^S", "^Q", "^G"} {
		s, err := Snippet(PowerShell, key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, "stty") {
			t.Errorf("powershell snippet for %s must not call stty:\n%s", key, s)
		}
	}
}

// PowerShell 5.1 decodes a native command's stdout using the console's OEM
// code page, so anything outside ASCII arrives as mojibake on its way through
// Invoke-Expression.
func TestPowerShellSnippetIsASCII(t *testing.T) {
	s, _ := Snippet(PowerShell, "^S")
	for i, r := range s {
		if r > 127 {
			t.Errorf("non-ASCII rune %q at byte %d in powershell snippet", r, i)
		}
	}
}

// A snippet is sourced from a startup file, so a broken one breaks every new
// shell. Guard the shape of each: balanced braces and no stray CR.
func TestSnippetsAreWellFormed(t *testing.T) {
	for _, sh := range Shells() {
		s, err := Snippet(sh, "^S")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, "\r") {
			t.Errorf("%s snippet contains a carriage return", sh)
		}
		if strings.Count(s, "{") != strings.Count(s, "}") {
			t.Errorf("%s snippet has unbalanced braces:\n%s", sh, s)
		}
		if !strings.HasSuffix(s, "\n") {
			t.Errorf("%s snippet does not end in a newline", sh)
		}
	}
}
