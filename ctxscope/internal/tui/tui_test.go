package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
)

func save(t *testing.T, label string, created time.Time, chunks ...analyze.Chunk) {
	t.Helper()
	total := 0
	for _, c := range chunks {
		total += c.Tokens
	}
	s := &store.Snapshot{Created: created, Label: label, Mode: "headless", Cwd: "/proj",
		Result: analyze.Result{Model: "m", Total: total, Calibrated: true, Chunks: chunks}}
	if err := store.Save(s, []byte("{}")); err != nil {
		t.Fatal(err)
	}
}

func chunk(id, cat, src, text string, tokens int) analyze.Chunk {
	return analyze.Chunk{ID: id, Category: cat, Name: id, Source: src, Text: text, Tokens: tokens, Hint: "hint for " + id}
}

func keyMsg(s string) tea.Msg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(t *testing.T, r *root, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_, cmd := r.Update(keyMsg(k))
		drain(r, cmd, 0)
	}
}

// drain runs commands synchronously (as the bubbletea runtime would), so asynchronous list
// filtering settles before the test looks at the view. Blink/tick commands are skipped by
// bounding the depth.
func drain(r *root, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 3 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond): // timers (cursor blink, status messages)
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drain(r, c, depth+1)
		}
		return
	}
	if msg != nil {
		_, next := r.Update(msg)
		drain(r, next, depth+1)
	}
}

func TestNavigation(t *testing.T) {
	t.Setenv("CTXSCOPE_HOME", t.TempDir())
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	save(t, "before", base,
		chunk("hook/SessionStart", analyze.CatHooks, "plugin superpowers", "You have superpowers", 1200),
		chunk("tool/Bash", analyze.CatTools, "built-in", "bash schema", 1400),
		chunk("skill/tdd", analyze.CatSkills, "user skill", "tdd", 60))
	save(t, "after", base.Add(time.Minute),
		chunk("tool/Bash", analyze.CatTools, "built-in", "bash schema", 1400),
		chunk("skill/tdd", analyze.CatSkills, "user skill", "tdd", 60))

	r, err := newRoot("")
	if err != nil {
		t.Fatal(err)
	}
	r.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if v := r.View(); !strings.Contains(v, "after") || !strings.Contains(v, "before") {
		t.Fatalf("snapshot list:\n%s", v)
	}

	// Newest is selected first; d diffs it against the one before.
	send(t, r, "d")
	if v := r.View(); !strings.Contains(v, "SessionStart") || !strings.Contains(v, "removed") || strings.Contains(v, "tool/Bash") {
		t.Fatalf("diff view:\n%s", v)
	}
	send(t, r, "esc")

	// Open the newest snapshot: chunks sorted by tokens.
	send(t, r, "enter")
	if len(r.stack) != 2 {
		t.Fatalf("stack depth %d after enter", len(r.stack))
	}
	if v := r.View(); !strings.Contains(v, "tool/Bash") || !strings.Contains(v, "by chunk") {
		t.Fatalf("chunk view:\n%s", v)
	}

	// g regroups by source; enter drills into the selected source.
	send(t, r, "g")
	if v := r.View(); !strings.Contains(v, "by source") || !strings.Contains(v, "built-in") {
		t.Fatalf("source view:\n%s", v)
	}
	send(t, r, "enter")
	if v := r.View(); !strings.Contains(v, "built-in ·") || strings.Contains(v, "skill/tdd") {
		t.Fatalf("drill-down view:\n%s", v)
	}
	send(t, r, "esc")

	// Back to chunk grouping, filter for tdd, open its detail.
	send(t, r, "g", "g", "/", "t", "d", "d", "enter")
	if v := r.View(); !strings.Contains(v, "skill/tdd") || strings.Contains(v, "tool/Bash") {
		t.Fatalf("filtered view:\n%s", v)
	}
	send(t, r, "enter")
	if v := r.View(); !strings.Contains(v, "hint for skill/tdd") || !strings.Contains(v, "id: skill/tdd") {
		t.Fatalf("detail view:\n%s", v)
	}
	// esc leaves detail; the next esc clears the filter instead of leaving the chunk screen.
	send(t, r, "esc", "esc")
	if len(r.stack) != 2 || !strings.Contains(r.View(), "tool/Bash") {
		t.Fatalf("esc should clear the filter first; depth %d\n%s", len(r.stack), r.View())
	}
	send(t, r, "esc")
	if len(r.stack) != 1 {
		t.Fatalf("esc should return to snapshots; depth %d", len(r.stack))
	}
}
