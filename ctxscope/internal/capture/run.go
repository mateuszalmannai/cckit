package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Headless starts the proxy, runs `claude -p` through it in dir, and returns every
// exchange recorded. extraArgs are passed to claude (e.g. --settings); the prompt goes on stdin.
func Headless(ctx context.Context, dir, prompt string, extraArgs []string) ([]*Exchange, error) {
	p, err := Start(nil)
	if err != nil {
		return nil, err
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := headlessCmd(ctx, dir, prompt, p.URL(), extraArgs)
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return p.Exchanges(), fmt.Errorf("claude -p: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	// The response body may still be draining into the sniffer; give it a moment.
	time.Sleep(200 * time.Millisecond)
	return p.Exchanges(), nil
}

// headlessCmd builds `claude -p [extraArgs...]` with the prompt on stdin: a variadic flag such
// as --disallowedTools at the end of extraArgs would otherwise take the prompt as a value.
func headlessCmd(ctx context.Context, dir, prompt, baseURL string, extraArgs []string) *exec.Cmd {
	args := append([]string{"-p"}, extraArgs...)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ANTHROPIC_BASE_URL="+baseURL)
	cmd.Stdin = strings.NewReader(prompt)
	return cmd
}

// Main picks the exchange that carries the session's real context: the one with the largest
// request body. Side calls (titles, quota checks, topic detection) are much smaller.
func Main(exs []*Exchange) (*Exchange, error) {
	var best *Exchange
	for _, ex := range exs {
		if best == nil || len(ex.Request) > len(best.Request) {
			best = ex
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no /v1/messages request was captured")
	}
	if !json.Valid(best.Request) {
		return nil, fmt.Errorf("captured request is not valid JSON")
	}
	return best, nil
}
