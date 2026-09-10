package fuzzy

import (
	"sort"
	"testing"
)

func TestMatchBasics(t *testing.T) {
	tests := []struct {
		pattern, text string
		want          bool
	}{
		{"", "anything", true},
		{"abc", "", false},
		{"prod", "prod-api", true},
		{"pa", "prod-api", true},
		{"papi", "prod-api", true},
		{"xyz", "prod-api", false},
		{"prodapi", "prod-api", true},
		{"prodx", "prod-api", false},
	}
	for _, tc := range tests {
		if _, ok := Match(tc.pattern, tc.text); ok != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.pattern, tc.text, ok, tc.want)
		}
	}
}

func TestSmartCase(t *testing.T) {
	if _, ok := Match("api", "PROD-API"); !ok {
		t.Error("lowercase pattern should match case-insensitively")
	}
	if _, ok := Match("API", "prod-api"); ok {
		t.Error("uppercase pattern should be case-sensitive")
	}
	if _, ok := Match("API", "prod-API"); !ok {
		t.Error("uppercase pattern should match exact case")
	}
}

func TestPositionsAreByteOffsets(t *testing.T) {
	r, ok := Match("pa", "prod-api")
	if !ok {
		t.Fatal("expected match")
	}
	if len(r.Positions) != 2 {
		t.Fatalf("positions = %v, want 2 entries", r.Positions)
	}
	for _, p := range r.Positions {
		if p < 0 || p >= len("prod-api") {
			t.Fatalf("position %d out of range", p)
		}
	}
	if !sort.IntsAreSorted(r.Positions) {
		t.Errorf("positions not ascending: %v", r.Positions)
	}
}

func TestPositionsWithMultibyte(t *testing.T) {
	text := "héllo-world"
	r, ok := Match("hw", text)
	if !ok {
		t.Fatal("expected match")
	}
	for _, p := range r.Positions {
		if p >= len(text) {
			t.Fatalf("byte offset %d exceeds len %d", p, len(text))
		}
	}
	// The 'w' offset must land exactly on the 'w' byte.
	last := r.Positions[len(r.Positions)-1]
	if text[last] != 'w' {
		t.Errorf("offset %d points at %q, want 'w'", last, text[last])
	}
}

// scoreOf is a helper that fails the test if the pattern does not match.
func scoreOf(t *testing.T, pattern, text string) int {
	t.Helper()
	r, ok := Match(pattern, text)
	if !ok {
		t.Fatalf("Match(%q, %q) did not match", pattern, text)
	}
	return r.Score
}

func TestPrefixBeatsMidword(t *testing.T) {
	if a, b := scoreOf(t, "api", "api-gateway"), scoreOf(t, "api", "legacy-rapid"); a <= b {
		t.Errorf("prefix match scored %d, mid-word scored %d; prefix should win", a, b)
	}
}

func TestBoundaryBeatsScattered(t *testing.T) {
	// "pa" as the start of two words beats "pa" scattered inside one word.
	if a, b := scoreOf(t, "pa", "prod-api"), scoreOf(t, "pa", "postgresql-alpha-legacy-node"); a <= b {
		t.Errorf("boundary match %d should beat scattered %d", a, b)
	}
}

func TestConsecutiveBeatsGapped(t *testing.T) {
	if a, b := scoreOf(t, "abc", "abcdef"), scoreOf(t, "abc", "axbxcx"); a <= b {
		t.Errorf("consecutive %d should beat gapped %d", a, b)
	}
}

func TestShorterCandidatePreferred(t *testing.T) {
	if a, b := scoreOf(t, "db", "db"), scoreOf(t, "db", "db-replica-eu-west-1-standby"); a <= b {
		t.Errorf("exact short %d should beat long %d", a, b)
	}
}

func TestBest(t *testing.T) {
	fields := []Field{
		{Text: "web-01", Weight: 100},
		{Text: "10.0.0.5", Weight: 80},
		{Text: "prod aws", Weight: 70},
	}

	score, idx, ok := Best("web", fields)
	if !ok {
		t.Fatal("expected a match")
	}
	if idx != 0 {
		t.Errorf("matched field %d, want 0 (alias)", idx)
	}
	if score <= 0 {
		t.Errorf("score = %d, want positive", score)
	}
	if pos := Positions("web", fields[idx].Text); len(pos) != 3 {
		t.Errorf("positions = %v, want 3", pos)
	}

	if _, idx, ok := Best("aws", fields); !ok || idx != 2 {
		t.Errorf("tag match: idx = %d, ok = %v, want idx 2", idx, ok)
	}

	if _, _, ok := Best("zzz", fields); ok {
		t.Error("expected no match")
	}
	if pos := Positions("zzz", "abc"); pos != nil {
		t.Errorf("Positions on a non-match = %v, want nil", pos)
	}
}

func TestBestWeightingPrefersAlias(t *testing.T) {
	// The same text in both fields: the heavier field must win.
	fields := []Field{
		{Text: "cache", Weight: 100},
		{Text: "cache", Weight: 40},
	}
	if _, idx, ok := Best("cache", fields); !ok || idx != 0 {
		t.Errorf("idx = %d, want 0", idx)
	}
}

func BenchmarkMatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Match("papi", "prod-api-gateway-eu-west-1")
	}
}
