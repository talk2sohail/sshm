package store

import (
	"os"
	"path/filepath"
	"runtime"
)

// Environment variables that let a user relocate everything, which matters for
// dotfile repos and for keeping a work profile separate from a personal one.
const (
	EnvConfig  = "SSHM_CONFIG"  // full path to hosts.toml
	EnvHistory = "SSHM_HISTORY" // full path to history.json
)

// ConfigPath returns the hosts file location.
//
// Config lives under XDG_CONFIG_HOME (~/.config/sshm) on every platform,
// including macOS. That is deliberate: this file is meant to be read, edited
// and committed to a dotfiles repo, and ~/Library/Application Support is a
// hostile place to keep something like that.
func ConfigPath() string {
	if p := os.Getenv(EnvConfig); p != "" {
		return p
	}
	return filepath.Join(configDir(), "hosts.toml")
}

// HistoryPath returns the frecency history file.
//
// History is machine-local, disposable state, so it goes under XDG_STATE_HOME
// and should not be committed anywhere.
func HistoryPath() string {
	if p := os.Getenv(EnvHistory); p != "" {
		return p
	}
	return filepath.Join(stateDir(), "history.json")
}

func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "sshm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "sshm")
}

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "sshm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "sshm")
		}
	}
	return filepath.Join(home, ".local", "state", "sshm")
}
