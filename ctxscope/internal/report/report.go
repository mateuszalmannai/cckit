// Package report groups and diffs snapshot chunks, and renders both as plain text tables.
package report

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
)

// Group is a set of chunks summed under one key.
type Group struct {
	Key    string
	Tokens int
	Count  int
	Hint   string // shared hint, if all chunks agree
}

// GroupBy sums chunks by "category", "source" or "chunk" (no grouping), largest first.
// Category grouping keeps the fixed category order instead.
func GroupBy(chunks []analyze.Chunk, by string) []Group {
	idx := map[string]int{}
	var out []Group
	for _, c := range chunks {
		key := groupKey(c, by)
		i, ok := idx[key]
		if !ok {
			idx[key] = len(out)
			out = append(out, Group{Key: key, Hint: c.Hint})
			i = len(out) - 1
		}
		out[i].Tokens += c.Tokens
		out[i].Count++
		if out[i].Hint != c.Hint {
			out[i].Hint = ""
		}
	}
	if by == "category" {
		order := map[string]int{}
		for i, c := range analyze.CategoryOrder {
			order[c] = i
		}
		sort.SliceStable(out, func(i, j int) bool { return order[out[i].Key] < order[out[j].Key] })
		return out
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tokens > out[j].Tokens })
	return out
}

func groupKey(c analyze.Chunk, by string) string {
	switch by {
	case "category":
		return c.Category
	case "source":
		return c.Source
	default:
		return c.Category + " · " + c.Name
	}
}

// K formats a token count like /context does: 950, 1.2k, 36.2k.
func K(n int) string {
	if n < 0 {
		return "-" + K(-n)
	}
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// SignedK is K with an explicit + for positive values.
func SignedK(n int) string {
	if n > 0 {
		return "+" + K(n)
	}
	return K(n)
}

// Bar draws a proportional bar of at most width cells.
func Bar(n, max, width int) string {
	if max <= 0 || n <= 0 {
		return ""
	}
	cells := int(math.Round(float64(n) / float64(max) * float64(width)))
	if cells == 0 {
		cells = 1
	}
	return strings.Repeat("█", cells)
}

// Header describes a snapshot in one line.
func Header(s *store.Snapshot) string {
	total := K(s.Total) + " tokens"
	if !s.Calibrated {
		total += " (estimated, no API usage recorded)"
	}
	label := ""
	if s.Label != "" {
		label = " [" + s.Label + "]"
	}
	return fmt.Sprintf("%s%s · %s · %s · %s · %s", s.Name, label, s.Mode, s.Model, s.Cwd, total)
}

// Write renders a report grouped by `by`, limited to top rows (0 = all).
func Write(w io.Writer, s *store.Snapshot, by string, top int, hints bool) {
	fmt.Fprintln(w, Header(s))
	fmt.Fprintln(w)
	groups := GroupBy(s.Chunks, by)
	max := 0
	for _, g := range groups {
		if g.Tokens > max {
			max = g.Tokens
		}
	}
	if top > 0 && len(groups) > top {
		groups = groups[:top]
	}
	keyW := 10
	for _, g := range groups {
		if l := len([]rune(g.Key)); l > keyW {
			keyW = min(l, 60)
		}
	}
	for _, g := range groups {
		pct := 100 * float64(g.Tokens) / float64(max1(s.Total))
		fmt.Fprintf(w, "%7s %5.1f%%  %-*s %4d  %s\n", K(g.Tokens), pct, keyW, trunc(g.Key, keyW), g.Count, Bar(g.Tokens, max, 24))
		if hints && g.Hint != "" {
			fmt.Fprintf(w, "%15s  ↳ %s\n", "", g.Hint)
		}
	}
	if notes := duplicates(s.Chunks); len(notes) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Duplicate skills:")
		for _, n := range notes {
			fmt.Fprintln(w, "  "+n)
		}
	}
}

// duplicates lists each group of same-named skills once.
func duplicates(chunks []analyze.Chunk) []string {
	var out []string
	done := map[string]bool{}
	for _, c := range chunks {
		if c.Note == "" || done[c.Name] {
			continue
		}
		names := []string{c.Name}
		tokens := c.Tokens
		for _, other := range strings.Split(strings.TrimPrefix(c.Note, "duplicate of "), ", ") {
			names = append(names, other)
			for _, o := range chunks {
				if o.Name == other {
					tokens += o.Tokens
				}
			}
		}
		for _, n := range names {
			done[n] = true
		}
		out = append(out, fmt.Sprintf("%6s  %s", K(tokens), strings.Join(names, " ↔ ")))
	}
	return out
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func trunc(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// Change is one chunk's difference between two snapshots.
type Change struct {
	ID       string
	Category string
	Name     string
	Source   string
	Before   int
	After    int
}

// Delta is After - Before.
func (c Change) Delta() int { return c.After - c.Before }

// Status is "added", "removed" or "changed".
func (c Change) Status() string {
	switch {
	case c.Before == 0:
		return "added"
	case c.After == 0:
		return "removed"
	}
	return "changed"
}

// Diff matches chunks by ID. A chunk whose text is unchanged counts as unchanged even if
// calibration gave it a slightly different token share; only real edits, additions and
// removals show up. Changes within threshold tokens are dropped.
func Diff(a, b *store.Snapshot, threshold int) []Change {
	all := allChanges(a, b)
	kept := all[:0]
	for _, c := range all {
		d := c.Delta()
		if c.Status() != "changed" || d > threshold || d < -threshold {
			kept = append(kept, c)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return abs(kept[i].Delta()) > abs(kept[j].Delta()) })
	return kept
}

// allChanges pairs chunks by ID. A pair whose source differs (a local skill replaced by the
// built-in of the same name) is reported as the old chunk removed and the new one added, so
// each source's total moves by what it really lost or gained.
func allChanges(a, b *store.Snapshot) []Change {
	before := map[string]analyze.Chunk{}
	for _, c := range a.Chunks {
		before[c.ID] = c
	}
	var out []Change
	paired := map[string]bool{}
	for _, c := range b.Chunks {
		ch := Change{ID: c.ID, Category: c.Category, Name: c.Name, Source: c.Source, After: c.Tokens}
		if p, ok := before[c.ID]; ok && p.Source == c.Source {
			paired[c.ID] = true
			ch.Before = p.Tokens
			if p.Text == c.Text {
				ch.After = p.Tokens
			}
		}
		out = append(out, ch)
	}
	for _, c := range a.Chunks {
		if !paired[c.ID] {
			out = append(out, Change{ID: c.ID, Category: c.Category, Name: c.Name, Source: c.Source, Before: c.Tokens})
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// WriteDiff renders a diff: totals, per-category and per-source deltas, then chunk changes.
func WriteDiff(w io.Writer, a, b *store.Snapshot, threshold, top int) {
	fmt.Fprintln(w, "A: "+Header(a))
	fmt.Fprintln(w, "B: "+Header(b))
	fmt.Fprintf(w, "\nTotal: %s → %s (%s)\n", K(a.Total), K(b.Total), SignedK(b.Total-a.Total))

	all := allChanges(a, b)
	attributed := 0
	for _, c := range all {
		attributed += c.Delta()
	}
	for _, by := range []string{"category", "source"} {
		fmt.Fprintf(w, "\nBy %s:\n", by)
		type agg struct{ before, after int }
		sums := map[string]*agg{}
		var keys []string
		for _, c := range all {
			k := c.Source
			if by == "category" {
				k = c.Category
			}
			if sums[k] == nil {
				sums[k] = &agg{}
				keys = append(keys, k)
			}
			sums[k].before += c.Before
			sums[k].after += c.After
		}
		sort.SliceStable(keys, func(i, j int) bool {
			return abs(sums[keys[i]].after-sums[keys[i]].before) > abs(sums[keys[j]].after-sums[keys[j]].before)
		})
		for _, k := range keys {
			g := sums[k]
			if d := g.after - g.before; d != 0 {
				fmt.Fprintf(w, "  %8s  %7s → %-7s  %s\n", SignedK(d), K(g.before), K(g.after), k)
			}
		}
	}
	if r := (b.Total - a.Total) - attributed; r != 0 {
		fmt.Fprintf(w, "\n%s tokens of the total change are calibration residual (unchanged text re-weighted).\n", SignedK(r))
	}

	changes := Diff(a, b, threshold)
	if top > 0 && len(changes) > top {
		changes = changes[:top]
	}
	fmt.Fprintf(w, "\nChunks (|Δ| > %d):\n", threshold)
	for _, c := range changes {
		fmt.Fprintf(w, "  %8s  %-8s %-16s %s  (%s)\n", SignedK(c.Delta()), c.Status(), c.Category, c.Name, c.Source)
	}
}
