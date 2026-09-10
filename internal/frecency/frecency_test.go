package frecency

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScoreDecaysByHalfLife(t *testing.T) {
	now := time.Now()
	s := New("")
	s.Record("a", now)

	fresh := s.Score("a", now)
	if fresh < 99 || fresh > 101 {
		t.Fatalf("fresh score = %v, want ~100", fresh)
	}

	oneHalfLife := s.Score("a", now.Add(HalfLife))
	if oneHalfLife < 49 || oneHalfLife > 51 {
		t.Errorf("score after one half-life = %v, want ~50", oneHalfLife)
	}

	twoHalfLives := s.Score("a", now.Add(2*HalfLife))
	if twoHalfLives < 24 || twoHalfLives > 26 {
		t.Errorf("score after two half-lives = %v, want ~25", twoHalfLives)
	}
}

func TestFrequencyBeatsSingleRecentVisit(t *testing.T) {
	now := time.Now()
	s := New("")
	// "daily" was used ten times over the last week.
	for i := 0; i < 10; i++ {
		s.Record("daily", now.Add(-time.Duration(i)*24*time.Hour))
	}
	// "once" was used a single time, just now.
	s.Record("once", now)

	if s.Score("daily", now) <= s.Score("once", now) {
		t.Errorf("daily (%v) should outrank once (%v)",
			s.Score("daily", now), s.Score("once", now))
	}
}

func TestRecentBeatsStale(t *testing.T) {
	now := time.Now()
	s := New("")
	for i := 0; i < 5; i++ {
		s.Record("stale", now.Add(-90*24*time.Hour))
	}
	s.Record("recent", now)

	if s.Score("recent", now) <= s.Score("stale", now) {
		t.Errorf("recent (%v) should outrank 90-day-old cluster (%v)",
			s.Score("recent", now), s.Score("stale", now))
	}
}

func TestUnknownAliasScoresZero(t *testing.T) {
	s := New("")
	if got := s.Score("nope", time.Now()); got != 0 {
		t.Errorf("score = %v, want 0", got)
	}
}

func TestClockSkewDoesNotExplode(t *testing.T) {
	now := time.Now()
	s := New("")
	s.Record("future", now.Add(48*time.Hour)) // clock jumped backwards since
	got := s.Score("future", now)
	if got < 99 || got > 101 {
		t.Errorf("score = %v, want clamped to ~100", got)
	}
}

func TestVisitsAreCapped(t *testing.T) {
	now := time.Now()
	s := New("")
	for i := 0; i < maxVisits*3; i++ {
		s.Record("a", now)
	}
	if got := len(s.Entries["a"].Visits); got != maxVisits {
		t.Errorf("visits = %d, want %d", got, maxVisits)
	}
	if got := s.Count("a"); got != maxVisits*3 {
		t.Errorf("lifetime count = %d, want %d", got, maxVisits*3)
	}
}

func TestRenamePreservesHistory(t *testing.T) {
	now := time.Now()
	s := New("")
	s.Record("old", now)
	before := s.Score("old", now)

	s.Rename("old", "new")
	if s.Score("old", now) != 0 {
		t.Error("old alias should have no history left")
	}
	if got := s.Score("new", now); got != before {
		t.Errorf("new score = %v, want %v", got, before)
	}
}

func TestPrune(t *testing.T) {
	now := time.Now()
	s := New("")
	s.Record("keep", now)
	s.Record("drop", now)
	s.Prune(map[string]struct{}{"keep": {}})

	if _, ok := s.Entries["drop"]; ok {
		t.Error("pruned entry still present")
	}
	if _, ok := s.Entries["keep"]; !ok {
		t.Error("kept entry was removed")
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	now := time.Now().Truncate(time.Second)

	s := New(path)
	s.Record("web", now)
	s.Record("web", now)
	s.Record("db", now)
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
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("history perms = %#o, want 0600", perm)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Count("web"); got != 2 {
		t.Errorf("count = %d, want 2", got)
	}
	last, ok := loaded.LastUsed("web")
	if !ok || !last.Equal(now) {
		t.Errorf("last = %v (ok=%v), want %v", last, ok, now)
	}
	if loaded.Score("db", now) <= 0 {
		t.Error("db lost its score across the round trip")
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil || len(s.Entries) != 0 {
		t.Fatal("expected a usable empty store")
	}
}

func TestLoadCorruptFileStaysUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err == nil {
		t.Error("expected an error describing the corruption")
	}
	if s == nil {
		t.Fatal("store must still be usable after a corrupt read")
	}
	s.Record("a", time.Now()) // must not panic
	if s.Score("a", time.Now()) <= 0 {
		t.Error("store unusable after corrupt load")
	}
}

func TestSaveIsNoOpWhenClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	s := New(path)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("clean store should not have written a file")
	}
}
