package capture

import (
	"context"
	"io"
	"slices"
	"testing"
)

// A variadic flag such as --disallowedTools at the end of extraArgs must not swallow the
// prompt, so the prompt goes to stdin instead of the argument list.
func TestHeadlessCmdPromptOnStdin(t *testing.T) {
	cmd := headlessCmd(context.Background(), "/tmp", "hi", "http://127.0.0.1:1", []string{"--disallowedTools", "Workflow"})
	want := []string{"claude", "-p", "--disallowedTools", "Workflow"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
	in, err := io.ReadAll(cmd.Stdin)
	if err != nil || string(in) != "hi" {
		t.Fatalf("stdin = %q, %v; want \"hi\"", in, err)
	}
	if !slices.Contains(cmd.Env, "ANTHROPIC_BASE_URL=http://127.0.0.1:1") || cmd.Dir != "/tmp" {
		t.Fatalf("env or dir not set: dir=%q", cmd.Dir)
	}
}
