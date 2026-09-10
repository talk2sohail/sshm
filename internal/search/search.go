// Package search turns what the user typed into a ranked, filtered host list.
//
// The query language is deliberately tiny, because anything a user has to
// remember defeats the point of the tool:
//
//	prod api          fuzzy match on both words
//	#aws              only hosts tagged aws
//	!#staging         exclude hosts tagged staging
//	#aws #db api      combine: tagged aws AND db, fuzzy "api"
//
// Everything that is not a #tag token is joined back together and matched
// fuzzily against the alias, hostname, tags, user and description.
package search

import (
	"sort"
	"strings"
	"time"

	"github.com/sohail/sshm/internal/fuzzy"
	"github.com/sohail/sshm/internal/model"
)

// Field weights. An alias hit matters most: it is the name the user chose.
const (
	weightAlias    = 100
	weightHostname = 82
	weightTag      = 70
	weightUser     = 55
	weightDesc     = 45
)

// frecencyWeight scales the frecency score before it is added to the fuzzy
// score. Tuned so history breaks ties and lifts daily servers, but never
// outranks a clearly better textual match.
const frecencyWeight = 0.45

// Query is a parsed search string.
type Query struct {
	Text        string   // the fuzzy portion, with #tokens removed
	Tags        []string // required tags (AND)
	ExcludeTags []string // forbidden tags
}

// Empty reports whether the query filters nothing out.
func (q Query) Empty() bool {
	return q.Text == "" && len(q.Tags) == 0 && len(q.ExcludeTags) == 0
}

// ParseQuery splits raw input into tag filters and fuzzy text.
func ParseQuery(raw string) Query {
	var q Query
	var words []string
	for _, tok := range strings.Fields(raw) {
		switch {
		case strings.HasPrefix(tok, "!#") && len(tok) > 2:
			q.ExcludeTags = append(q.ExcludeTags, strings.ToLower(tok[2:]))
		case strings.HasPrefix(tok, "#") && len(tok) > 1:
			q.Tags = append(q.Tags, strings.ToLower(tok[1:]))
		default:
			words = append(words, tok)
		}
	}
	q.Text = strings.Join(words, " ")
	return q
}

// Hit is one host that survived filtering, with its ranking inputs.
type Hit struct {
	Host     model.Host
	Index    int // index into the original slice, stable across re-filters
	Score    int // combined fuzzy + frecency score
	Field    int // which field matched; see Field* constants
	Frecency float64
}

// MatchText returns the text of the field this hit matched on, which is what
// Highlight's offsets refer to.
func (h Hit) MatchText() string {
	switch h.Field {
	case FieldHostname:
		return h.Host.Hostname
	case FieldTags:
		return strings.Join(h.Host.Tags, " ")
	case FieldUser:
		return h.Host.User
	case FieldDesc:
		return h.Host.Description
	default:
		return h.Host.Alias
	}
}

// Highlight returns the byte offsets to emphasise for this hit under query q.
// It is computed on demand, for visible rows only.
func (h Hit) Highlight(q Query) []int {
	if q.Text == "" {
		return nil
	}
	return fuzzy.Positions(q.Text, h.MatchText())
}

// Which field a Hit's Positions refer to.
const (
	FieldAlias = iota
	FieldHostname
	FieldTags
	FieldUser
	FieldDesc
)

// Ranker holds the inputs to ranking that do not change per keystroke.
type Ranker interface {
	Score(alias string, now time.Time) float64
}

// Index holds a host list plus the per-host strings that searching needs, so
// that nothing is recomputed on every keystroke.
//
// Build it once when the host list changes; call Filter on every keypress.
type Index struct {
	hosts   []model.Host
	tagText []string      // Tags joined, parallel to hosts
	scratch []fuzzy.Field // reused per host to keep Filter allocation-free
	hits    []Hit         // reused result buffer
}

// NewIndex precomputes the searchable text for hosts.
func NewIndex(hosts []model.Host) *Index {
	ix := &Index{
		hosts:   hosts,
		tagText: make([]string, len(hosts)),
		scratch: make([]fuzzy.Field, 5),
		hits:    make([]Hit, 0, len(hosts)),
	}
	for i, h := range hosts {
		ix.tagText[i] = strings.Join(h.Tags, " ")
	}
	return ix
}

// Hosts returns the indexed host list.
func (ix *Index) Hosts() []model.Host { return ix.hosts }

// Len returns the number of indexed hosts.
func (ix *Index) Len() int { return len(ix.hosts) }

// Filter applies q and returns hits in ranked order.
//
// The returned slice is owned by the Index and is overwritten by the next
// Filter call, which is exactly what a redraw loop wants. It does no I/O and,
// after the first call, no allocation.
func (ix *Index) Filter(q Query, r Ranker, now time.Time) []Hit {
	hits := ix.hits[:0]

	for i := range ix.hosts {
		h := &ix.hosts[i]
		if !matchesTags(h, q) {
			continue
		}

		hit := Hit{Host: *h, Index: i, Field: FieldAlias}
		if r != nil {
			hit.Frecency = r.Score(h.Alias, now)
		}

		if q.Text == "" {
			hit.Score = int(hit.Frecency * 100)
			hits = append(hits, hit)
			continue
		}

		f := ix.scratch
		f[0] = fuzzy.Field{Text: h.Alias, Weight: weightAlias}
		f[1] = fuzzy.Field{Text: h.Hostname, Weight: weightHostname}
		f[2] = fuzzy.Field{Text: ix.tagText[i], Weight: weightTag}
		f[3] = fuzzy.Field{Text: h.User, Weight: weightUser}
		f[4] = fuzzy.Field{Text: h.Description, Weight: weightDesc}

		score, idx, ok := fuzzy.Best(q.Text, f)
		if !ok {
			continue
		}
		hit.Score = score + int(hit.Frecency*frecencyWeight)
		hit.Field = idx
		hits = append(hits, hit)
	}

	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].Score != hits[b].Score {
			return hits[a].Score > hits[b].Score
		}
		if hits[a].Frecency != hits[b].Frecency {
			return hits[a].Frecency > hits[b].Frecency
		}
		return hits[a].Host.Alias < hits[b].Host.Alias
	})

	ix.hits = hits
	return hits
}

// Filter is a convenience wrapper that builds a throwaway index. Use Index
// directly in the render loop.
func Filter(hosts []model.Host, q Query, r Ranker, now time.Time) []Hit {
	return NewIndex(hosts).Filter(q, r, now)
}

func matchesTags(h *model.Host, q Query) bool {
	for _, t := range q.Tags {
		if !h.HasTag(t) {
			return false
		}
	}
	for _, t := range q.ExcludeTags {
		if h.HasTag(t) {
			return false
		}
	}
	return true
}

// Resolve finds the single host a non-interactive command refers to.
//
// It returns exactly one host when the query is an exact alias match, or when
// fuzzy matching leaves exactly one candidate. Otherwise it returns the full
// candidate list so the caller can show the TUI pre-filtered instead of
// guessing — connecting to the wrong server is much worse than one extra
// keystroke.
func Resolve(hosts []model.Host, raw string) (model.Host, []model.Host, bool) {
	for _, h := range hosts {
		if strings.EqualFold(h.Alias, raw) {
			return h, nil, true
		}
	}
	q := ParseQuery(raw)
	hits := Filter(hosts, q, nil, time.Now())
	if len(hits) == 1 {
		return hits[0].Host, nil, true
	}
	cands := make([]model.Host, len(hits))
	for i, h := range hits {
		cands[i] = h.Host
	}
	return model.Host{}, cands, false
}
