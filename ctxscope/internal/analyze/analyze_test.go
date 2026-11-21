package analyze

import (
	"encoding/json"
	"strings"
	"testing"
)

type fakeResolver struct{}

func (fakeResolver) SkillSource(name string) (string, string) {
	if p, _, ok := strings.Cut(name, ":"); ok {
		return "plugin " + p, ""
	}
	if name == "tdd" {
		return "user ~/.claude/skills/tdd", ""
	}
	return "built-in", ""
}
func (fakeResolver) AgentSource(name string) (string, string) {
	if name == "reviewer" {
		return "user ~/.claude/agents/reviewer.md", ""
	}
	return "built-in", ""
}
func (fakeResolver) MemorySource(p string) (string, string) {
	return "~" + strings.TrimPrefix(p, "/home/u"), ""
}
func (fakeResolver) HookSources(event string) []string {
	if event == "SessionStart" {
		return []string{"plugin powers", "settings ~/.claude/settings.json"}
	}
	return nil
}
func (fakeResolver) Short(p string) string { return "~" + strings.TrimPrefix(p, "/home/u") }

const injected = `SessionStart hook additional context: <EXTREMELY_IMPORTANT>
You have powers. Use powers:brainstorming and powers:debugging, see also mattpocock:tdd.
## The Rule
Always.
</EXTREMELY_IMPORTANT>

# Environment
 - Primary working directory: /home/u/proj

The following deferred tools are now available via ToolSearch. Their schemas are NOT loaded:
CronCreate
WebSearch
mcp__claude_ai_Atlassian__authenticate
mcp__claude_ai_Atlassian__complete_authentication

Available agent types for the Agent tool:
- general-purpose: General agent. (Tools: *)
- reviewer: Use this agent to review code.

Examples:
- user: "Review this"
  assistant: "I'll use the reviewer"
<example>
- user: "Another"
</example> (Tools: Read, Grep)
- Explore: Read-only search agent. (Tools: All tools)

When you launch multiple agents, send them in a single message.

The following skills are available for use with the Skill tool:

- tdd: Test-driven development.
- powers:brainstorming: Use before creative work.
- powers:debugging: Use for bugs.
- mattpocock:tdd: Test-driven development, again.
- claude-api: Reference for the Claude API.
TRIGGER — read FIRST whenever the prompt names Claude.
SKIP only when another provider is used.
- llm:wiki-health (llm:wiki:health): Check wiki health.

# MCP Server Instructions

The following MCP servers have provided instructions:

## claude.ai Docs
Make docs here.

## easel
Tools registered by easel.
`

func fixture(t *testing.T) []byte {
	t.Helper()
	tool := func(name string, deferred bool) map[string]any {
		m := map[string]any{"name": name, "description": "does " + name, "input_schema": map[string]any{"type": "object"}}
		if deferred {
			m["defer_loading"] = true
		}
		return m
	}
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	req := map[string]any{
		"model": "claude-test",
		"system": []any{
			text("x-anthropic-billing-header: cc_version=1"),
			text("You are a Claude agent."),
			text("\nYou are an agent.\n\n# Harness\nRules.\n\n# Memory\nMemory rules.\n"),
		},
		"tools": []any{tool("Bash", false), tool("mcp__easel__chart", false), tool("DeferredToolPlaceholder", true)},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				text("<system-reminder>\nInstructions below.\n\nContents of /home/u/.claude/CLAUDE.md (user's private global instructions):\n\n# Global\nBe brief.\n\nContents of /home/u/.claude/RTK.md (user's private global instructions):\n\n# RTK\nUse rtk.\n\nContents of /home/u/proj/CLAUDE.md (project instructions):\n\n# Project\nNotes.\n</system-reminder>"),
				text("<system-reminder>\nContext:\n# userEmail\nu@example.com\n# gitStatus\nOn main.\n</system-reminder>"),
				text("<system-reminder>\nAttribution for git commits: add a trailer.\n</system-reminder>"),
				text("hi"),
			}},
			map[string]any{"role": "system", "content": []any{
				text(injected + "\nThe following tools just became available and are ready to use:\nmcp__claude_ai_Docs__batch\n"),
				map[string]any{"type": "tool_addition", "tool": map[string]any{"type": "tool_definition",
					"definition": map[string]any{"name": "mcp__claude_ai_Docs__batch", "description": "Create a doc."}}},
			}},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func byID(r *Result) map[string]Chunk {
	m := map[string]Chunk{}
	for _, c := range r.Chunks {
		m[c.ID] = c
	}
	return m
}

func TestAnalyzeSplitsAndAttributes(t *testing.T) {
	r, err := Analyze(fixture(t), fakeResolver{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := byID(r)

	want := map[string]struct{ cat, src string }{
		"System prompt/billing header":     {CatSystem, "built-in"},
		"System prompt/identity":           {CatSystem, "built-in"},
		"System prompt/intro":              {CatSystem, "built-in"},
		"System prompt/Harness":            {CatSystem, "built-in"},
		"System prompt/Memory":             {CatSystem, "built-in"},
		"tool/Bash":                        {CatTools, "built-in"},
		"tool/mcp__easel__chart":           {CatTools, "MCP easel"},
		"tool/DeferredToolPlaceholder":     {CatDeferred, "built-in"},
		"memory//home/u/.claude/CLAUDE.md": {CatMemory, "~/.claude/CLAUDE.md"},
		"memory//home/u/.claude/RTK.md":    {CatMemory, "~/.claude/RTK.md"},
		"memory//home/u/proj/CLAUDE.md":    {CatMemory, "~/proj/CLAUDE.md"},
		"session/userEmail":                {CatSession, "built-in"},
		"session/gitStatus":                {CatSession, "built-in"},
		"session/attribution":              {CatSession, "built-in"},
		"Your prompt/prompt":               {CatPrompt, "you"},
		"hook/SessionStart":                {CatHooks, "plugin powers"},
		"session/environment":              {CatSession, "built-in"},
		"deferred/built-in":                {CatDeferred, "built-in"},
		"deferred/MCP claude_ai_Atlassian": {CatDeferred, "MCP claude_ai_Atlassian"},
		"agent/general-purpose":            {CatAgents, "built-in"},
		"agent/reviewer":                   {CatAgents, "user ~/.claude/agents/reviewer.md"},
		"agent/Explore":                    {CatAgents, "built-in"},
		"agents/trailer":                   {CatAgents, "built-in"},
		"skill/tdd":                        {CatSkills, "user ~/.claude/skills/tdd"},
		"skill/powers:brainstorming":       {CatSkills, "plugin powers"},
		"skill/mattpocock:tdd":             {CatSkills, "plugin mattpocock"},
		"skill/claude-api":                 {CatSkills, "built-in"},
		"skill/llm:wiki-health":            {CatSkills, "plugin llm"},
		"mcp/claude.ai Docs":               {CatMCP, "MCP claude.ai Docs"},
		"mcp/easel":                        {CatMCP, "MCP easel"},
	}
	for id, w := range want {
		c, ok := got[id]
		if !ok {
			t.Errorf("missing chunk %q", id)
			continue
		}
		if c.Category != w.cat || c.Source != w.src {
			t.Errorf("%s: got %s / %s, want %s / %s", id, c.Category, c.Source, w.cat, w.src)
		}
	}

	if n := got["deferred/built-in"].Name; n != "built-in (2 names)" {
		t.Errorf("deferred built-in group name = %q", n)
	}
	// The reviewer agent keeps its "- user:" example lines instead of being split there.
	if txt := got["agent/reviewer"].Text; !strings.Contains(txt, `- user: "Another"`) || !strings.Contains(txt, "(Tools: Read, Grep)") {
		t.Errorf("reviewer agent was split inside its examples:\n%s", txt)
	}
	// Continuation lines belong to the skill above them.
	if txt := got["skill/claude-api"].Text; !strings.Contains(txt, "SKIP only when") {
		t.Errorf("claude-api lost its continuation lines:\n%s", txt)
	}
	if n := got["hook/SessionStart"].Note; !strings.Contains(n, "settings ~/.claude/settings.json") {
		t.Errorf("hook note does not name the other SessionStart source: %q", n)
	}
	if got["skill/tdd"].Note != "duplicate of mattpocock:tdd" || got["skill/mattpocock:tdd"].Note != "duplicate of tdd" {
		t.Errorf("duplicates not marked: %q / %q", got["skill/tdd"].Note, got["skill/mattpocock:tdd"].Note)
	}
}

func TestAnalyzeKeepsEveryCharacterOfMessageText(t *testing.T) {
	r, err := Analyze(fixture(t), fakeResolver{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, c := range r.Chunks {
		all.WriteString(c.Text)
	}
	// Every non-blank line of the injected message must land in some chunk.
	for _, line := range strings.Split(injected, "\n") {
		if strings.TrimSpace(line) != "" && !strings.Contains(all.String(), line) {
			t.Errorf("line lost: %q", line)
		}
	}
}

func TestCalibrationSumsExactly(t *testing.T) {
	for _, total := range []int{1234, 36249, 99} {
		r, err := Analyze(fixture(t), fakeResolver{}, total)
		if err != nil {
			t.Fatal(err)
		}
		sum := 0
		for _, c := range r.Chunks {
			sum += c.Tokens
		}
		if sum != total || r.Total != total || !r.Calibrated {
			t.Errorf("total %d: chunks sum to %d, Total=%d calibrated=%v", total, sum, r.Total, r.Calibrated)
		}
	}
}

func TestUncalibratedUsesEstimates(t *testing.T) {
	r, err := Analyze(fixture(t), fakeResolver{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Calibrated {
		t.Error("calibrated without a total")
	}
	for _, c := range r.Chunks {
		if c.Tokens != c.Estimate {
			t.Errorf("%s: tokens %d != estimate %d", c.ID, c.Tokens, c.Estimate)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("abcdefgh"); got != 3 { // ceil(8/3.8)
		t.Errorf("ascii: %d", got)
	}
	if got := EstimateTokens("启动规划"); got != 4 {
		t.Errorf("cjk: %d", got)
	}
}

func TestStringSystemAndContent(t *testing.T) {
	req := `{"model":"m","system":"plain system","messages":[{"role":"user","content":"hello"}]}`
	r, err := Analyze([]byte(req), fakeResolver{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := byID(r)
	if _, ok := got["Your prompt/prompt"]; !ok {
		t.Errorf("string content not parsed: %+v", r.Chunks)
	}
}
