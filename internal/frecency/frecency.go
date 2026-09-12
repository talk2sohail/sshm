// Package frecency tracks how often and how recently each host was used, so
// the servers someone actually works with float to the top of an unfiltered
// list.
//
// The score is a sum of exponentially decaying visit weights. A visit today is
// worth 100; after one half-life it is worth 50, and so on. That makes the
// ranking respond quickly when someone switches projects, while still
// remembering long-standing favourites.
package frecency

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"sort"
	"time"

	"github.com/talk2sohail/sshm/internal/atomicfile"
)

const (
	// HalfLife is how long it takes a single visit's contribution to halve.
	HalfLife = 7 * 24 * time.Hour

	// maxVisits caps the per-host history. Older visits contribute so little
	// that keeping more is pure cost.
	maxVisits = 32

	formatVersion = 1
)

// Stat is the recorded history for one alias.
type Stat struct {
	Count  int     `json:"count"`  // lifetime connection count
	Last   int64   `json:"last"`   // unix seconds of the most recent connection
	Visits []int64 `json:"visits"` // unix seconds, ascending, capped at maxVisits
}

// Store is the on-disk history file.
type Store struct {
	Version int              `json:"version"`
	Entries map[string]*Stat `json:"entries"`

	path  string
	dirty bool
}

// New returns an empty store bound to path.
func New(path string) *Store {
	return &Store{Version: formatVersion, Entries: map[string]*Stat{}, path: path}
}

// Load reads the history file. A missing or corrupt file is not an error:
// history is a convenience, and losing it must never stop the tool from
// starting. A corrupt file is reported so the caller can mention it.
func Load(path string) (*Store, error) {
	s := New(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if len(data) == 0 {
		return s, nil
	}
	var loaded Store
	if err := json.Unmarshal(data, &loaded); err != nil {
		return s, err // s is still usable and empty
	}
	if loaded.Entries == nil {
		loaded.Entries = map[string]*Stat{}
	}
	loaded.path = path
	loaded.Version = formatVersion
	return &loaded, nil
}

// Path returns the file this store is bound to.
func (s *Store) Path() string { return s.path }

// Dirty reports whether there are unsaved changes.
func (s *Store) Dirty() bool { return s.dirty }

// Record registers a connection to alias at time now.
func (s *Store) Record(alias string, now time.Time) {
	if alias == "" {
		return
	}
	if s.Entries == nil {
		s.Entries = map[string]*Stat{}
	}
	st := s.Entries[alias]
	if st == nil {
		st = &Stat{}
		s.Entries[alias] = st
	}
	ts := now.Unix()
	st.Count++
	st.Last = ts
	st.Visits = append(st.Visits, ts)
	if len(st.Visits) > maxVisits {
		st.Visits = append(st.Visits[:0], st.Visits[len(st.Visits)-maxVisits:]...)
	}
	s.dirty = true
}

// Rename moves history from one alias to another, preserving rank when a user
// renames a server in the TUI.
func (s *Store) Rename(oldAlias, newAlias string) {
	if oldAlias == newAlias || s.Entries == nil {
		return
	}
	st, ok := s.Entries[oldAlias]
	if !ok {
		return
	}
	delete(s.Entries, oldAlias)
	s.Entries[newAlias] = st
	s.dirty = true
}

// Forget drops all history for an alias.
func (s *Store) Forget(alias string) {
	if s.Entries == nil {
		return
	}
	if _, ok := s.Entries[alias]; ok {
		delete(s.Entries, alias)
		s.dirty = true
	}
}

// Score returns the decayed frecency score for alias. Unknown aliases score 0.
func (s *Store) Score(alias string, now time.Time) float64 {
	st := s.Entries[alias]
	if st == nil || len(st.Visits) == 0 {
		return 0
	}
	halfLife := HalfLife.Seconds()
	nowUnix := now.Unix()
	total := 0.0
	for _, v := range st.Visits {
		age := float64(nowUnix - v)
		if age < 0 {
			age = 0 // clock skew; treat as brand new
		}
		total += 100 * math.Pow(0.5, age/halfLife)
	}
	return total
}

// LastUsed returns the time of the most recent connection, and false if never.
func (s *Store) LastUsed(alias string) (time.Time, bool) {
	st := s.Entries[alias]
	if st == nil || st.Last == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Last, 0), true
}

// Count returns the lifetime connection count for alias.
func (s *Store) Count(alias string) int {
	if st := s.Entries[alias]; st != nil {
		return st.Count
	}
	return 0
}

// Prune removes entries whose alias is no longer in keep. Called after loading
// the host list so history for deleted servers does not accumulate forever.
func (s *Store) Prune(keep map[string]struct{}) {
	for alias := range s.Entries {
		if _, ok := keep[alias]; !ok {
			delete(s.Entries, alias)
			s.dirty = true
		}
	}
}

// Save writes the store atomically. It is a no-op when nothing changed.
func (s *Store) Save() error {
	if !s.dirty || s.path == "" {
		return nil
	}
	// Keep visit slices sorted and bounded before writing.
	for _, st := range s.Entries {
		sort.Slice(st.Visits, func(i, j int) bool { return st.Visits[i] < st.Visits[j] })
	}
	s.Version = formatVersion
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(s.path, data, 0o600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}
