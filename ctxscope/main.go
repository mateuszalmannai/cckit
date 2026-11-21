// ctxscope shows what Claude Code loads into the context window before you type anything,
// attributes every token to the file, plugin or setting that put it there, and diffs two
// captures so you can see what a config change saved.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mateuszalmannai/cckit/ctxscope/internal/analyze"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/capture"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/report"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/store"
	"github.com/mateuszalmannai/cckit/ctxscope/internal/tui"
)

const usage = `ctxscope — what is in Claude Code's starting context, and where it came from

Usage:
  ctxscope capture [-label L] [-dir D] [-prompt P] [-- claude args...]
        Run "claude -p" once through a local logging proxy and save a snapshot.
        Headless mode leaves out claude.ai connectors and some tools; use proxy for the
        interactive context.
  ctxscope proxy [-label L]
        Start the proxy and print the command for an interactive session; the first full
        request is saved as a snapshot. Stop with Ctrl-C.
  ctxscope import [-label L] [-total N] request.json
        Analyse a request body captured elsewhere (e.g. a request logger).
  ctxscope list
  ctxscope reanalyze [snapshot|all]
        Re-run attribution on stored requests (after updating ctxscope).
  ctxscope report [-by category|source|chunk] [-n N] [-hints] [snapshot]
  ctxscope diff [-min N] [-n N] [A] [B]      (default: the two newest snapshots)
  ctxscope show [snapshot] <chunk-id-or-name>
  ctxscope tui [snapshot]

Snapshot references: latest (default), latest~1 or ~1, the index from list, or part of a name.
Snapshots live in $CTXSCOPE_HOME/snapshots (default ~/.local/share/ctxscope/snapshots); they
contain your full prompt context; keep them out of any repository you share or sync.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "capture":
		err = cmdCapture(args)
	case "proxy":
		err = cmdProxy(args)
	case "import":
		err = cmdImport(args)
	case "reanalyze":
		err = cmdReanalyze(args)
	case "list", "ls":
		err = cmdList()
	case "report":
		err = cmdReport(args)
	case "diff":
		err = cmdDiff(args)
	case "show":
		err = cmdShow(args)
	case "tui":
		err = cmdTUI(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctxscope:", err)
		os.Exit(1)
	}
}

func resolver(cwd string) analyze.Resolver {
	home, _ := os.UserHomeDir()
	return analyze.NewFSResolver(home, cwd)
}

// save analyses a request and stores it as a snapshot, then prints the category report.
func save(req []byte, u *capture.Usage, mode, label, cwd string, args []string) error {
	total := 0
	if u != nil {
		total = u.Total()
	}
	res, err := analyze.Analyze(req, resolver(cwd), total)
	if err != nil {
		return err
	}
	s := &store.Snapshot{Created: time.Now(), Label: label, Mode: mode, Cwd: cwd, Args: args, Usage: u, Result: *res}
	if err := store.Save(s, req); err != nil {
		return err
	}
	report.Write(os.Stdout, s, "category", 0, false)
	fmt.Printf("\nsaved %s — `ctxscope report -by source`, `ctxscope tui`\n", s.Name)
	return nil
}

func cmdCapture(args []string) error {
	fs := flag.NewFlagSet("capture", flag.ExitOnError)
	label := fs.String("label", "", "label to add to the snapshot name")
	dir := fs.String("dir", "", "directory to start claude in (default: current)")
	prompt := fs.String("prompt", "hi", "prompt to send")
	fs.Parse(args)
	cwd := *dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwd, _ = filepath.Abs(cwd)
	extra := fs.Args()
	fmt.Fprintf(os.Stderr, "running claude -p %s in %s …\n", strings.Join(append(extra, *prompt), " "), cwd)
	exs, err := capture.Headless(context.Background(), cwd, *prompt, extra)
	if err != nil && len(exs) == 0 {
		return err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	ex, err := capture.Main(exs)
	if err != nil {
		return err
	}
	return save(ex.Request, ex.Usage, "headless", *label, cwd, extra)
}

func cmdProxy(args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	label := fs.String("label", "", "label to add to the snapshot name")
	fs.Parse(args)
	cwd, _ := os.Getwd()
	done := make(chan *capture.Exchange, 1)
	p, err := capture.Start(func(ex *capture.Exchange) {
		if isMain(ex.Request) {
			select {
			case done <- ex:
			default:
			}
		}
	})
	if err != nil {
		return err
	}
	defer p.Close()
	fmt.Printf("Proxy on %s. In another terminal, in the project you want to measure, run:\n\n", p.URL())
	fmt.Printf("  ANTHROPIC_BASE_URL=%s claude\n\n", p.URL())
	fmt.Println("then send a short message (e.g. hi). The first full request is saved; keep this")
	fmt.Println("running until you quit claude, then press Ctrl-C.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	saved := false
	for {
		select {
		case ex := <-done:
			if saved {
				continue
			}
			saved = true
			fmt.Println()
			// The interactive session's directory is unknown to the proxy; read it from the
			// request's environment section when present.
			dir := requestCwd(ex.Request, cwd)
			if err := save(ex.Request, ex.Usage, "interactive", *label, dir, nil); err != nil {
				return err
			}
			fmt.Println("\nsnapshot saved; you can quit claude and press Ctrl-C")
		case <-sig:
			if !saved {
				return fmt.Errorf("stopped before a request arrived")
			}
			return nil
		}
	}
}

// isMain reports whether a request is a real conversation turn rather than a side call: it
// carries tools.
func isMain(req []byte) bool {
	var r struct {
		Tools []json.RawMessage `json:"tools"`
	}
	return json.Unmarshal(req, &r) == nil && len(r.Tools) > 3
}

func requestCwd(req []byte, fallback string) string {
	const key = "Primary working directory: "
	s := string(req)
	if i := strings.Index(s, key); i >= 0 {
		rest := s[i+len(key):]
		if j := strings.IndexAny(rest, `\"`); j > 0 {
			return rest[:j]
		}
	}
	return fallback
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	label := fs.String("label", "", "label to add to the snapshot name")
	total := fs.Int("total", 0, "API-reported input tokens, to calibrate the estimates")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("import needs one request.json path")
	}
	req, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var u *capture.Usage
	if *total > 0 {
		u = &capture.Usage{InputTokens: *total}
	}
	cwd, _ := os.Getwd()
	return save(req, u, "import", *label, requestCwd(req, cwd), nil)
}

func cmdReanalyze(args []string) error {
	var names []string
	if len(args) > 0 && args[0] == "all" {
		var err error
		if names, err = store.List(); err != nil {
			return err
		}
	} else {
		ref := ""
		if len(args) > 0 {
			ref = args[0]
		}
		name, err := store.Resolve(ref)
		if err != nil {
			return err
		}
		names = []string{name}
	}
	for _, n := range names {
		s, err := store.Load(n)
		if err != nil {
			return err
		}
		req, err := store.Request(n)
		if err != nil {
			return err
		}
		total := 0
		if s.Usage != nil {
			total = s.Usage.Total()
		}
		res, err := analyze.Analyze(req, resolver(s.Cwd), total)
		if err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
		s.Result = *res
		if err := store.Rewrite(s); err != nil {
			return err
		}
		fmt.Printf("reanalysed %s (%d chunks)\n", n, len(res.Chunks))
	}
	return nil
}

func cmdList() error {
	names, err := store.List()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("no snapshots yet; run `ctxscope capture`")
		return nil
	}
	for i, n := range names {
		s, err := store.Load(n)
		if err != nil {
			fmt.Printf("%3d  %s (unreadable: %v)\n", i+1, n, err)
			continue
		}
		fmt.Printf("%3d  %7s  %-11s  %s  %s\n", i+1, report.K(s.Total), s.Mode, n, s.Cwd)
	}
	return nil
}

func load(ref string) (*store.Snapshot, error) {
	name, err := store.Resolve(ref)
	if err != nil {
		return nil, err
	}
	return store.Load(name)
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	by := fs.String("by", "category", "group by category, source or chunk")
	n := fs.Int("n", 0, "show only the top N rows (0 = all)")
	hints := fs.Bool("hints", false, "show how to remove each item")
	fs.Parse(args)
	switch *by {
	case "category", "source", "chunk":
	default:
		return fmt.Errorf("-by must be category, source or chunk")
	}
	s, err := load(fs.Arg(0))
	if err != nil {
		return err
	}
	report.Write(os.Stdout, s, *by, *n, *hints)
	return nil
}

func cmdDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	threshold := fs.Int("min", 5, "hide chunk changes of this many tokens or fewer (calibration noise)")
	n := fs.Int("n", 40, "show at most N chunk rows (0 = all)")
	fs.Parse(args)
	refA, refB := "latest~1", "latest"
	switch fs.NArg() {
	case 0:
	case 1:
		refA = fs.Arg(0)
	case 2:
		refA, refB = fs.Arg(0), fs.Arg(1)
	default:
		return fmt.Errorf("diff takes at most two snapshots")
	}
	a, err := load(refA)
	if err != nil {
		return err
	}
	b, err := load(refB)
	if err != nil {
		return err
	}
	report.WriteDiff(os.Stdout, a, b, *threshold, *n)
	return nil
}

func cmdShow(args []string) error {
	ref, id := "", ""
	switch len(args) {
	case 1:
		id = args[0]
	case 2:
		ref, id = args[0], args[1]
	default:
		return fmt.Errorf("show [snapshot] <chunk-id-or-name>")
	}
	s, err := load(ref)
	if err != nil {
		return err
	}
	for _, c := range s.Chunks {
		if c.ID == id || c.Name == id {
			fmt.Printf("%s · %s · %s tokens · %s\n", c.ID, c.Category, report.K(c.Tokens), c.Source)
			if c.Hint != "" {
				fmt.Println("↳ " + c.Hint)
			}
			fmt.Println(strings.Repeat("─", 60))
			fmt.Println(c.Text)
			return nil
		}
	}
	return fmt.Errorf("no chunk %q in %s (ids are listed in the tui detail view)", id, s.Name)
}

func cmdTUI(args []string) error {
	open := ""
	if len(args) > 0 {
		name, err := store.Resolve(args[0])
		if err != nil {
			return err
		}
		open = name
	}
	return tui.Run(open)
}
