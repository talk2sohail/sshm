package model

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	h := Host{
		Alias: "  api  ",
		Tags:  []string{"Prod", "aws", "prod", "  ", "DB"},
		Port:  22,
	}
	h.Normalize()

	if h.Alias != "api" {
		t.Errorf("alias not trimmed: %q", h.Alias)
	}
	if want := []string{"aws", "db", "prod"}; !reflect.DeepEqual(h.Tags, want) {
		t.Errorf("tags = %v, want %v", h.Tags, want)
	}
	if h.Port != 0 {
		t.Errorf("default port should be elided, got %d", h.Port)
	}
	if h.EffectivePort() != 22 {
		t.Errorf("EffectivePort = %d, want 22", h.EffectivePort())
	}
}

func TestNormalizeEmptiesToNil(t *testing.T) {
	h := Host{Alias: "a", Hostname: "h", Tags: []string{"", "  "}, Options: []string{"  "}}
	h.Normalize()
	if h.Tags != nil {
		t.Errorf("Tags = %v, want nil", h.Tags)
	}
	if h.Options != nil {
		t.Errorf("Options = %v, want nil", h.Options)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		host    Host
		wantErr string
	}{
		{"ok", Host{Alias: "web", Hostname: "10.0.0.1"}, ""},
		{"no alias", Host{Hostname: "h"}, "alias is required"},
		{"no hostname", Host{Alias: "web"}, "hostname is required"},
		{"space in alias", Host{Alias: "my web", Hostname: "h"}, "shell metacharacters"},
		{"shell meta in alias", Host{Alias: "web;rm", Hostname: "h"}, "shell metacharacters"},
		{"bad port", Host{Alias: "web", Hostname: "h", Port: 70000}, "out of range"},
		{"bad option", Host{Alias: "web", Hostname: "h", Options: []string{"NoEquals"}}, "Key=Value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.host.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAddrIPv6(t *testing.T) {
	h := Host{Alias: "v6", Hostname: "2001:db8::1", Port: 2222}
	if got, want := h.Addr(), "[2001:db8::1]:2222"; got != want {
		t.Errorf("Addr = %q, want %q", got, want)
	}
}

func TestSSHArgs(t *testing.T) {
	h := Host{
		Alias:        "prod",
		Hostname:     "10.0.4.21",
		User:         "ubuntu",
		Port:         2222,
		IdentityFile: "/keys/prod.pem",
		ProxyJump:    "bastion",
		Options:      []string{"ServerAliveInterval=30"},
	}
	got := h.SSHArgs(nil)
	want := []string{
		"-p", "2222",
		"-i", "/keys/prod.pem",
		"-o", "IdentitiesOnly=yes",
		"-J", "bastion",
		"-o", "ServerAliveInterval=30",
		"-tt",
		"ubuntu@10.0.4.21",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SSHArgs =\n %v\nwant\n %v", got, want)
	}
}

func TestSSHArgsMinimal(t *testing.T) {
	h := Host{Alias: "box", Hostname: "example.com"}
	got := h.SSHArgs(nil)
	if !reflect.DeepEqual(got, []string{"-tt", "example.com"}) {
		t.Errorf("SSHArgs = %v, want [-tt example.com]", got)
	}
	// Port 22 must not produce a -p flag.
	h.Port = 22
	if got := h.SSHArgs(nil); len(got) != 2 {
		t.Errorf("explicit port 22 should not add flags, got %v", got)
	}
}

func TestSSHArgsExtraPassthrough(t *testing.T) {
	h := Host{Alias: "box", Hostname: "example.com"}
	got := h.SSHArgs([]string{"uptime"})
	// -tt is forced even with a trailing remote command: sshm hands the
	// terminal to ssh unconditionally, so an interactive remote command
	// (top, a shell, etc.) still gets a pty.
	want := []string{"-tt", "example.com", "uptime"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SSHArgs = %v, want %v", got, want)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if got, want := ExpandPath("~/x/y.pem"), filepath.Join(home, "x/y.pem"); got != want {
		t.Errorf("ExpandPath = %q, want %q", got, want)
	}
	if got := ExpandPath("/abs/path"); got != "/abs/path" {
		t.Errorf("absolute path was rewritten to %q", got)
	}
	if got := ExpandPath("~other/x"); got != "~other/x" {
		t.Errorf("~user path was rewritten to %q", got)
	}
}

func TestCheckIdentity(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		h := Host{Alias: "a", Hostname: "h", IdentityFile: filepath.Join(dir, "nope.pem")}
		p := h.CheckIdentity()
		if p == nil || !strings.Contains(p.Message, "not found") {
			t.Fatalf("problem = %+v, want not-found", p)
		}
	})

	t.Run("loose permissions", func(t *testing.T) {
		key := filepath.Join(dir, "loose.pem")
		if err := os.WriteFile(key, []byte("k"), 0o644); err != nil {
			t.Fatal(err)
		}
		h := Host{Alias: "a", Hostname: "h", IdentityFile: key}
		p := h.CheckIdentity()
		if p == nil || !strings.Contains(p.Message, "chmod 600") {
			t.Fatalf("problem = %+v, want permission warning", p)
		}
	})

	t.Run("good", func(t *testing.T) {
		key := filepath.Join(dir, "good.pem")
		if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
			t.Fatal(err)
		}
		h := Host{Alias: "a", Hostname: "h", IdentityFile: key}
		if p := h.CheckIdentity(); p != nil {
			t.Fatalf("unexpected problem: %+v", p)
		}
	})

	t.Run("none configured", func(t *testing.T) {
		h := Host{Alias: "a", Hostname: "h"}
		if p := h.CheckIdentity(); p != nil {
			t.Fatalf("unexpected problem: %+v", p)
		}
	})
}

func TestHasTagAndAllTags(t *testing.T) {
	hosts := []Host{
		{Alias: "a", Tags: []string{"prod", "aws"}},
		{Alias: "b", Tags: []string{"AWS", "db"}},
	}
	if !hosts[0].HasTag("PROD") {
		t.Error("HasTag should be case-insensitive")
	}
	if hosts[0].HasTag("") {
		t.Error("empty tag must not match")
	}
	want := []string{"aws", "db", "prod"}
	if got := AllTags(hosts); !reflect.DeepEqual(got, want) {
		t.Errorf("AllTags = %v, want %v", got, want)
	}
}

func TestCommandLineQuoting(t *testing.T) {
	h := Host{Alias: "a", Hostname: "example.com", IdentityFile: "/k/my key.pem"}
	got := h.CommandLine(nil)
	if !strings.Contains(got, `"/k/my key.pem"`) {
		t.Errorf("path with space not quoted: %s", got)
	}
}
