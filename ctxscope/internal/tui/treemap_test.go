package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
)

// Every cell of the area belongs to exactly one tile, and bigger sizes get bigger tiles.
func TestSquarifyCoversArea(t *testing.T) {
	cases := []struct {
		sizes []int
		w, h  int
	}{
		{[]int{13400, 2900, 2000, 1900, 1700, 1400, 1200, 1000, 700, 500, 300}, 120, 30},
		{[]int{5, 3, 2}, 40, 10},
		{[]int{1}, 17, 3},
		{[]int{100, 100, 100, 100}, 80, 20},
		{[]int{9000, 10, 10, 10, 10}, 60, 15},
	}
	for _, c := range cases {
		tiles := squarify(c.sizes, c.w, c.h)
		grid := make([]int, c.w*c.h)
		for i, tl := range tiles {
			if tl.x0 < 0 || tl.y0 < 0 || tl.x1 > c.w || tl.y1 > c.h || tl.x0 > tl.x1 || tl.y0 > tl.y1 {
				t.Fatalf("%v: tile %d out of bounds: %+v", c.sizes, i, tl)
			}
			for y := tl.y0; y < tl.y1; y++ {
				for x := tl.x0; x < tl.x1; x++ {
					grid[y*c.w+x]++
				}
			}
		}
		for i, n := range grid {
			if n != 1 {
				t.Fatalf("%v %dx%d: cell (%d,%d) covered %d times", c.sizes, c.w, c.h, i%c.w, i/c.w, n)
			}
		}
		for i := 1; i < len(tiles); i++ {
			if c.sizes[i-1] > 2*c.sizes[i] && tiles[i-1].area() < tiles[i].area() {
				t.Fatalf("%v: tile %d (%d cells) smaller than tile %d (%d cells)", c.sizes, i-1, tiles[i-1].area(), i, tiles[i].area())
			}
		}
	}
}

func TestApportion(t *testing.T) {
	got := apportion([]int{50, 30, 20, 0}, 7)
	sum := 0
	for _, n := range got {
		sum += n
	}
	if sum != 7 || got[3] != 0 || got[0] < got[1] {
		t.Fatalf("apportion = %v", got)
	}
}

func TestTreemapScreen(t *testing.T) {
	t.Setenv("CTXSCOPE_HOME", t.TempDir())
	save(t, "map", time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		chunk("Artifact", analyze.CatTools, "built-in", "artifact schema", 13400),
		chunk("Bash", analyze.CatTools, "built-in", "bash schema", 1400),
		chunk("CLAUDE.md", analyze.CatMemory, "~/markdown/CLAUDE.md", "notes", 2900),
		chunk("hook", analyze.CatHooks, "plugin superpowers", "You have superpowers", 1200),
		chunk("tiny", analyze.CatSkills, "user skill", "t", 3),
		chunk("tinier", analyze.CatAgents, "user agent", "a", 2))

	r, err := newRoot("")
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// The chunk screen carries the category strip and legend.
	send(t, r, "enter")
	if v := r.View(); !strings.Contains(v, "Tools 14.8k") || !strings.Contains(v, "Memory files 2.9k") {
		t.Fatalf("chunk screen strip:\n%s", v)
	}
	send(t, r, "esc")

	// t opens the map by chunk; g switches to source, where the biggest tile is selected.
	send(t, r, "t")
	if v := r.View(); !strings.Contains(v, "map by chunk") || !strings.Contains(v, "▶ Artifact · 13.4k") {
		t.Fatalf("chunk map:\n%s", v)
	}
	send(t, r, "g")
	tm, ok := r.top().(*treemapScreen)
	if !ok {
		t.Fatalf("top is %T, want treemap", r.top())
	}
	v := r.View()
	for _, want := range []string{"map by source", "built-in", "~/markdown/CLAUDE.md", "▶ built-in · 14.8k"} {
		if !strings.Contains(v, want) {
			t.Fatalf("treemap view lacks %q:\n%s", want, v)
		}
	}
	if tm.items[len(tm.items)-1].key != "+2 more" {
		t.Fatalf("tiny sources should be folded, items: %+v", tm.items)
	}

	// Arrows move off the big tile and back.
	send(t, r, "right")
	if tm.sel == 0 {
		t.Fatal("right did not move the selection")
	}
	send(t, r, "left")
	if tm.sel != 0 {
		t.Fatalf("left should return to the big tile, sel=%d", tm.sel)
	}

	// enter drills into the source's chunks; esc returns to the map.
	send(t, r, "enter")
	if v := r.View(); !strings.Contains(v, "Artifact") || strings.Contains(v, "plugin superpowers") {
		t.Fatalf("drill-down:\n%s", v)
	}
	send(t, r, "esc")
	if _, ok := r.top().(*treemapScreen); !ok {
		t.Fatalf("esc should return to the map, top is %T", r.top())
	}

	// g cycles to category, then chunk; enter on a chunk opens its detail.
	send(t, r, "g")
	if v := r.View(); !strings.Contains(v, "map by category") || !strings.Contains(v, "Tools") {
		t.Fatalf("category map:\n%s", v)
	}
	send(t, r, "g", "enter")
	if v := r.View(); !strings.Contains(v, "artifact schema") {
		t.Fatalf("chunk detail from map:\n%s", v)
	}
}
