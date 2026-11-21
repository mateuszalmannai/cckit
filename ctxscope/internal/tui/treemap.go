package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/report"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
)

// --- colours ---------------------------------------------------------------------------------

// catColor gives every category a fixed background, so the strip, the legend and the treemap
// read the same. White text stays legible on all of them in light and dark terminals.
var catColor = map[string]lipgloss.Color{
	analyze.CatSystem:   "61",
	analyze.CatTools:    "25",
	analyze.CatDeferred: "31",
	analyze.CatMemory:   "29",
	analyze.CatHooks:    "130",
	analyze.CatSkills:   "91",
	analyze.CatAgents:   "166",
	analyze.CatMCP:      "37",
	analyze.CatSession:  "101",
	analyze.CatPrompt:   "240",
	analyze.CatOther:    "124",
}

const lumpColor = lipgloss.Color("238")

func colorOf(cat string) lipgloss.Color {
	if c, ok := catColor[cat]; ok {
		return c
	}
	return lumpColor
}

func tileStyle(cat string) lipgloss.Style {
	return lipgloss.NewStyle().Background(colorOf(cat)).Foreground(lipgloss.Color("255"))
}

// categoryStrip renders one line of width w split by category share, plus a legend line.
func categoryStrip(chunks []analyze.Chunk, w int) string {
	if w < 1 {
		return "\n"
	}
	groups := report.GroupBy(chunks, "category")
	sizes := make([]int, len(groups))
	for i, g := range groups {
		sizes[i] = g.Tokens
	}
	cells := apportion(sizes, w)
	var bar, legend strings.Builder
	used := 0
	for i, g := range groups {
		if cells[i] > 0 {
			bar.WriteString(lipgloss.NewStyle().Background(colorOf(g.Key)).Render(strings.Repeat(" ", cells[i])))
		}
		if g.Tokens == 0 {
			continue
		}
		entry := fmt.Sprintf("%s %s ", g.Key, report.K(g.Tokens))
		if used+2+len(entry) > w {
			continue
		}
		legend.WriteString(lipgloss.NewStyle().Foreground(colorOf(g.Key)).Render("■") + " " + dim.Render(entry))
		used += 2 + len(entry)
	}
	return bar.String() + "\n" + legend.String()
}

// apportion splits width cells over sizes by largest remainder, so the cells sum to width.
func apportion(sizes []int, width int) []int {
	total := 0
	for _, s := range sizes {
		total += s
	}
	out := make([]int, len(sizes))
	if total <= 0 {
		return out
	}
	type rem struct {
		i int
		r float64
	}
	var rems []rem
	left := width
	for i, s := range sizes {
		exact := float64(s) / float64(total) * float64(width)
		out[i] = int(exact)
		left -= out[i]
		rems = append(rems, rem{i, exact - float64(out[i])})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
	for k := 0; k < left && k < len(rems); k++ {
		out[rems[k].i]++
	}
	return out
}

// --- squarified layout -----------------------------------------------------------------------

// tile is a rectangle in terminal cells: columns [x0,x1), rows [y0,y1).
type tile struct{ x0, y0, x1, y1 int }

func (t tile) area() int { return (t.x1 - t.x0) * (t.y1 - t.y0) }

type frect struct{ x, y, w, h float64 }

// cellAspect is how much taller a terminal cell is than wide; the layout runs in square units
// and is squashed back, so tiles look square on screen rather than in cell counts.
const cellAspect = 2.0

// squarify lays sizes (largest first) out over a w×h cell area with the squarified treemap
// algorithm (Bruls, Huizing, van Wijk). Edges are rounded from shared float coordinates, so
// neighbouring tiles meet without gaps or overlaps and the tiles cover the whole area.
func squarify(sizes []int, w, h int) []tile {
	out := make([]tile, len(sizes))
	total := 0
	for _, s := range sizes {
		total += s
	}
	if total <= 0 || w <= 0 || h <= 0 {
		return out
	}
	W, H := float64(w), float64(h)*cellAspect
	areas := make([]float64, len(sizes))
	for i, s := range sizes {
		areas[i] = float64(s) / float64(total) * W * H
	}
	rects := layout(areas, frect{0, 0, W, H})
	for i, r := range rects {
		out[i] = tile{
			x0: int(math.Round(r.x)), x1: int(math.Round(r.x + r.w)),
			y0: int(math.Round(r.y / cellAspect)), y1: int(math.Round((r.y + r.h) / cellAspect)),
		}
	}
	// The last row's far edge must land exactly on the border despite float drift.
	for i := range out {
		if out[i].x1 > w-1 && out[i].x1 != w && math.Abs(float64(out[i].x1-w)) <= 1 {
			out[i].x1 = w
		}
		if out[i].y1 > h-1 && out[i].y1 != h && math.Abs(float64(out[i].y1-h)) <= 1 {
			out[i].y1 = h
		}
	}
	return out
}

func layout(areas []float64, r frect) []frect {
	out := make([]frect, 0, len(areas))
	for i := 0; i < len(areas); {
		short := math.Min(r.w, r.h)
		j := i + 1
		for j < len(areas) && worst(areas[i:j+1], short) <= worst(areas[i:j], short) {
			j++
		}
		row := areas[i:j]
		s := 0.0
		for _, a := range row {
			s += a
		}
		if j == len(areas) {
			// Last row: take the remaining space exactly.
			s = r.w * r.h
			if rs := sumF(row); rs > 0 {
				for k := range row {
					row[k] = row[k] / rs * s
				}
			}
		}
		if r.w >= r.h { // a column on the left
			cw := 0.0
			if r.h > 0 {
				cw = s / r.h
			}
			y := r.y
			for _, a := range row {
				hh := 0.0
				if cw > 0 {
					hh = a / cw
				}
				out = append(out, frect{r.x, y, cw, hh})
				y += hh
			}
			r.x += cw
			r.w -= cw
		} else { // a row along the top
			rh := 0.0
			if r.w > 0 {
				rh = s / r.w
			}
			x := r.x
			for _, a := range row {
				ww := 0.0
				if rh > 0 {
					ww = a / rh
				}
				out = append(out, frect{x, r.y, ww, rh})
				x += ww
			}
			r.y += rh
			r.h -= rh
		}
		i = j
	}
	return out
}

func sumF(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s
}

// worst is the largest aspect ratio in a row laid along a side of length short.
func worst(row []float64, short float64) float64 {
	s, mx, mn := 0.0, 0.0, math.Inf(1)
	for _, a := range row {
		s += a
		mx = math.Max(mx, a)
		mn = math.Min(mn, a)
	}
	if s == 0 || mn == 0 || short == 0 {
		return math.Inf(1)
	}
	return math.Max(short*short*mx/(s*s), s*s/(short*short*mn))
}

// --- treemap screen --------------------------------------------------------------------------

type tmItem struct {
	key           string
	tokens, count int
	cat           string // dominant category (tile colour)
	hint          string
	chunk         *analyze.Chunk // set when grouping by chunk
	members       []string       // keys folded into a "+N more" tile
}

// minTileCells is the smallest tile worth drawing; anything smaller is folded into "+N more".
const minTileCells = 40

type treemapScreen struct {
	snap  *store.Snapshot
	scope *drill
	by    string
	items []tmItem
	tiles []tile
	sel   int
	w, h  int
}

func newTreemapScreen(s *store.Snapshot, by string, scope *drill) *treemapScreen {
	if by == "" {
		by = "chunk"
	}
	return &treemapScreen{snap: s, by: by, scope: scope}
}

func (t *treemapScreen) chunks() []analyze.Chunk {
	if t.scope == nil {
		return t.snap.Chunks
	}
	var out []analyze.Chunk
	for _, ch := range t.snap.Chunks {
		if t.scope.match(ch) {
			out = append(out, ch)
		}
	}
	return out
}

func (t *treemapScreen) mapSize() (int, int) { return t.w, t.h - 4 }

func (t *treemapScreen) rebuild() {
	chunks := t.chunks()
	var items []tmItem
	if t.by == "chunk" {
		for i := range chunks {
			ch := chunks[i]
			items = append(items, tmItem{key: ch.Name, tokens: ch.Tokens, count: 1, cat: ch.Category, hint: ch.Hint, chunk: &ch})
		}
	} else {
		byCat := map[string]map[string]int{}
		for _, ch := range chunks {
			k := groupKeyOf(ch, t.by)
			if byCat[k] == nil {
				byCat[k] = map[string]int{}
			}
			byCat[k][ch.Category] += ch.Tokens
		}
		for _, g := range report.GroupBy(chunks, t.by) {
			items = append(items, tmItem{key: g.Key, tokens: g.Tokens, count: g.Count, cat: dominant(byCat[g.Key]), hint: g.Hint})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].tokens > items[j].tokens })
	var kept []tmItem
	for len(items) > 0 && items[len(items)-1].tokens <= 0 {
		items = items[:len(items)-1]
	}

	// Tiles too small to label are folded: first into one tile per category ("+72 Skills"),
	// so the tail still shows what it is made of, then any fold that is still too small into
	// one grey "+N more".
	mw, mh := t.mapSize()
	total := 0
	for _, it := range items {
		total += it.tokens
	}
	cells := float64(max(0, mw) * max(0, mh))
	small := func(n int) bool { return total > 0 && float64(n)/float64(total)*cells < minTileCells }
	var tail []tmItem
	for _, it := range items {
		if small(it.tokens) {
			tail = append(tail, it)
		} else {
			kept = append(kept, it)
		}
	}
	if len(tail) == 1 {
		kept = append(kept, tail...)
	} else if len(tail) > 1 {
		byCat := map[string]*tmItem{}
		var order []string
		for _, it := range tail {
			l := byCat[it.cat]
			if l == nil {
				l = &tmItem{cat: it.cat}
				byCat[it.cat] = l
				order = append(order, it.cat)
			}
			l.tokens += it.tokens
			l.count += it.count
			l.members = append(l.members, it.key)
		}
		var rest *tmItem
		for _, c := range order {
			l := byCat[c]
			if len(l.members) == 1 {
				// A fold of one is just the item; keep it, or send it on to "+N more".
				for _, it := range tail {
					if it.key == l.members[0] && it.cat == c {
						*l = it
						break
					}
				}
			} else {
				l.key = fmt.Sprintf("+%d %s", len(l.members), c)
			}
			if !small(l.tokens) {
				kept = append(kept, *l)
				continue
			}
			if rest == nil {
				rest = &tmItem{}
			}
			rest.tokens += l.tokens
			rest.count += l.count
			if l.members != nil {
				rest.members = append(rest.members, l.members...)
			} else {
				rest.members = append(rest.members, l.key)
			}
		}
		if rest != nil {
			rest.key = fmt.Sprintf("+%d more", len(rest.members))
			kept = append(kept, *rest)
		}
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].tokens > kept[j].tokens })
	}
	t.items = kept
	sizes := make([]int, len(kept))
	for i, it := range kept {
		sizes[i] = it.tokens
	}
	t.tiles = squarify(sizes, mw, mh)
	if t.sel >= len(t.items) {
		t.sel = 0
	}
}

func groupKeyOf(ch analyze.Chunk, by string) string {
	if by == "category" {
		return ch.Category
	}
	return ch.Source
}

func dominant(m map[string]int) string {
	best, n := "", -1
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

func (t *treemapScreen) filterState() list.FilterState { return list.Unfiltered }

func (t *treemapScreen) resize(w, h int) {
	t.w, t.h = w, h
	t.rebuild()
}

func (t *treemapScreen) update(msg tea.Msg, r *root) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok || len(t.items) == 0 {
		return t, nil
	}
	switch k.String() {
	case "left", "h":
		t.move(-1, 0)
	case "right", "l":
		t.move(1, 0)
	case "up", "k":
		t.move(0, -1)
	case "down", "j":
		t.move(0, 1)
	case "tab":
		t.sel = (t.sel + 1) % len(t.items)
	case "shift+tab":
		t.sel = (t.sel + len(t.items) - 1) % len(t.items)
	case "g":
		for i, g := range groupings {
			if g == t.by {
				t.by = groupings[(i+1)%len(groupings)]
				break
			}
		}
		t.sel = 0
		t.rebuild()
	case "enter":
		it := t.items[t.sel]
		if it.chunk != nil {
			r.push(newDetailScreen(*it.chunk))
			return t, nil
		}
		r.push(newChunkScreen(t.snap, "chunk", t.drillInto(it)))
	}
	return t, nil
}

func (t *treemapScreen) drillInto(it tmItem) *drill {
	by, outer := t.by, t.scope
	keys := map[string]bool{it.key: true}
	if it.members != nil {
		keys = map[string]bool{}
		for _, m := range it.members {
			keys[m] = true
		}
	}
	name := it.key
	if outer != nil {
		name = outer.name + " · " + name
	}
	return &drill{name: name, match: func(ch analyze.Chunk) bool {
		if outer != nil && !outer.match(ch) {
			return false
		}
		if by == "chunk" {
			return keys[ch.Name]
		}
		return keys[groupKeyOf(ch, by)]
	}}
}

// move selects the nearest tile in direction (dx, dy): it must lie beyond the current tile's
// edge, and tiles that overlap the current one on the other axis win.
func (t *treemapScreen) move(dx, dy int) {
	cur := t.tiles[t.sel]
	best, bestScore := -1, math.Inf(1)
	for i, o := range t.tiles {
		if i == t.sel || o.area() == 0 {
			continue
		}
		var gap, perp, off float64
		switch {
		case dx > 0:
			gap, perp = float64(o.x0-cur.x1), rangeGap(o.y0, o.y1, cur.y0, cur.y1)*cellAspect
			off = math.Abs(mid(o.y0, o.y1)-mid(cur.y0, cur.y1)) * cellAspect
		case dx < 0:
			gap, perp = float64(cur.x0-o.x1), rangeGap(o.y0, o.y1, cur.y0, cur.y1)*cellAspect
			off = math.Abs(mid(o.y0, o.y1)-mid(cur.y0, cur.y1)) * cellAspect
		case dy > 0:
			gap, perp = float64(o.y0-cur.y1)*cellAspect, rangeGap(o.x0, o.x1, cur.x0, cur.x1)
			off = math.Abs(mid(o.x0, o.x1) - mid(cur.x0, cur.x1))
		default:
			gap, perp = float64(cur.y0-o.y1)*cellAspect, rangeGap(o.x0, o.x1, cur.x0, cur.x1)
			off = math.Abs(mid(o.x0, o.x1) - mid(cur.x0, cur.x1))
		}
		if gap < 0 {
			continue
		}
		score := gap + 4*perp + 0.01*off
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	if best >= 0 {
		t.sel = best
	}
}

func rangeGap(a0, a1, b0, b1 int) float64 {
	if a1 <= b0 {
		return float64(b0 - a1)
	}
	if b1 <= a0 {
		return float64(a0 - b1)
	}
	return 0
}

func mid(a, b int) float64 { return float64(a+b) / 2 }

func (t *treemapScreen) view() string {
	chunks := t.chunks()
	sum := 0
	for _, ch := range chunks {
		sum += ch.Tokens
	}
	title := fmt.Sprintf("%s · %s tokens · map by %s", t.snap.Name, report.K(sum), t.by)
	if t.scope != nil {
		title = fmt.Sprintf("%s · %s · %s tokens · map by %s", t.snap.Name, t.scope.name, report.K(sum), t.by)
	}
	var b strings.Builder
	b.WriteString(accent.Render(title) + "\n")
	mw, mh := t.mapSize()
	if mw < 10 || mh < 2 || len(t.items) == 0 {
		b.WriteString(dim.Render("window too small or nothing to show") + "\n")
		return b.String()
	}
	b.WriteString(t.renderMap(mw, mh))
	it := t.items[t.sel]
	info := fmt.Sprintf("▶ %s · %s · %.1f%% · %d chunks", it.key, report.K(it.tokens), 100*float64(it.tokens)/float64(max1(sum)), it.count)
	if it.cat != "" && t.by != "category" {
		info += " · " + it.cat
	}
	b.WriteString("\n" + trunc(info, mw) + "\n")
	hint := it.hint
	if it.members != nil {
		hint = "folded: " + strings.Join(it.members, ", ")
	}
	b.WriteString(dim.Render(trunc("↳ "+hint, mw)) + "\n")
	b.WriteString(dim.Render(trunc("←↑↓→/hjkl move · tab next · enter open · g group by chunk/source/category · esc back · q quit", mw)))
	return b.String()
}

// renderMap draws every tile with a one-cell gutter on its right and bottom edge, so
// neighbours of the same colour stay apart, and labels it with its name and size.
func (t *treemapScreen) renderMap(w, h int) string {
	owner := make([][]int, h)
	text := make([][]rune, h)
	for y := range owner {
		owner[y] = make([]int, w)
		text[y] = []rune(strings.Repeat(" ", w))
		for x := range owner[y] {
			owner[y][x] = -1
		}
	}
	sum := 0
	for _, it := range t.items {
		sum += it.tokens
	}
	for i, tl := range t.tiles {
		x1, y1 := min(tl.x1, w), min(tl.y1, h)
		iw, ih := x1-tl.x0-1, y1-tl.y0-1
		if iw < 1 || ih < 1 {
			// Too thin for a gutter: fill it whole so the area is still visible.
			iw, ih = x1-tl.x0, y1-tl.y0
		}
		for y := tl.y0; y < tl.y0+ih; y++ {
			for x := tl.x0; x < tl.x0+iw; x++ {
				owner[y][x] = i
			}
		}
		it := t.items[i]
		lines := []string{it.key, fmt.Sprintf("%s %.0f%%", report.K(it.tokens), 100*float64(it.tokens)/float64(max1(sum)))}
		if iw >= 6 {
			for l, s := range lines {
				if l >= ih {
					break
				}
				rs := []rune(" " + trunc(s, iw-1))
				copy(text[tl.y0+l][tl.x0:tl.x0+iw], rs)
			}
		}
	}
	gutter := lipgloss.NewStyle()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; {
			o := owner[y][x]
			end := x
			for end < w && owner[y][end] == o {
				end++
			}
			seg := string(text[y][x:end])
			switch {
			case o < 0:
				b.WriteString(gutter.Render(seg))
			case o == t.sel:
				b.WriteString(lipgloss.NewStyle().Background(lipgloss.Color("255")).Foreground(colorOf(t.items[o].cat)).Bold(true).Render(seg))
			default:
				b.WriteString(tileStyle(t.items[o].cat).Render(seg))
			}
			x = end
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func trunc(s string, w int) string {
	r := []rune(s)
	if w <= 0 {
		return ""
	}
	if len(r) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}
