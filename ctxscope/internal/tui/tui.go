// Package tui is the interactive browser: pick a snapshot, fuzzy-filter its chunks, regroup
// them by source or category, read a chunk's full text, and diff two snapshots.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/report"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
)

// Run starts the browser. If open is non-empty, it starts on that snapshot's chunks.
func Run(open string) error {
	m, err := newRoot(open)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "242"})
	accent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "39"}).Bold(true)
	warn   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	plus   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "203"})
	minus  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "78"})
)

// screen is one level of the navigation stack.
type screen interface {
	update(msg tea.Msg, r *root) (screen, tea.Cmd)
	view() string
	resize(w, h int)
	filterState() list.FilterState
}

type root struct {
	stack []screen
	w, h  int
	err   string
}

func newRoot(open string) (*root, error) {
	r := &root{}
	ss, err := newSnapshotScreen()
	if err != nil {
		return nil, err
	}
	r.stack = []screen{ss}
	if open != "" {
		s, err := store.Load(open)
		if err != nil {
			return nil, err
		}
		r.stack = append(r.stack, newChunkScreen(s, "chunk", nil))
	}
	return r, nil
}

func (r *root) Init() tea.Cmd { return nil }

func (r *root) top() screen { return r.stack[len(r.stack)-1] }

func (r *root) push(s screen) {
	s.resize(r.w, r.h)
	r.stack = append(r.stack, s)
}

func (r *root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.w, r.h = msg.Width, msg.Height
		for _, s := range r.stack {
			s.resize(r.w, r.h)
		}
		return r, nil
	case tea.KeyMsg:
		r.err = ""
		if msg.String() == "ctrl+c" {
			return r, tea.Quit
		}
		fs := r.top().filterState()
		// While typing a filter every key belongs to the list; with a filter applied, esc
		// clears it before it navigates back.
		if fs != list.Filtering && !(fs == list.FilterApplied && msg.String() == "esc") {
			switch msg.String() {
			case "q":
				return r, tea.Quit
			case "esc", "backspace":
				if len(r.stack) > 1 {
					r.stack = r.stack[:len(r.stack)-1]
					return r, nil
				}
				if msg.String() == "esc" {
					return r, tea.Quit
				}
			}
		}
	}
	// update may push a new screen, so write the result back to the screen's own slot.
	i := len(r.stack) - 1
	next, cmd := r.stack[i].update(msg, r)
	r.stack[i] = next
	return r, cmd
}

func (r *root) View() string {
	v := r.top().view()
	if r.err != "" {
		v += "\n" + warn.Render(r.err)
	}
	return v
}

// --- list plumbing ---------------------------------------------------------------------------

type item struct {
	title, desc, filter string
	payload             any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.filter }

func newList(title string, items []list.Item, extra ...key.Binding) list.Model {
	d := list.NewDefaultDelegate()
	l := list.New(items, d, 0, 0)
	l.Title = title
	l.SetStatusBarItemName("item", "items")
	l.AdditionalShortHelpKeys = func() []key.Binding { return extra }
	l.AdditionalFullHelpKeys = func() []key.Binding { return extra }
	return l
}

func bind(keys, help string) key.Binding {
	return key.NewBinding(key.WithKeys(strings.Split(keys, ",")...), key.WithHelp(strings.Split(keys, ",")[0], help))
}

// --- snapshots -------------------------------------------------------------------------------

type snapshotScreen struct {
	list   list.Model
	marked []string
}

func newSnapshotScreen() (*snapshotScreen, error) {
	names, err := store.List()
	if err != nil {
		return nil, err
	}
	s := &snapshotScreen{}
	s.list = newList("ctxscope · snapshots", nil, bind("enter", "open"), bind(" ", "mark for diff"), bind("d", "diff"), bind("t", "treemap"))
	var items []list.Item
	for i := len(names) - 1; i >= 0; i-- { // newest first
		snap, err := store.Load(names[i])
		if err != nil {
			continue
		}
		items = append(items, snapshotItem(snap, i+1))
	}
	s.list.SetItems(items)
	return s, nil
}

func snapshotItem(s *store.Snapshot, idx int) item {
	est := ""
	if !s.Calibrated {
		est = " (est.)"
	}
	label := ""
	if s.Label != "" {
		label = "  [" + s.Label + "]"
	}
	return item{
		title:   fmt.Sprintf("%3d  %7s%s  %s%s", idx, report.K(s.Total), est, s.Name, label),
		desc:    fmt.Sprintf("     %s · %s · %s", s.Mode, s.Model, s.Cwd),
		filter:  s.Name + " " + s.Label + " " + s.Mode + " " + s.Cwd,
		payload: s,
	}
}

func (s *snapshotScreen) filterState() list.FilterState { return s.list.FilterState() }
func (s *snapshotScreen) resize(w, h int)               { s.list.SetSize(w, h-1) }

func (s *snapshotScreen) update(msg tea.Msg, r *root) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && s.list.FilterState() != list.Filtering {
		sel, _ := s.list.SelectedItem().(item)
		snap, _ := sel.payload.(*store.Snapshot)
		switch k.String() {
		case "enter":
			if snap != nil {
				r.push(newChunkScreen(snap, "chunk", nil))
			}
			return s, nil
		case " ":
			if snap != nil {
				s.toggle(snap.Name)
			}
			return s, nil
		case "t":
			if snap != nil {
				r.push(newTreemapScreen(snap, "chunk", nil))
			}
			return s, nil
		case "d":
			a, b, err := s.diffPair(snap)
			if err != nil {
				r.err = err.Error()
				return s, nil
			}
			r.push(newDiffScreen(a, b))
			return s, nil
		}
	}
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return s, cmd
}

func (s *snapshotScreen) toggle(name string) {
	for i, m := range s.marked {
		if m == name {
			s.marked = append(s.marked[:i], s.marked[i+1:]...)
			s.refreshTitle()
			return
		}
	}
	s.marked = append(s.marked, name)
	if len(s.marked) > 2 {
		s.marked = s.marked[1:]
	}
	s.refreshTitle()
}

func (s *snapshotScreen) refreshTitle() {
	s.list.Title = "ctxscope · snapshots"
	if len(s.marked) > 0 {
		s.list.Title += " · marked: " + strings.Join(s.marked, ", ")
	}
}

// diffPair diffs the two marked snapshots (older → newer), or the selected one against the
// snapshot just before it.
func (s *snapshotScreen) diffPair(sel *store.Snapshot) (*store.Snapshot, *store.Snapshot, error) {
	if len(s.marked) == 2 {
		a, err := store.Load(min(s.marked[0], s.marked[1]))
		if err != nil {
			return nil, nil, err
		}
		b, err := store.Load(max(s.marked[0], s.marked[1]))
		return a, b, err
	}
	if sel == nil {
		return nil, nil, fmt.Errorf("nothing selected")
	}
	names, err := store.List()
	if err != nil {
		return nil, nil, err
	}
	for i, n := range names {
		if n == sel.Name {
			if i == 0 {
				return nil, nil, fmt.Errorf("%s is the oldest snapshot; mark two with space", n)
			}
			a, err := store.Load(names[i-1])
			return a, sel, err
		}
	}
	return nil, nil, fmt.Errorf("snapshot %s not found", sel.Name)
}

func (s *snapshotScreen) view() string { return s.list.View() }

// --- chunks ----------------------------------------------------------------------------------

var groupings = []string{"chunk", "source", "category"}

type chunkScreen struct {
	snap   *store.Snapshot
	by     string
	scope  func(analyze.Chunk) bool // drill-down filter; nil = all
	scopeN string
	drill  *drill
	list   list.Model
	w      int
}

func newChunkScreen(s *store.Snapshot, by string, scope *drill) *chunkScreen {
	c := &chunkScreen{snap: s, by: by}
	if scope != nil {
		c.scope, c.scopeN, c.drill = scope.match, scope.name, scope
	}
	c.list = newList("", nil, bind("enter", "open"), bind("g", "group by chunk/source/category"), bind("t", "treemap"))
	c.rebuild()
	return c
}

type drill struct {
	name  string
	match func(analyze.Chunk) bool
}

func (c *chunkScreen) chunks() []analyze.Chunk {
	if c.scope == nil {
		return c.snap.Chunks
	}
	var out []analyze.Chunk
	for _, ch := range c.snap.Chunks {
		if c.scope(ch) {
			out = append(out, ch)
		}
	}
	return out
}

func (c *chunkScreen) rebuild() {
	chunks := c.chunks()
	sum := 0
	for _, ch := range chunks {
		sum += ch.Tokens
	}
	title := fmt.Sprintf("%s · %s tokens · by %s", c.snap.Name, report.K(sum), c.by)
	if c.scopeN != "" {
		title = fmt.Sprintf("%s · %s · %s tokens · by %s", c.snap.Name, c.scopeN, report.K(sum), c.by)
	}
	c.list.Title = title
	var items []list.Item
	if c.by == "chunk" {
		sorted := append([]analyze.Chunk(nil), chunks...)
		sortChunks(sorted)
		max := 0
		if len(sorted) > 0 {
			max = sorted[0].Tokens
		}
		for _, ch := range sorted {
			note := ""
			if ch.Note != "" {
				note = " · " + warn.Render(ch.Note)
			}
			items = append(items, item{
				title:   fmt.Sprintf("%6s  %-10s %s", report.K(ch.Tokens), report.Bar(ch.Tokens, max, 10), ch.Name),
				desc:    fmt.Sprintf("        %s · %s%s", ch.Category, ch.Source, note),
				filter:  ch.Name + " " + ch.Category + " " + ch.Source + " " + ch.Note,
				payload: ch,
			})
		}
	} else {
		groups := report.GroupBy(chunks, c.by)
		max := 0
		for _, g := range groups {
			if g.Tokens > max {
				max = g.Tokens
			}
		}
		for _, g := range groups {
			items = append(items, item{
				title:   fmt.Sprintf("%6s  %-10s %s", report.K(g.Tokens), report.Bar(g.Tokens, max, 10), g.Key),
				desc:    fmt.Sprintf("        %d chunks · %.1f%%", g.Count, 100*float64(g.Tokens)/float64(max1(sum))),
				filter:  g.Key,
				payload: g,
			})
		}
	}
	c.list.SetItems(items)
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func sortChunks(cs []analyze.Chunk) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].Tokens > cs[j-1].Tokens; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}

func (c *chunkScreen) filterState() list.FilterState { return c.list.FilterState() }
func (c *chunkScreen) resize(w, h int) {
	c.w = w
	c.list.SetSize(w, max(1, h-3)) // two lines for the category strip
}

func (c *chunkScreen) update(msg tea.Msg, r *root) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && c.list.FilterState() != list.Filtering {
		switch k.String() {
		case "g":
			for i, g := range groupings {
				if g == c.by {
					c.by = groupings[(i+1)%len(groupings)]
					break
				}
			}
			c.list.ResetFilter()
			c.rebuild()
			return c, nil
		case "t":
			r.push(newTreemapScreen(c.snap, c.by, c.drill))
			return c, nil
		case "enter":
			sel, _ := c.list.SelectedItem().(item)
			switch p := sel.payload.(type) {
			case analyze.Chunk:
				r.push(newDetailScreen(p))
			case report.Group:
				by, key := c.by, p.Key
				r.push(newChunkScreen(c.snap, "chunk", &drill{
					name: key,
					match: func(ch analyze.Chunk) bool {
						if by == "source" {
							return ch.Source == key
						}
						return ch.Category == key
					},
				}))
			}
			return c, nil
		}
	}
	var cmd tea.Cmd
	c.list, cmd = c.list.Update(msg)
	return c, cmd
}

func (c *chunkScreen) view() string { return categoryStrip(c.chunks(), c.w) + "\n" + c.list.View() }

// --- chunk detail ----------------------------------------------------------------------------

type detailScreen struct {
	header string
	vp     viewport.Model
	body   string
}

func newDetailScreen(ch analyze.Chunk) *detailScreen {
	var b strings.Builder
	fmt.Fprintln(&b, accent.Render(ch.Name))
	fmt.Fprintf(&b, "%s · %s tokens (estimate %d) · %s\n", ch.Category, report.K(ch.Tokens), ch.Estimate, ch.Source)
	fmt.Fprintln(&b, dim.Render("id: "+ch.ID))
	if ch.Hint != "" {
		fmt.Fprintln(&b, "↳ "+ch.Hint)
	}
	if ch.Note != "" {
		fmt.Fprintln(&b, warn.Render("! "+ch.Note))
	}
	d := &detailScreen{header: b.String(), body: ch.Text}
	d.vp = viewport.New(0, 0)
	return d
}

func (d *detailScreen) filterState() list.FilterState { return list.Unfiltered }

func (d *detailScreen) resize(w, h int) {
	hh := lipgloss.Height(d.header) + 2
	d.vp.Width, d.vp.Height = w, max(1, h-hh)
	d.vp.SetContent(lipgloss.NewStyle().Width(w).Render(d.body))
}

func (d *detailScreen) update(msg tea.Msg, r *root) (screen, tea.Cmd) {
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return d, cmd
}

func (d *detailScreen) view() string {
	return d.header + dim.Render(strings.Repeat("─", max(1, d.vp.Width))) + "\n" + d.vp.View() +
		"\n" + dim.Render(fmt.Sprintf("%3.f%% · ↑/↓ scroll · esc back · q quit", d.vp.ScrollPercent()*100))
}

// --- diff ------------------------------------------------------------------------------------

type diffScreen struct {
	list list.Model
}

func newDiffScreen(a, b *store.Snapshot) *diffScreen {
	changes := report.Diff(a, b, 5)
	title := fmt.Sprintf("diff %s → %s · %s → %s (%s)", a.Name, b.Name, report.K(a.Total), report.K(b.Total), report.SignedK(b.Total-a.Total))
	var items []list.Item
	for _, c := range changes {
		style := plus
		if c.Delta() < 0 {
			style = minus
		}
		items = append(items, item{
			title:  fmt.Sprintf("%8s  %-8s %s", style.Render(report.SignedK(c.Delta())), c.Status(), c.Name),
			desc:   fmt.Sprintf("          %s · %s · %s → %s", c.Category, c.Source, report.K(c.Before), report.K(c.After)),
			filter: c.Name + " " + c.Category + " " + c.Source + " " + c.Status(),
		})
	}
	d := &diffScreen{list: newList(title, items)}
	return d
}

func (d *diffScreen) filterState() list.FilterState { return d.list.FilterState() }
func (d *diffScreen) resize(w, h int)               { d.list.SetSize(w, h-1) }

func (d *diffScreen) update(msg tea.Msg, r *root) (screen, tea.Cmd) {
	var cmd tea.Cmd
	d.list, cmd = d.list.Update(msg)
	return d, cmd
}

func (d *diffScreen) view() string { return d.list.View() }
