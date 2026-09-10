package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sohail/sshm/internal/model"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "hosts.toml"))
}

func TestAddAndLookup(t *testing.T) {
	s := tmpStore(t)
	if err := s.Add(model.Host{Alias: "web", Hostname: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	if !s.Dirty() {
		t.Error("store should be dirty after Add")
	}

	h, i, ok := s.ByAlias("WEB")
	if !ok {
		t.Fatal("lookup should be case-insensitive")
	}
	if i != 0 || h.Hostname != "10.0.0.1" {
		t.Errorf("got index %d, host %+v", i, h)
	}
}

func TestAddRejectsDuplicateAlias(t *testing.T) {
	s := tmpStore(t)
	if err := s.Add(model.Host{Alias: "web", Hostname: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	err := s.Add(model.Host{Alias: "WEB", Hostname: "10.0.0.2"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want a duplicate-alias error", err)
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}
}

func TestAddRejectsInvalidHost(t *testing.T) {
	s := tmpStore(t)
	if err := s.Add(model.Host{Alias: "no-host"}); err == nil {
		t.Fatal("expected a validation error")
	}
	if s.Len() != 0 {
		t.Error("invalid host was stored")
	}
}

func TestAddNormalizes(t *testing.T) {
	s := tmpStore(t)
	if err := s.Add(model.Host{
		Alias:    "  web  ",
		Hostname: " 10.0.0.1 ",
		Tags:     []string{"Prod", "prod", "AWS"},
	}); err != nil {
		t.Fatal(err)
	}
	h, _, _ := s.ByAlias("web")
	if h.Alias != "web" || h.Hostname != "10.0.0.1" {
		t.Errorf("not trimmed: %+v", h)
	}
	if len(h.Tags) != 2 || h.Tags[0] != "aws" || h.Tags[1] != "prod" {
		t.Errorf("tags = %v, want [aws prod]", h.Tags)
	}
}

func TestUpdate(t *testing.T) {
	s := tmpStore(t)
	_ = s.Add(model.Host{Alias: "web", Hostname: "10.0.0.1"})
	_ = s.Add(model.Host{Alias: "db", Hostname: "10.0.0.2"})

	if err := s.Update(0, model.Host{Alias: "web", Hostname: "10.9.9.9"}); err != nil {
		t.Fatal(err)
	}
	h, _, _ := s.ByAlias("web")
	if h.Hostname != "10.9.9.9" {
		t.Errorf("hostname = %q", h.Hostname)
	}

	// Renaming onto an existing alias must fail.
	if err := s.Update(0, model.Host{Alias: "db", Hostname: "10.0.0.1"}); err == nil {
		t.Error("expected a duplicate-alias error")
	}
	// Renaming to a fresh alias is fine.
	if err := s.Update(0, model.Host{Alias: "web2", Hostname: "10.0.0.1"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Keeping the same alias is not a self-collision.
	if err := s.Update(0, model.Host{Alias: "web2", Hostname: "10.0.0.3"}); err != nil {
		t.Errorf("updating a host in place failed: %v", err)
	}
}

func TestUpdateOutOfRange(t *testing.T) {
	s := tmpStore(t)
	if err := s.Update(5, model.Host{Alias: "a", Hostname: "h"}); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	s := tmpStore(t)
	_ = s.Add(model.Host{Alias: "a", Hostname: "1"})
	_ = s.Add(model.Host{Alias: "b", Hostname: "2"})
	_ = s.Add(model.Host{Alias: "c", Hostname: "3"})

	h, err := s.Delete(1)
	if err != nil {
		t.Fatal(err)
	}
	if h.Alias != "b" {
		t.Errorf("deleted %q, want b", h.Alias)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	// The remaining hosts must keep their order and stay addressable.
	if got := s.Hosts()[0].Alias; got != "a" {
		t.Errorf("hosts[0] = %q, want a", got)
	}
	if got := s.Hosts()[1].Alias; got != "c" {
		t.Errorf("hosts[1] = %q, want c", got)
	}
	if _, err := s.Delete(9); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestAddMany(t *testing.T) {
	s := tmpStore(t)
	_ = s.Add(model.Host{Alias: "existing", Hostname: "1"})

	added, skipped := s.AddMany([]model.Host{
		{Alias: "new1", Hostname: "2"},
		{Alias: "existing", Hostname: "3"}, // duplicate
		{Alias: "bad"},                     // invalid: no hostname
		{Alias: "new2", Hostname: "4"},
	})
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want 2", skipped)
	}
	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3", s.Len())
	}
}

func TestAliases(t *testing.T) {
	s := tmpStore(t)
	_ = s.Add(model.Host{Alias: "a", Hostname: "1"})
	_ = s.Add(model.Host{Alias: "b", Hostname: "2"})
	got := s.Aliases()
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["a"]; !ok {
		t.Error("missing a")
	}
}

func TestSortByAlias(t *testing.T) {
	s := tmpStore(t)
	_ = s.Add(model.Host{Alias: "zeta", Hostname: "1"})
	_ = s.Add(model.Host{Alias: "Alpha", Hostname: "2"})
	_ = s.Add(model.Host{Alias: "mid", Hostname: "3"})
	s.SortByAlias()

	want := []string{"Alpha", "mid", "zeta"}
	for i, h := range s.Hosts() {
		if h.Alias != want[i] {
			t.Errorf("hosts[%d] = %q, want %q", i, h.Alias, want[i])
		}
	}
}

func TestSaveIsNoOpWhenClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.toml")
	s := New(path)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a clean store should not create a file")
	}
}

func TestSaveWritesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.toml")
	s := New(path)
	_ = s.Add(model.Host{Alias: "web", Hostname: "10.0.0.1"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.Dirty() {
		t.Error("store still dirty after Save")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The file names private key paths, so it must not be world-readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %#o, want 0600", perm)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.toml")

	s := New(path)
	want := model.Host{
		Alias:        "prod-api",
		Hostname:     "10.0.4.21",
		User:         "ubuntu",
		Port:         2222,
		IdentityFile: "~/.ssh/prod.pem",
		ProxyJump:    "bastion",
		Tags:         []string{"aws", "prod"},
		Description:  "main api box",
		Options:      []string{"ServerAliveInterval=30"},
	}
	if err := s.Add(want); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(model.Host{Alias: "plain", Hostname: "example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Len() != 2 {
		t.Fatalf("loaded %d hosts, want 2", loaded.Len())
	}
	if loaded.Dirty() {
		t.Error("a freshly loaded store should not be dirty")
	}

	got, _, ok := loaded.ByAlias("prod-api")
	if !ok {
		t.Fatal("prod-api missing after reload")
	}
	for _, c := range []struct{ name, got, want string }{
		{"hostname", got.Hostname, want.Hostname},
		{"user", got.User, want.User},
		{"identity", got.IdentityFile, want.IdentityFile},
		{"proxyjump", got.ProxyJump, want.ProxyJump},
		{"description", got.Description, want.Description},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got.Port != 2222 {
		t.Errorf("port = %d, want 2222", got.Port)
	}
	if strings.Join(got.Tags, ",") != "aws,prod" {
		t.Errorf("tags = %v", got.Tags)
	}
	if len(got.Options) != 1 || got.Options[0] != "ServerAliveInterval=30" {
		t.Errorf("options = %v", got.Options)
	}

	// A host that used the default port must come back with the default.
	plain, _, _ := loaded.ByAlias("plain")
	if plain.EffectivePort() != model.DefaultPort {
		t.Errorf("plain port = %d, want %d", plain.EffectivePort(), model.DefaultPort)
	}
}

func TestLoadMissingFileGivesEmptyStore(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("a missing config must not be an error: %v", err)
	}
	if s == nil || s.Len() != 0 {
		t.Fatal("expected a usable empty store")
	}
	// And it must be immediately usable for a first-run add.
	if err := s.Add(model.Host{Alias: "first", Hostname: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.toml")

	s := New(path)
	_ = s.Add(model.Host{Alias: "a", Hostname: "1"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	_ = s.Add(model.Host{Alias: "b", Hostname: "2"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// No temporary files may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "hosts.toml" {
			t.Errorf("stray file left in the config directory: %s", e.Name())
		}
	}
}

func TestPathHelpersRespectEnv(t *testing.T) {
	t.Setenv(EnvConfig, "/custom/hosts.toml")
	t.Setenv(EnvHistory, "/custom/history.json")
	if got := ConfigPath(); got != "/custom/hosts.toml" {
		t.Errorf("ConfigPath = %q", got)
	}
	if got := HistoryPath(); got != "/custom/history.json" {
		t.Errorf("HistoryPath = %q", got)
	}
}

func TestPathHelpersRespectXDG(t *testing.T) {
	t.Setenv(EnvConfig, "")
	t.Setenv(EnvHistory, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	if got, want := ConfigPath(), filepath.Join("/xdg/config", "sshm", "hosts.toml"); got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
	if got, want := HistoryPath(), filepath.Join("/xdg/state", "sshm", "history.json"); got != want {
		t.Errorf("HistoryPath = %q, want %q", got, want)
	}
}
