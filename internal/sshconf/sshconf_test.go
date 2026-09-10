package sshconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sohail/sshm/internal/model"
)

func byAlias(hosts []model.Host, alias string) (model.Host, bool) {
	for _, h := range hosts {
		if h.Alias == alias {
			return h, true
		}
	}
	return model.Host{}, false
}

const sample = `
# personal machines
Host *
    ServerAliveInterval 60
    AddKeysToAgent yes

Host prod-api
    HostName 10.0.4.21
    User ubuntu
    Port 2222
    IdentityFile ~/.ssh/keys/prod.pem
    IdentityFile ~/.ssh/keys/ignored.pem
    ServerAliveInterval 30

Host db-1 db-2
    HostName db.internal
    User postgres

Host bastion
    Hostname=bastion.example.com
    User = jump

Host behind
    HostName 10.1.1.5
    ProxyJump bastion

Host quoted
    HostName real.example.com
    IdentityFile "~/.ssh/my key.pem"
    LocalForward 8080 localhost:80

Host broken
    HostName "not a hostname"

Host !nope
    HostName never.example.com

Match host somewhere
    User matched

Host plain
`

func TestParseSample(t *testing.T) {
	hosts, warnings, err := Parse(strings.NewReader(sample), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("skips wildcard and negated blocks", func(t *testing.T) {
		for _, h := range hosts {
			if strings.ContainsAny(h.Alias, "*!?") {
				t.Errorf("imported pattern block %q", h.Alias)
			}
		}
	})

	t.Run("full host", func(t *testing.T) {
		h, ok := byAlias(hosts, "prod-api")
		if !ok {
			t.Fatal("prod-api missing")
		}
		if h.Hostname != "10.0.4.21" || h.User != "ubuntu" || h.Port != 2222 {
			t.Errorf("got %+v", h)
		}
		if h.IdentityFile != "~/.ssh/keys/prod.pem" {
			t.Errorf("IdentityFile = %q, want the first one", h.IdentityFile)
		}
		if len(h.Options) != 1 || h.Options[0] != "ServerAliveInterval=30" {
			t.Errorf("Options = %v", h.Options)
		}
		if h.Source != "ssh_config" {
			t.Errorf("Source = %q", h.Source)
		}
	})

	t.Run("multiple patterns on one Host line", func(t *testing.T) {
		for _, alias := range []string{"db-1", "db-2"} {
			h, ok := byAlias(hosts, alias)
			if !ok {
				t.Fatalf("%s missing", alias)
			}
			if h.Hostname != "db.internal" || h.User != "postgres" {
				t.Errorf("%s = %+v", alias, h)
			}
		}
	})

	t.Run("equals separator and spacing", func(t *testing.T) {
		h, ok := byAlias(hosts, "bastion")
		if !ok {
			t.Fatal("bastion missing")
		}
		if h.Hostname != "bastion.example.com" || h.User != "jump" {
			t.Errorf("got %+v", h)
		}
	})

	t.Run("proxy jump", func(t *testing.T) {
		h, _ := byAlias(hosts, "behind")
		if h.ProxyJump != "bastion" {
			t.Errorf("ProxyJump = %q", h.ProxyJump)
		}
	})

	t.Run("unusable host is rejected, not imported", func(t *testing.T) {
		if _, ok := byAlias(hosts, "broken"); ok {
			t.Error("host with a whitespace hostname should not be imported")
		}
		var mentioned bool
		for _, w := range warnings {
			if strings.Contains(w, "broken") {
				mentioned = true
			}
		}
		if !mentioned {
			t.Errorf("warnings should explain the skip, got %v", warnings)
		}
	})

	t.Run("quoted value and dropped directive", func(t *testing.T) {
		h, ok := byAlias(hosts, "quoted")
		if !ok {
			t.Fatal("quoted missing")
		}
		if h.IdentityFile != "~/.ssh/my key.pem" {
			t.Errorf("IdentityFile = %q, want the quoted path with its space", h.IdentityFile)
		}
		for _, o := range h.Options {
			if strings.HasPrefix(strings.ToLower(o), "localforward") {
				t.Errorf("LocalForward should not be imported: %v", h.Options)
			}
		}
	})

	t.Run("Match blocks are skipped", func(t *testing.T) {
		for _, h := range hosts {
			if h.User == "matched" {
				t.Errorf("directive from a Match block leaked into %q", h.Alias)
			}
		}
	})

	t.Run("host with no HostName defaults to its alias", func(t *testing.T) {
		h, ok := byAlias(hosts, "plain")
		if !ok {
			t.Fatal("plain missing")
		}
		if h.Hostname != "plain" {
			t.Errorf("Hostname = %q, want %q", h.Hostname, "plain")
		}
	})
}

func TestParsedHostsAreValid(t *testing.T) {
	hosts, _, err := Parse(strings.NewReader(sample), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) == 0 {
		t.Fatal("no hosts parsed")
	}
	for _, h := range hosts {
		if err := h.Validate(); err != nil {
			t.Errorf("invalid imported host %+v: %v", h, err)
		}
	}
}

func TestInclude(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "work.conf")
	write(t, inc, "Host work-vpn\n    HostName 10.9.9.9\n    User dev\n")

	main := filepath.Join(dir, "config")
	write(t, main, "Include work.conf\n\nHost home\n    HostName 192.168.1.10\n")

	hosts, _, err := ParseFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := byAlias(hosts, "work-vpn"); !ok {
		t.Error("included host missing")
	}
	if h, ok := byAlias(hosts, "home"); !ok || h.Hostname != "192.168.1.10" {
		t.Errorf("host after Include is wrong: %+v ok=%v", h, ok)
	}
}

func TestIncludeGlob(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(sub, "a.conf"), "Host aaa\n  HostName a.example.com\n")
	write(t, filepath.Join(sub, "b.conf"), "Host bbb\n  HostName b.example.com\n")

	main := filepath.Join(dir, "config")
	write(t, main, "Include conf.d/*.conf\n")

	hosts, _, err := ParseFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
}

func TestIncludeCycleTerminates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	write(t, a, "Include b\nHost aaa\n  HostName a.example.com\n")
	write(t, b, "Include a\nHost bbb\n  HostName b.example.com\n")

	done := make(chan struct{})
	go func() {
		defer close(done)
		hosts, _, err := ParseFile(a)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(hosts) != 2 {
			t.Errorf("got %d hosts, want 2", len(hosts))
		}
	}()
	<-done
}

func TestMissingIncludeIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "config")
	write(t, main, "Include nothing-here.conf\nHost ok\n  HostName ok.example.com\n")

	hosts, _, err := ParseFile(main)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1", len(hosts))
	}
}

func TestDedup(t *testing.T) {
	existing := []model.Host{{Alias: "web", Hostname: "1.1.1.1"}}
	imported := []model.Host{
		{Alias: "WEB", Hostname: "2.2.2.2"}, // case-insensitive clash
		{Alias: "db", Hostname: "3.3.3.3"},
		{Alias: "db", Hostname: "4.4.4.4"}, // clash within the import itself
	}
	added, skipped := Dedup(existing, imported)

	if len(added) != 1 || added[0].Alias != "db" || added[0].Hostname != "3.3.3.3" {
		t.Errorf("added = %+v", added)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %+v, want 2", skipped)
	}
}

func TestSplitLine(t *testing.T) {
	tests := []struct{ in, key, val string }{
		{"Host web", "Host", "web"},
		{"  HostName   1.2.3.4  ", "HostName", "1.2.3.4"},
		{"User=root", "User", "root"},
		{"User = root", "User", "root"},
		{`HostName "a b"`, "HostName", "a b"},
	}
	for _, tc := range tests {
		k, v, ok := splitLine(tc.in)
		if !ok || k != tc.key || v != tc.val {
			t.Errorf("splitLine(%q) = (%q, %q, %v)", tc.in, k, v, ok)
		}
	}
	for _, bad := range []string{"", "   ", "# comment", "LoneWord"} {
		if _, _, ok := splitLine(bad); ok {
			t.Errorf("splitLine(%q) should not parse", bad)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
