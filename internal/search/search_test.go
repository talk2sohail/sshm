package search

import (
	"reflect"
	"testing"
	"time"

	"github.com/sohail/sshm/internal/frecency"
	"github.com/sohail/sshm/internal/model"
)

func fixtures() []model.Host {
	return []model.Host{
		{Alias: "prod-api", Hostname: "10.0.4.21", User: "ubuntu", Tags: []string{"prod", "aws"}, Description: "main api"},
		{Alias: "prod-db", Hostname: "10.0.4.30", User: "postgres", Tags: []string{"prod", "aws", "db"}},
		{Alias: "staging-api", Hostname: "10.9.0.5", User: "ubuntu", Tags: []string{"staging", "aws"}},
		{Alias: "home-nas", Hostname: "192.168.1.10", User: "sohail", Tags: []string{"home"}},
		{Alias: "bastion", Hostname: "jump.example.com", Tags: []string{"prod", "aws"}},
	}
}

func aliases(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Host.Alias
	}
	return out
}

func TestParseQuery(t *testing.T) {
	tests := []struct {
		raw  string
		want Query
	}{
		{"", Query{}},
		{"prod api", Query{Text: "prod api"}},
		{"#aws", Query{Tags: []string{"aws"}}},
		{"#AWS api", Query{Text: "api", Tags: []string{"aws"}}},
		{"#aws #db", Query{Tags: []string{"aws", "db"}}},
		{"!#staging api", Query{Text: "api", ExcludeTags: []string{"staging"}}},
		{"#", Query{Text: "#"}},   // a bare # is not a tag filter
		{"!#", Query{Text: "!#"}}, // nor a bare !#
	}
	for _, tc := range tests {
		if got := ParseQuery(tc.raw); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseQuery(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestEmpty(t *testing.T) {
	if !ParseQuery("  ").Empty() {
		t.Error("blank query should be empty")
	}
	if ParseQuery("#aws").Empty() {
		t.Error("tag-only query is not empty")
	}
}

func TestFilterEmptyQueryReturnsEverything(t *testing.T) {
	hosts := fixtures()
	hits := Filter(hosts, ParseQuery(""), nil, time.Now())
	if len(hits) != len(hosts) {
		t.Fatalf("got %d hits, want %d", len(hits), len(hosts))
	}
}

func TestFilterByTag(t *testing.T) {
	hits := Filter(fixtures(), ParseQuery("#db"), nil, time.Now())
	if got := aliases(hits); !reflect.DeepEqual(got, []string{"prod-db"}) {
		t.Errorf("got %v, want [prod-db]", got)
	}
}

func TestFilterByMultipleTagsIsAnd(t *testing.T) {
	hits := Filter(fixtures(), ParseQuery("#aws #db"), nil, time.Now())
	if got := aliases(hits); !reflect.DeepEqual(got, []string{"prod-db"}) {
		t.Errorf("got %v, want [prod-db]", got)
	}
}

func TestExcludeTag(t *testing.T) {
	hits := Filter(fixtures(), ParseQuery("!#prod"), nil, time.Now())
	for _, h := range hits {
		if h.Host.HasTag("prod") {
			t.Errorf("%s should have been excluded", h.Host.Alias)
		}
	}
	if len(hits) != 2 {
		t.Errorf("got %v, want 2 hosts", aliases(hits))
	}
}

func TestTagAndTextCombine(t *testing.T) {
	hits := Filter(fixtures(), ParseQuery("#prod api"), nil, time.Now())
	if got := aliases(hits); !reflect.DeepEqual(got, []string{"prod-api"}) {
		t.Errorf("got %v, want [prod-api]", got)
	}
}

func TestAliasMatchOutranksDescriptionMatch(t *testing.T) {
	hosts := []model.Host{
		{Alias: "unrelated", Hostname: "h1", Description: "the api box"},
		{Alias: "api", Hostname: "h2"},
	}
	hits := Filter(hosts, ParseQuery("api"), nil, time.Now())
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Host.Alias != "api" {
		t.Errorf("ranked %v first, want alias match", aliases(hits))
	}
}

func TestMatchOnHostname(t *testing.T) {
	hits := Filter(fixtures(), ParseQuery("192.168"), nil, time.Now())
	if got := aliases(hits); !reflect.DeepEqual(got, []string{"home-nas"}) {
		t.Errorf("got %v, want [home-nas]", got)
	}
	if hits[0].Field != FieldHostname {
		t.Errorf("Field = %d, want FieldHostname", hits[0].Field)
	}
}

func TestFrecencyOrdersEmptyQuery(t *testing.T) {
	now := time.Now()
	hist := frecency.New("")
	for i := 0; i < 5; i++ {
		hist.Record("home-nas", now.Add(-time.Duration(i)*time.Hour))
	}
	hist.Record("bastion", now.Add(-40*24*time.Hour))

	hits := Filter(fixtures(), ParseQuery(""), hist, now)
	if hits[0].Host.Alias != "home-nas" {
		t.Errorf("first hit = %q, want home-nas (highest frecency); order was %v",
			hits[0].Host.Alias, aliases(hits))
	}
}

func TestFrecencyBreaksTiesButDoesNotOverrideMatchQuality(t *testing.T) {
	now := time.Now()
	hist := frecency.New("")
	// Heavily favour the worse textual match.
	for i := 0; i < 30; i++ {
		hist.Record("staging-api", now)
	}
	hits := Filter(fixtures(), ParseQuery("prod-api"), hist, now)
	if hits[0].Host.Alias != "prod-api" {
		t.Errorf("first hit = %q, want prod-api; history must not beat an exact match (order %v)",
			hits[0].Host.Alias, aliases(hits))
	}
}

func TestFilterIsStableAndCarriesIndex(t *testing.T) {
	hosts := fixtures()
	hits := Filter(hosts, ParseQuery(""), nil, time.Now())
	for _, h := range hits {
		if hosts[h.Index].Alias != h.Host.Alias {
			t.Errorf("Index %d points at %q, want %q", h.Index, hosts[h.Index].Alias, h.Host.Alias)
		}
	}
}

func TestNoMatch(t *testing.T) {
	if hits := Filter(fixtures(), ParseQuery("zzzzzz"), nil, time.Now()); len(hits) != 0 {
		t.Errorf("got %v, want none", aliases(hits))
	}
}

func TestResolveExactAlias(t *testing.T) {
	// "prod-db" also fuzzy-matches others, but an exact alias must win outright.
	h, cands, ok := Resolve(fixtures(), "prod-db")
	if !ok {
		t.Fatalf("expected a unique resolution, got candidates %v", cands)
	}
	if h.Alias != "prod-db" {
		t.Errorf("resolved %q", h.Alias)
	}
}

func TestResolveExactAliasIsCaseInsensitive(t *testing.T) {
	h, _, ok := Resolve(fixtures(), "PROD-DB")
	if !ok || h.Alias != "prod-db" {
		t.Errorf("resolved %+v ok=%v", h, ok)
	}
}

func TestResolveUniqueFuzzy(t *testing.T) {
	h, cands, ok := Resolve(fixtures(), "hnas")
	if !ok || h.Alias != "home-nas" {
		t.Errorf("resolved %q ok=%v cands=%v, want home-nas", h.Alias, ok, cands)
	}
}

// A query that looks unique against aliases can still match another host
// through its tags or description. Resolve must notice and refuse to guess:
// silently opening a session to the wrong server is the worst failure this
// tool can have.
func TestResolveConsidersAllSearchableFields(t *testing.T) {
	// "nas" matches home-nas by alias, and staging-api through "staging aws".
	if h, cands, ok := Resolve(fixtures(), "nas"); ok {
		t.Errorf("resolved %q, but %d candidates matched", h.Alias, len(cands)+1)
	}
}

func TestResolveAmbiguousReturnsCandidates(t *testing.T) {
	h, cands, ok := Resolve(fixtures(), "api")
	if ok {
		t.Fatalf("ambiguous query resolved to %q; it must not guess", h.Alias)
	}
	if len(cands) < 2 {
		t.Errorf("candidates = %v, want at least 2", cands)
	}
}

func TestResolveNoMatch(t *testing.T) {
	_, cands, ok := Resolve(fixtures(), "zzzz")
	if ok || len(cands) != 0 {
		t.Errorf("ok=%v cands=%v, want no resolution", ok, cands)
	}
}

func BenchmarkFilter1000(b *testing.B) {
	hosts := make([]model.Host, 0, 1000)
	for i := 0; i < 1000; i++ {
		hosts = append(hosts, model.Host{
			Alias:    "server-" + string(rune('a'+i%26)) + "-prod-eu",
			Hostname: "10.0.0.1",
			Tags:     []string{"prod", "aws"},
		})
	}
	q := ParseQuery("spe")
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Filter(hosts, q, nil, now)
	}
}

func BenchmarkIndexFilter1000(b *testing.B) {
	hosts := make([]model.Host, 0, 1000)
	for i := 0; i < 1000; i++ {
		hosts = append(hosts, model.Host{
			Alias:    "server-" + string(rune('a'+i%26)) + "-prod-eu",
			Hostname: "10.0.0.1",
			Tags:     []string{"prod", "aws"},
		})
	}
	ix := NewIndex(hosts)
	q := ParseQuery("spe")
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Filter(q, nil, now)
	}
}
