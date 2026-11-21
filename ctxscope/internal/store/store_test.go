package store

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	t.Setenv("CTXSCOPE_HOME", t.TempDir())
	if _, err := Resolve(""); err == nil {
		t.Fatal("expected error with no snapshots")
	}
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i, label := range []string{"baseline", "no superpowers", "trimmed"} {
		s := &Snapshot{Created: base.Add(time.Duration(i) * time.Minute), Label: label}
		if err := Save(s, []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"":         "20261120-100200-trimmed",
		"latest":   "20261120-100200-trimmed",
		"latest~1": "20261120-100100-no-superpowers",
		"~2":       "20261120-100000-baseline",
		"1":        "20261120-100000-baseline",
		"superp":   "20261120-100100-no-superpowers",
	}
	for ref, want := range cases {
		got, err := Resolve(ref)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	for _, bad := range []string{"~3", "4", "nomatch", "2026"} {
		if _, err := Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) should fail", bad)
		}
	}
}
