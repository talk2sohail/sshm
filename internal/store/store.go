// Package store owns hosts.toml: the single source of truth for saved servers.
//
// The file is plain TOML on purpose. It is meant to be readable, diffable and
// committable to a dotfiles repo, and to be editable by hand when that is
// faster than using the UI. Every write goes through an atomic replace, so an
// interrupted save can never corrupt the list.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/sohail/sshm/internal/atomicfile"
	"github.com/sohail/sshm/internal/model"
)

// FormatVersion is bumped only for breaking schema changes. Reading a newer
// version is refused rather than silently mangling someone's file.
const FormatVersion = 1

// file is the on-disk shape.
type file struct {
	Version int          `toml:"version"`
	Hosts   []model.Host `toml:"host"`
}

// Store is an in-memory host list bound to a file.
type Store struct {
	path  string
	hosts []model.Host
	dirty bool
}

// ErrNotFound is returned when an alias does not exist.
var ErrNotFound = errors.New("host not found")

// New returns an empty store bound to path.
func New(path string) *Store { return &Store{path: path} }

// Load reads path. A missing file yields an empty store and no error, so the
// very first run works without any setup step.
func Load(path string) (*Store, error) {
	s := New(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("reading %s: %w", path, err)
	}

	var f file
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return s, fmt.Errorf("parsing %s: %w", path, err)
	}
	if f.Version > FormatVersion {
		return s, fmt.Errorf(
			"%s was written by a newer sshm (format version %d, this build understands %d)",
			path, f.Version, FormatVersion)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		// Not fatal: an unknown key is far more likely to be a typo than a
		// reason to refuse to start. Report it so the caller can warn.
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		s.hosts = f.Hosts
		s.normalize()
		return s, fmt.Errorf("%s: unrecognised keys: %s", path, strings.Join(keys, ", "))
	}

	s.hosts = f.Hosts
	s.normalize()
	if err := s.validate(); err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *Store) normalize() {
	for i := range s.hosts {
		s.hosts[i].Normalize()
	}
}

func (s *Store) validate() error {
	seen := make(map[string]struct{}, len(s.hosts))
	for _, h := range s.hosts {
		if err := h.Validate(); err != nil {
			return err
		}
		k := strings.ToLower(h.Alias)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("duplicate alias %q", h.Alias)
		}
		seen[k] = struct{}{}
	}
	return nil
}

// Path returns the bound file path.
func (s *Store) Path() string { return s.path }

// Hosts returns the current list. The slice is owned by the store; callers
// must not mutate it.
func (s *Store) Hosts() []model.Host { return s.hosts }

// Len returns the number of saved hosts.
func (s *Store) Len() int { return len(s.hosts) }

// Dirty reports whether there are unsaved changes.
func (s *Store) Dirty() bool { return s.dirty }

// Aliases returns the set of aliases, for pruning history.
func (s *Store) Aliases() map[string]struct{} {
	out := make(map[string]struct{}, len(s.hosts))
	for _, h := range s.hosts {
		out[h.Alias] = struct{}{}
	}
	return out
}

// ByAlias looks up a host case-insensitively.
func (s *Store) ByAlias(alias string) (model.Host, int, bool) {
	for i, h := range s.hosts {
		if strings.EqualFold(h.Alias, alias) {
			return h, i, true
		}
	}
	return model.Host{}, -1, false
}

// Add appends a host, rejecting duplicates and invalid entries.
func (s *Store) Add(h model.Host) error {
	h.Normalize()
	if err := h.Validate(); err != nil {
		return err
	}
	if _, _, ok := s.ByAlias(h.Alias); ok {
		return fmt.Errorf("alias %q already exists", h.Alias)
	}
	s.hosts = append(s.hosts, h)
	s.dirty = true
	return nil
}

// Update replaces the host at index i. Renaming to an alias used by a
// different host is rejected.
func (s *Store) Update(i int, h model.Host) error {
	if i < 0 || i >= len(s.hosts) {
		return ErrNotFound
	}
	h.Normalize()
	if err := h.Validate(); err != nil {
		return err
	}
	if _, j, ok := s.ByAlias(h.Alias); ok && j != i {
		return fmt.Errorf("alias %q already exists", h.Alias)
	}
	s.hosts[i] = h
	s.dirty = true
	return nil
}

// Delete removes the host at index i and returns it.
func (s *Store) Delete(i int) (model.Host, error) {
	if i < 0 || i >= len(s.hosts) {
		return model.Host{}, ErrNotFound
	}
	h := s.hosts[i]
	s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
	s.dirty = true
	return h, nil
}

// AddMany appends several hosts, skipping ones whose alias is taken. It
// returns how many were added and the aliases that were skipped.
func (s *Store) AddMany(hosts []model.Host) (added int, skipped []string) {
	for _, h := range hosts {
		if err := s.Add(h); err != nil {
			skipped = append(skipped, h.Alias)
			continue
		}
		added++
	}
	return added, skipped
}

// SortByAlias orders the list alphabetically. Display order is decided by
// search ranking, so file order exists only to keep diffs readable.
func (s *Store) SortByAlias() {
	sort.SliceStable(s.hosts, func(a, b int) bool {
		return strings.ToLower(s.hosts[a].Alias) < strings.ToLower(s.hosts[b].Alias)
	})
	s.dirty = true
}

// Save writes the file atomically with 0600 permissions. It is a no-op when
// nothing has changed.
//
// The file may reference private key paths, so it is kept owner-only even
// though it never contains secrets itself.
func (s *Store) Save() error {
	if !s.dirty {
		return nil
	}
	data, err := s.Marshal()
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(s.path, data, 0o600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// Marshal renders the store as TOML, with a short header explaining the format
// to anyone who opens the file in an editor.
func (s *Store) Marshal() ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("# sshm host list.\n")
	sb.WriteString("# Edit by hand or with `sshm` (press a to add, e to edit).\n")
	sb.WriteString("# Reference: https://github.com/sohail/sshm\n\n")

	enc := toml.NewEncoder(&sb)
	enc.Indent = ""
	if err := enc.Encode(file{Version: FormatVersion, Hosts: s.hosts}); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}
