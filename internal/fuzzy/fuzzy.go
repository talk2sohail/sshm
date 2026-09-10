// Package fuzzy implements the subsequence matcher used by the search bar.
//
// The scoring model is a simplified version of fzf's: a candidate matches when
// the pattern appears as a subsequence, and the score rewards matches that
// start at word boundaries and run consecutively. Matched byte positions are
// returned so the UI can highlight them.
//
// Performance matters here because the whole host list is re-ranked on every
// keystroke. Two things keep it cheap. Aliases and hostnames are almost always
// ASCII, so there is a byte-wise fast path that avoids decoding runes at all;
// and scoring is separated from position collection, so the four fields that
// lose the per-host contest never allocate.
package fuzzy

import (
	"strings"
	"unicode"
)

// Scoring weights. These are tuned so that an exact prefix match on a short
// alias always beats a scattered match inside a long description.
const (
	scoreMatch       = 16
	bonusBoundary    = 30 // match right after a separator, or at index 0
	bonusCamel       = 22 // lower->Upper transition
	bonusConsecutive = 20
	bonusFirstChar   = 24 // extra weight on matching the very first character
	penaltyGapStart  = -6
	penaltyGapExtend = -2
	maxGapPenalty    = -40
)

// Result is a successful match.
type Result struct {
	Score     int
	Positions []int // byte offsets into the candidate, ascending
}

// Match reports whether pattern occurs as a subsequence of text and, if so,
// scores it and records where the matched characters are.
//
// Matching is smart-case: an all-lowercase pattern matches case-insensitively,
// while any uppercase rune in the pattern makes the whole match case-sensitive.
// An empty pattern matches everything with a score of 0.
func Match(pattern, text string) (Result, bool) {
	if pattern == "" {
		return Result{}, true
	}
	if text == "" {
		return Result{}, false
	}
	positions := make([]int, 0, len(pattern))
	score, positions, ok := match(pattern, text, positions)
	if !ok {
		return Result{}, false
	}
	return Result{Score: score, Positions: positions}, true
}

// Score is Match without position collection. It allocates nothing for ASCII
// input, which is what makes re-ranking on every keystroke cheap.
func Score(pattern, text string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	if text == "" {
		return 0, false
	}
	score, _, ok := match(pattern, text, nil)
	return score, ok
}

// match is the shared implementation. When positions is non-nil the matched
// byte offsets are appended to it; pass nil to skip that work entirely.
func match(pattern, text string, positions []int) (int, []int, bool) {
	caseSensitive := strings.IndexFunc(pattern, unicode.IsUpper) >= 0
	if isASCII(pattern) && isASCII(text) {
		return matchASCII(pattern, text, caseSensitive, positions)
	}
	return matchUnicode(pattern, text, caseSensitive, positions)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}

func eqASCII(a, b byte, caseSensitive bool) bool {
	if a == b {
		return true
	}
	if caseSensitive {
		return false
	}
	return lowerASCII(a) == lowerASCII(b)
}

// matchASCII is the allocation-free path: byte indices are byte offsets, so no
// rune decoding or offset table is needed.
func matchASCII(pattern, text string, caseSensitive bool, positions []int) (int, []int, bool) {
	// Forward pass: does the pattern occur at all, and where does it end?
	end, pi := -1, 0
	for ti := 0; ti < len(text) && pi < len(pattern); ti++ {
		if eqASCII(pattern[pi], text[ti], caseSensitive) {
			pi++
			if pi == len(pattern) {
				end = ti
			}
		}
	}
	if pi < len(pattern) {
		return 0, positions, false
	}

	// Backward pass: slide the start as far right as possible so the matched
	// run is as compact as it can be.
	start := end
	for ti, pj := end, len(pattern)-1; ti >= 0 && pj >= 0; ti-- {
		if eqASCII(pattern[pj], text[ti], caseSensitive) {
			pj--
			start = ti
		}
	}

	score, gapPenalty := 0, 0
	pi = 0
	inGap, prevMatched := false, false
	for ti := start; ti <= end && pi < len(pattern); ti++ {
		if eqASCII(pattern[pi], text[ti], caseSensitive) {
			s := scoreMatch + boundaryBonusASCII(text, ti)
			if prevMatched {
				s += bonusConsecutive
			}
			if ti == 0 {
				s += bonusFirstChar
			}
			if pi == 0 {
				s += max(0, 12-ti)
			}
			score += s
			if positions != nil {
				positions = append(positions, ti)
			}
			pi++
			prevMatched, inGap = true, false
		} else {
			if !inGap {
				gapPenalty += penaltyGapStart
				inGap = true
			} else {
				gapPenalty += penaltyGapExtend
			}
			prevMatched = false
		}
	}

	score += max(gapPenalty, maxGapPenalty)
	score -= len(text) / 8
	return score, positions, true
}

// matchUnicode is the correctness path for non-ASCII text. It decodes runes and
// keeps a byte-offset table so Positions stay valid byte indices.
func matchUnicode(pattern, text string, caseSensitive bool, positions []int) (int, []int, bool) {
	pr := []rune(pattern)
	tr := []rune(text)
	offsets := make([]int, len(tr)+1)
	off := 0
	for i, r := range tr {
		offsets[i] = off
		off += len(string(r))
	}
	offsets[len(tr)] = off

	end, pi := -1, 0
	for ti := 0; ti < len(tr) && pi < len(pr); ti++ {
		if eqRune(pr[pi], tr[ti], caseSensitive) {
			pi++
			if pi == len(pr) {
				end = ti
			}
		}
	}
	if pi < len(pr) {
		return 0, positions, false
	}

	start := end
	for ti, pj := end, len(pr)-1; ti >= 0 && pj >= 0; ti-- {
		if eqRune(pr[pj], tr[ti], caseSensitive) {
			pj--
			start = ti
		}
	}

	score, gapPenalty := 0, 0
	pi = 0
	inGap, prevMatched := false, false
	for ti := start; ti <= end && pi < len(pr); ti++ {
		if eqRune(pr[pi], tr[ti], caseSensitive) {
			s := scoreMatch + boundaryBonusRune(tr, ti)
			if prevMatched {
				s += bonusConsecutive
			}
			if ti == 0 {
				s += bonusFirstChar
			}
			if pi == 0 {
				s += max(0, 12-ti)
			}
			score += s
			if positions != nil {
				positions = append(positions, offsets[ti])
			}
			pi++
			prevMatched, inGap = true, false
		} else {
			if !inGap {
				gapPenalty += penaltyGapStart
				inGap = true
			} else {
				gapPenalty += penaltyGapExtend
			}
			prevMatched = false
		}
	}

	score += max(gapPenalty, maxGapPenalty)
	score -= len(tr) / 8
	return score, positions, true
}

func eqRune(a, b rune, caseSensitive bool) bool {
	if a == b {
		return true
	}
	if caseSensitive {
		return false
	}
	return unicode.ToLower(a) == unicode.ToLower(b)
}

func boundaryBonusASCII(text string, i int) int {
	if i == 0 {
		return bonusBoundary
	}
	prev, cur := text[i-1], text[i]
	if isSepByte(prev) {
		return bonusBoundary
	}
	if prev >= 'a' && prev <= 'z' && cur >= 'A' && cur <= 'Z' {
		return bonusCamel
	}
	if isAlphaByte(prev) && cur >= '0' && cur <= '9' {
		return bonusCamel / 2
	}
	return 0
}

func boundaryBonusRune(tr []rune, i int) int {
	if i == 0 {
		return bonusBoundary
	}
	prev, cur := tr[i-1], tr[i]
	if isSep(prev) {
		return bonusBoundary
	}
	if unicode.IsLower(prev) && unicode.IsUpper(cur) {
		return bonusCamel
	}
	if unicode.IsLetter(prev) && unicode.IsDigit(cur) {
		return bonusCamel / 2
	}
	return 0
}

func isSep(r rune) bool {
	switch r {
	case '-', '_', '.', '/', ' ', ':', '@', ',', '+':
		return true
	}
	return false
}

func isSepByte(b byte) bool {
	switch b {
	case '-', '_', '.', '/', ' ', ':', '@', ',', '+':
		return true
	}
	return false
}

func isAlphaByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Field is one searchable string on a record, with a weight expressing how
// much a match there should count. Weights are percentages.
type Field struct {
	Text   string
	Weight int
}

// Best runs the matcher against every field and returns the highest weighted
// score and the index of the field that produced it. ok is false when the
// pattern matches none of the fields.
//
// Best deliberately does not compute highlight positions. Ranking touches every
// host on every keystroke, but only the couple of dozen rows actually on screen
// need highlighting, so the UI calls Positions for those instead. That keeps
// the hot path allocation-free.
func Best(pattern string, fields []Field) (score int, fieldIdx int, ok bool) {
	fieldIdx = -1
	for i, f := range fields {
		if f.Text == "" {
			continue
		}
		raw, matched := Score(pattern, f.Text)
		if !matched {
			continue
		}
		s := raw * f.Weight / 100
		if !ok || s > score {
			score, fieldIdx, ok = s, i, true
		}
	}
	if !ok {
		return 0, -1, false
	}
	return score, fieldIdx, true
}

// Positions returns the byte offsets to highlight when pattern is matched
// against text, or nil if it does not match. Call it per visible row.
func Positions(pattern, text string) []int {
	r, ok := Match(pattern, text)
	if !ok {
		return nil
	}
	return r.Positions
}
