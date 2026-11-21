// Package store keeps snapshots on disk, one directory each:
//
//	$CTXSCOPE_HOME (default ~/.local/share/ctxscope)/snapshots/<YYYYMMDD-HHMMSS>[-label]/
//	    request.json   the captured request body (may contain personal context; mode 0600)
//	    snapshot.json  metadata plus the attributed chunks
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/capture"
)

// Snapshot is one analysed capture.
type Snapshot struct {
	Name    string         `json:"name"` // directory name, also the reference used on the CLI
	Created time.Time      `json:"created"`
	Label   string         `json:"label,omitempty"`
	Mode    string         `json:"mode"` // headless, interactive or import
	Cwd     string         `json:"cwd"`
	Args    []string       `json:"args,omitempty"`
	Usage   *capture.Usage `json:"usage,omitempty"`
	analyze.Result
}

// Root is the snapshots directory.
func Root() string {
	if d := os.Getenv("CTXSCOPE_HOME"); d != "" {
		return filepath.Join(d, "snapshots")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ctxscope", "snapshots")
}

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Save writes a new snapshot directory and fills in s.Name.
func Save(s *Snapshot, request []byte) error {
	name := s.Created.Format("20060102-150405")
	if s.Label != "" {
		name += "-" + strings.Trim(unsafe.ReplaceAllString(s.Label, "-"), "-")
	}
	dir := filepath.Join(Root(), name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.Name = name
	if err := os.WriteFile(filepath.Join(dir, "request.json"), request, 0o600); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "snapshot.json"), b, 0o600)
}

// Rewrite replaces an existing snapshot's snapshot.json (after re-analysis).
func Rewrite(s *Snapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Root(), s.Name, "snapshot.json"), b, 0o600)
}

// Request reads a snapshot's captured request body.
func Request(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(Root(), name, "request.json"))
}

// List returns all snapshot names, oldest first.
func List() ([]string, error) {
	entries, err := os.ReadDir(Root())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && fileExists(filepath.Join(Root(), e.Name(), "snapshot.json")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Load reads a snapshot by name.
func Load(name string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(Root(), name, "snapshot.json"))
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	s.Name = name
	return &s, nil
}

// Resolve turns a CLI reference into a snapshot name. Accepted: "" or "latest" (newest),
// "latest~N" or "~N" (N before the newest), a 1-based index as printed by `list`, or a unique
// prefix/substring of a name.
func Resolve(ref string) (string, error) {
	names, err := List()
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no snapshots yet; run `ctxscope capture` first")
	}
	back := -1
	switch {
	case ref == "" || ref == "latest":
		back = 0
	case strings.HasPrefix(ref, "latest~") || strings.HasPrefix(ref, "~"):
		_, num, _ := strings.Cut(ref, "~")
		n, err := strconv.Atoi(num)
		if err != nil || n < 0 {
			return "", fmt.Errorf("bad reference %q", ref)
		}
		back = n
	}
	if back >= 0 {
		if back >= len(names) {
			return "", fmt.Errorf("%q: only %d snapshots", ref, len(names))
		}
		return names[len(names)-1-back], nil
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(names) {
			return "", fmt.Errorf("index %d out of range 1..%d", n, len(names))
		}
		return names[n-1], nil
	}
	var hits []string
	for _, n := range names {
		if n == ref {
			return n, nil
		}
		if strings.Contains(n, ref) {
			hits = append(hits, n)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no snapshot matches %q", ref)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q matches %d snapshots: %s", ref, len(hits), strings.Join(hits, ", "))
	}
}
