package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
)

func snap(total int, chunks ...analyze.Chunk) *store.Snapshot {
	return &store.Snapshot{Name: "s", Result: analyze.Result{Total: total, Calibrated: true, Chunks: chunks}}
}

func ch(id, cat, src, text string, tokens int) analyze.Chunk {
	return analyze.Chunk{ID: id, Category: cat, Name: id, Source: src, Text: text, Tokens: tokens}
}

func TestDiffIgnoresRecalibrationOfUnchangedText(t *testing.T) {
	a := snap(1100,
		ch("tool/Bash", "Tools", "built-in", "same", 500),
		ch("hook/SessionStart", "Hooks", "plugin x", "hook", 400),
		ch("memory/CLAUDE.md", "Memory files", "CLAUDE.md", "old", 200))
	b := snap(780,
		ch("tool/Bash", "Tools", "built-in", "same", 530), // re-weighted, text unchanged
		ch("memory/CLAUDE.md", "Memory files", "CLAUDE.md", "new and longer", 250),
		ch("skill/new", "Skills", "user", "x", 0+10))
	changes := Diff(a, b, 5)
	got := map[string]Change{}
	for _, c := range changes {
		got[c.ID] = c
	}
	if _, ok := got["tool/Bash"]; ok {
		t.Error("unchanged text reported as a change")
	}
	if c := got["hook/SessionStart"]; c.Status() != "removed" || c.Delta() != -400 {
		t.Errorf("hook: %+v", c)
	}
	if c := got["memory/CLAUDE.md"]; c.Status() != "changed" || c.Delta() != 50 {
		t.Errorf("memory: %+v", c)
	}
	if c := got["skill/new"]; c.Status() != "added" || c.Delta() != 10 {
		t.Errorf("skill: %+v", c)
	}
	if changes[0].ID != "hook/SessionStart" {
		t.Errorf("not sorted by |Δ|: first is %s", changes[0].ID)
	}

	var out bytes.Buffer
	WriteDiff(&out, a, b, 5, 0)
	// -320 total, -340 attributed (-400 + 50 + 10) → +20 residual.
	if !strings.Contains(out.String(), "+20 tokens of the total change are calibration residual") {
		t.Errorf("residual line missing:\n%s", out.String())
	}
}

func TestGroupByCategoryKeepsFixedOrder(t *testing.T) {
	cs := []analyze.Chunk{
		ch("a", analyze.CatSkills, "x", "", 900),
		ch("b", analyze.CatSystem, "x", "", 10),
		ch("c", analyze.CatSkills, "y", "", 100),
	}
	g := GroupBy(cs, "category")
	if len(g) != 2 || g[0].Key != analyze.CatSystem || g[1].Tokens != 1000 || g[1].Count != 2 {
		t.Errorf("got %+v", g)
	}
	s := GroupBy(cs, "source")
	if s[0].Key != "x" || s[0].Tokens != 910 {
		t.Errorf("source grouping: %+v", s)
	}
}

func TestDuplicatesListedOncePerGroup(t *testing.T) {
	a := ch("skill/tdd", "Skills", "user", "", 50)
	a.Note = "duplicate of mp:tdd"
	b := ch("skill/mp:tdd", "Skills", "plugin mp", "", 60)
	b.Name, a.Name = "mp:tdd", "tdd"
	b.Note = "duplicate of tdd"
	got := duplicates([]analyze.Chunk{a, b})
	if len(got) != 1 || !strings.Contains(got[0], "tdd ↔ mp:tdd") || !strings.Contains(got[0], "110") {
		t.Errorf("got %q", got)
	}
}

func TestK(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1.0k", 36249: "36.2k", -1200: "-1.2k"} {
		if got := K(n); got != want {
			t.Errorf("K(%d) = %q, want %q", n, got, want)
		}
	}
}

// A local skill that shadowed a built-in of the same name keeps its ID when it is removed, but
// its tokens belong to the user source, not to built-in.
func TestDiffSplitsSourceChange(t *testing.T) {
	a := snap(155, ch("skill/code-review", "Skills", "user ~/.claude/skills/code-review", "local", 155))
	b := snap(451, ch("skill/code-review", "Skills", "built-in", "bundled", 451))
	changes := Diff(a, b, 0)
	if len(changes) != 2 {
		t.Fatalf("want removed + added, got %+v", changes)
	}
	bySrc := map[string]Change{}
	for _, c := range changes {
		bySrc[c.Source] = c
	}
	if c := bySrc["user ~/.claude/skills/code-review"]; c.Status() != "removed" || c.Delta() != -155 {
		t.Errorf("user copy: %+v", c)
	}
	if c := bySrc["built-in"]; c.Status() != "added" || c.Delta() != 451 {
		t.Errorf("built-in: %+v", c)
	}
}
