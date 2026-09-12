// Package model defines the core Host type shared by every layer of sshm.
//
// It deliberately has no external dependencies so that the data model,
// validation rules and ssh argument construction can be unit tested in
// isolation from the TUI and the config file format.
package model

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// DefaultPort is used whenever a host does not specify one.
const DefaultPort = 22

// Host is a single saved SSH destination.
//
// Alias is the primary key: it is what the user types, what history is keyed
// on, and what must be unique across the whole store.
type Host struct {
	Alias        string   `toml:"alias" json:"alias"`
	Hostname     string   `toml:"hostname" json:"hostname"`
	User         string   `toml:"user,omitempty" json:"user,omitempty"`
	Port         int      `toml:"port,omitempty" json:"port,omitempty"`
	IdentityFile string   `toml:"identity_file,omitempty" json:"identity_file,omitempty"`
	ProxyJump    string   `toml:"proxy_jump,omitempty" json:"proxy_jump,omitempty"`
	Tags         []string `toml:"tags,omitempty" json:"tags,omitempty"`
	Description  string   `toml:"description,omitempty" json:"description,omitempty"`

	// Options are raw ssh -o directives, e.g. "ServerAliveInterval=30".
	Options []string `toml:"options,omitempty" json:"options,omitempty"`

	// Source records where the host came from ("" for native, "ssh_config"
	// for an imported entry). Purely informational.
	Source string `toml:"source,omitempty" json:"source,omitempty"`
}

// EffectivePort returns the port to dial, applying the default.
func (h Host) EffectivePort() int {
	if h.Port <= 0 {
		return DefaultPort
	}
	return h.Port
}

// Target renders the user@hostname form ssh expects.
func (h Host) Target() string {
	if h.User == "" {
		return h.Hostname
	}
	return h.User + "@" + h.Hostname
}

// Addr renders host:port for TCP reachability probes. It is IPv6-safe.
func (h Host) Addr() string {
	return net.JoinHostPort(h.Hostname, strconv.Itoa(h.EffectivePort()))
}

// Label is what the list renders as the primary line.
func (h Host) Label() string {
	if h.Alias != "" {
		return h.Alias
	}
	return h.Hostname
}

// HasTag reports whether the host carries tag, case-insensitively.
func (h Host) HasTag(tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return false
	}
	for _, t := range h.Tags {
		if strings.ToLower(t) == tag {
			return true
		}
	}
	return false
}

// Normalize cleans user input in place: trims whitespace everywhere and
// lowercases, de-duplicates and sorts tags so the store stays stable and
// diffs stay small.
func (h *Host) Normalize() {
	h.Alias = strings.TrimSpace(h.Alias)
	h.Hostname = strings.TrimSpace(h.Hostname)
	h.User = strings.TrimSpace(h.User)
	h.IdentityFile = strings.TrimSpace(h.IdentityFile)
	h.ProxyJump = strings.TrimSpace(h.ProxyJump)
	h.Description = strings.TrimSpace(h.Description)

	if h.Port == DefaultPort {
		h.Port = 0 // keep the file clean; the default is implicit
	}

	seen := make(map[string]struct{}, len(h.Tags))
	tags := h.Tags[:0]
	for _, t := range h.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		tags = append(tags, t)
	}
	sort.Strings(tags)
	if len(tags) == 0 {
		h.Tags = nil
	} else {
		h.Tags = tags
	}

	opts := h.Options[:0]
	for _, o := range h.Options {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, o)
		}
	}
	if len(opts) == 0 {
		h.Options = nil
	} else {
		h.Options = opts
	}
}

// aliasBadChars are rejected in aliases so that an alias is always safe to use
// as a shell word, a map key and a filename fragment.
const aliasBadChars = " \t\n\r\"'`$&;|<>()[]{}*?!\\"

// Validate returns a descriptive error if the host cannot be used.
func (h Host) Validate() error {
	if h.Alias == "" {
		return fmt.Errorf("alias is required")
	}
	if strings.ContainsAny(h.Alias, aliasBadChars) {
		return fmt.Errorf("alias %q contains whitespace or shell metacharacters", h.Alias)
	}
	if h.Hostname == "" {
		return fmt.Errorf("host %q: hostname is required", h.Alias)
	}
	if strings.ContainsAny(h.Hostname, " \t\n") {
		return fmt.Errorf("host %q: hostname %q contains whitespace", h.Alias, h.Hostname)
	}
	if h.Port < 0 || h.Port > 65535 {
		return fmt.Errorf("host %q: port %d out of range", h.Alias, h.Port)
	}
	for _, o := range h.Options {
		if !strings.Contains(o, "=") {
			return fmt.Errorf("host %q: option %q must be in Key=Value form", h.Alias, o)
		}
	}
	return nil
}

// ExpandPath resolves a leading ~ against the current user's home directory.
// It is used for identity files, which users almost always write as ~/....
func ExpandPath(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	if len(p) > 1 && p[1] != '/' {
		return p // ~otheruser/... — leave for ssh to resolve
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// KeyProblem describes something wrong with a host's identity file. It is
// surfaced in the detail pane, because a missing or world-readable key is the
// single most common reason a connection that "should work" does not.
type KeyProblem struct {
	Path    string
	Message string
	Fatal   bool // true when ssh will refuse outright
}

// CheckIdentity inspects the host's identity file, if it has one. It returns
// nil when there is no key configured or the key looks fine.
//
// This is deliberately not called during startup; it stats the filesystem and
// is only run for the currently selected host.
func (h Host) CheckIdentity() *KeyProblem {
	if h.IdentityFile == "" {
		return nil
	}
	path := ExpandPath(h.IdentityFile)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return &KeyProblem{Path: path, Message: "key file not found", Fatal: true}
	}
	if err != nil {
		return &KeyProblem{Path: path, Message: err.Error(), Fatal: true}
	}
	if info.IsDir() {
		return &KeyProblem{Path: path, Message: "key path is a directory", Fatal: true}
	}
	// Only Unix has a permission check worth making. Windows has no POSIX mode
	// bits: Mode().Perm() reports 0666 for any writable file and 0444 for a
	// read-only one, so this test would call every key on the machine fatally
	// misconfigured and tell the user to run a chmod that does not exist there.
	// Win32 OpenSSH enforces key privacy through ACLs instead, which is not
	// something a mode mask can see.
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			return &KeyProblem{
				Path: path,
				Message: fmt.Sprintf("key permissions are %#o, ssh requires 0600 (run: chmod 600 %s)",
					mode, path),
				Fatal: true,
			}
		}
	}
	return nil
}

// SSHArgs builds the full argv (excluding argv[0]) for connecting to this host.
// extra is appended verbatim, which is how a remote command or extra flags are
// passed through from the command line.
func (h Host) SSHArgs(extra []string) []string {
	args := make([]string, 0, 12+len(extra)+2*len(h.Options))

	if h.Port > 0 && h.Port != DefaultPort {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	if h.IdentityFile != "" {
		args = append(args, "-i", ExpandPath(h.IdentityFile))
		// With an explicit key, stop ssh from offering every agent key first;
		// on hosts with MaxAuthTries=3 that is a common cause of failure.
		args = append(args, "-o", "IdentitiesOnly=yes")
	}
	if h.ProxyJump != "" {
		args = append(args, "-J", h.ProxyJump)
	}
	for _, o := range h.Options {
		args = append(args, "-o", o)
	}
	// Force pty allocation. sshm's whole point is handing the terminal to
	// ssh for an interactive session, but ssh's own auto-detection (allocate
	// a pty only if stdin looks like a terminal) can come back negative
	// coming out of the picker's TUI even though the real terminal is fine,
	// which otherwise silently drops you back to your shell right after the
	// login banner. A single -t only *requests* a pty and still backs off
	// with the same warning if ssh doesn't think stdin is a tty; two -t's
	// (i.e. -tt) force it regardless.
	args = append(args, "-tt")
	args = append(args, h.Target())
	args = append(args, extra...)
	return args
}

// CommandLine renders a copy-pasteable ssh invocation for this host.
func (h Host) CommandLine(extra []string) string {
	parts := append([]string{"ssh"}, h.SSHArgs(extra)...)
	for i, p := range parts {
		parts[i] = quoteArg(p)
	}
	return strings.Join(parts, " ")
}

// quoteArg quotes one argument so the rendered line survives being pasted into
// the shell the user is actually running.
//
// The rules differ by platform, and the Unix ones produce a broken command on
// Windows. A backslash is an escape character in a POSIX shell, so strconv.Quote
// is right there: it doubles them. On Windows a backslash is just the path
// separator, and doubling it turns C:\Users\me\.ssh\id_ed25519 into a path ssh
// cannot open -- so backslashes are left alone and do not by themselves make an
// argument need quoting.
func quoteArg(s string) string {
	if runtime.GOOS == "windows" {
		if !strings.ContainsAny(s, " \t\"") {
			return s
		}
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	if !strings.ContainsAny(s, " \t\"'$`\\") {
		return s
	}
	return strconv.Quote(s)
}

// AllTags returns the sorted, de-duplicated set of tags across hosts.
func AllTags(hosts []Host) []string {
	seen := map[string]struct{}{}
	for _, h := range hosts {
		for _, t := range h.Tags {
			seen[strings.ToLower(t)] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
