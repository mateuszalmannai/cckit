// Package analyze splits a captured /v1/messages request into chunks — one per tool, skill,
// agent, memory file, hook injection, system-prompt section and so on — and attributes each
// chunk to the file, plugin or setting that put it there.
//
// The request layout is Claude Code's, not a public contract, so the splitter keys on the
// section markers Claude Code currently writes and falls back to "Other" chunks for anything
// it does not recognise. Nothing is dropped: every character of the request ends up in a chunk.
package analyze

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Categories, in display order.
const (
	CatSystem   = "System prompt"
	CatTools    = "Tools"
	CatDeferred = "Deferred tools"
	CatMemory   = "Memory files"
	CatHooks    = "Hooks"
	CatSkills   = "Skills"
	CatAgents   = "Agents"
	CatMCP      = "MCP instructions"
	CatSession  = "Session context"
	CatPrompt   = "Your prompt"
	CatOther    = "Other"
)

// CategoryOrder lists the categories in the order reports show them.
var CategoryOrder = []string{CatSystem, CatTools, CatDeferred, CatMemory, CatHooks, CatSkills, CatAgents, CatMCP, CatSession, CatPrompt, CatOther}

// Chunk is one attributable piece of the request.
type Chunk struct {
	ID       string `json:"id"` // stable across snapshots, used to diff
	Category string `json:"category"`
	Name     string `json:"name"`
	Source   string `json:"source"`         // what put it there: a file, plugin, MCP server, "built-in"
	Hint     string `json:"hint,omitempty"` // how to remove or shrink it
	Note     string `json:"note,omitempty"` // e.g. duplicate warnings
	Text     string `json:"text"`
	Estimate int    `json:"estimate"` // heuristic token count
	Tokens   int    `json:"tokens"`   // estimate scaled so all chunks sum to the API-reported total
}

// Result is the analysed request.
type Result struct {
	Model      string  `json:"model"`
	Chunks     []Chunk `json:"chunks"`
	Total      int     `json:"total"`      // sum of Tokens
	Calibrated bool    `json:"calibrated"` // Total equals the API-reported input tokens
}

type block struct {
	Type string          `json:"type"`
	Text string          `json:"text"`
	Raw  json.RawMessage `json:"-"`
}

type request struct {
	Model    string            `json:"model"`
	System   json.RawMessage   `json:"system"`
	Tools    []json.RawMessage `json:"tools"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// Analyze splits req into chunks. actualTotal is the API-reported input token count (0 if
// unknown); when set, chunk estimates are scaled to sum to it.
func Analyze(req []byte, res Resolver, actualTotal int) (*Result, error) {
	var r request
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	a := &analyzer{res: res}

	sys, err := blocks(r.System)
	if err != nil {
		return nil, fmt.Errorf("parse system: %w", err)
	}
	for i, b := range sys {
		a.systemBlock(i, b.Text)
	}
	for _, raw := range r.Tools {
		a.tool(raw)
	}
	lastUser := -1
	for i, m := range r.Messages {
		if m.Role == "user" {
			lastUser = i
		}
	}
	for i, m := range r.Messages {
		bs, err := blocks(m.Content)
		if err != nil {
			return nil, fmt.Errorf("parse message %d: %w", i, err)
		}
		for _, b := range bs {
			if b.Type != "text" {
				a.nonText(b)
				continue
			}
			a.textBlock(b.Text, i == lastUser)
		}
	}
	a.attributeHooks()
	a.markDuplicates()
	out := &Result{Model: r.Model, Chunks: a.chunks}
	calibrate(out, actualTotal)
	return out, nil
}

// blocks accepts either a plain string or an array of content blocks.
func blocks(raw json.RawMessage) ([]block, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []block{{Type: "text", Text: s}}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil, err
	}
	bs := make([]block, len(raws))
	for i, r := range raws {
		if err := json.Unmarshal(r, &bs[i]); err != nil {
			return nil, err
		}
		bs[i].Raw = r
	}
	return bs, nil
}

type analyzer struct {
	res     Resolver
	chunks  []Chunk
	ids     map[string]int
	plugins map[string]bool // plugin prefixes seen in skill names, for hook attribution
}

func (a *analyzer) add(c Chunk) {
	if strings.TrimSpace(c.Text) == "" {
		return
	}
	if a.ids == nil {
		a.ids = map[string]int{}
	}
	base := c.ID
	if base == "" {
		base = c.Category + "/" + c.Name
	}
	a.ids[base]++
	c.ID = base
	if n := a.ids[base]; n > 1 {
		c.ID = fmt.Sprintf("%s#%d", base, n)
	}
	c.Estimate = EstimateTokens(c.Text)
	a.chunks = append(a.chunks, c)
}

// --- system prompt ---------------------------------------------------------------------------

var h1 = regexp.MustCompile(`(?m)^# \S.*$`)

func (a *analyzer) systemBlock(i int, text string) {
	if strings.HasPrefix(text, "x-anthropic-billing-header") {
		a.add(Chunk{Category: CatSystem, Name: "billing header", Source: "built-in", Text: text})
		return
	}
	sections := splitAt(text, h1)
	for _, s := range sections {
		name := s.title
		if name == "" {
			name = untitledSystemName(i, s.body, len(sections) == 1)
		}
		a.add(Chunk{Category: CatSystem, Name: name, Source: "built-in", Hint: "Claude Code's own prompt; not configurable", Text: s.body})
	}
}

// untitledSystemName labels system-prompt text that has no "# " heading. The names stay the
// same across modes (headless and interactive open with different identity lines), so these
// chunks pair up in a diff:
//   - a block that is only a "You are …" line is the identity line;
//   - text before a block's first heading is that block's intro.
func untitledSystemName(i int, body string, wholeBlock bool) string {
	switch {
	case wholeBlock && strings.HasPrefix(strings.TrimSpace(body), "You are"):
		return "identity"
	case !wholeBlock:
		return "intro"
	}
	return fmt.Sprintf("block %d: %s", i, firstLine(body, 50))
}

// --- tools -----------------------------------------------------------------------------------

func (a *analyzer) tool(raw json.RawMessage) {
	var t struct {
		Name         string `json:"name"`
		DeferLoading bool   `json:"defer_loading"`
	}
	_ = json.Unmarshal(raw, &t)
	cat := CatTools
	if t.DeferLoading {
		cat = CatDeferred
	}
	src, hint := toolSource(t.Name)
	a.add(Chunk{ID: "tool/" + t.Name, Category: cat, Name: t.Name, Source: src, Hint: hint, Text: string(raw)})
}

func toolSource(name string) (string, string) {
	if server, ok := mcpServer(name); ok {
		return "MCP " + server, mcpHint(server)
	}
	return "built-in", "built-in tool: drop it with --disallowedTools or permissions.deny, then diff to confirm it left the payload"
}

func mcpServer(tool string) (string, bool) {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "", false
	}
	server, _, _ := strings.Cut(rest, "__")
	return server, true
}

func mcpHint(server string) string {
	if strings.HasPrefix(server, "claude_ai_") {
		return "claude.ai connector: disconnect it at claude.ai → Settings → Connectors, or disable it in /mcp"
	}
	return "MCP server: disable it in /mcp, or remove it from .mcp.json / ~/.claude.json"
}

// nonText handles structured message blocks. tool_addition blocks carry the schema of a tool
// that became available after startup (typically an MCP server that finished connecting).
func (a *analyzer) nonText(b block) {
	if b.Type == "tool_addition" {
		var t struct {
			Tool struct {
				Definition struct {
					Name string `json:"name"`
				} `json:"definition"`
			} `json:"tool"`
		}
		_ = json.Unmarshal(b.Raw, &t)
		name := t.Tool.Definition.Name
		src, hint := toolSource(name)
		a.add(Chunk{ID: "tool/" + name, Category: CatTools, Name: name, Source: src, Hint: hint, Text: string(b.Raw)})
		return
	}
	a.add(Chunk{Category: CatOther, Name: "message " + b.Type + " block", Source: "conversation", Text: string(b.Raw)})
}

// --- message text ----------------------------------------------------------------------------

const reminderOpen, reminderClose = "<system-reminder>", "</system-reminder>"

func (a *analyzer) textBlock(text string, lastUser bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, reminderOpen):
		inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, reminderOpen), reminderClose)
		a.reminder(inner)
	case lastUser && !looksInjected(trimmed):
		a.add(Chunk{Category: CatPrompt, Name: "prompt", Source: "you", Text: text})
	default:
		a.injected(text)
	}
}

func looksInjected(s string) bool {
	for _, m := range markers {
		if m.re.MatchString(s) {
			return true
		}
	}
	return false
}

var contentsOf = regexp.MustCompile(`(?m)^Contents of (.+?) \(([^)]*)\):$`)

func (a *analyzer) reminder(inner string) {
	switch {
	case contentsOf.MatchString(inner):
		for _, s := range splitAtFunc(inner, contentsOf, func(m []string) string { return m[1] }) {
			if s.title == "" {
				a.add(Chunk{Category: CatMemory, Name: "preamble", Source: "built-in", Text: s.body})
				continue
			}
			src, hint := a.res.MemorySource(s.title)
			a.add(Chunk{ID: "memory/" + s.title, Category: CatMemory, Name: a.res.Short(s.title), Source: src, Hint: hint, Text: s.body})
		}
	case regexp.MustCompile(`(?m)^# (userEmail|gitStatus|currentDate)`).MatchString(inner):
		for _, s := range splitAt(inner, h1) {
			name := s.title
			if name == "" {
				name = "preamble"
			}
			hint := ""
			if name == "gitStatus" {
				hint = "set includeGitInstructions: false (or env CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS) to drop it"
			}
			a.add(Chunk{ID: "session/" + name, Category: CatSession, Name: name, Source: "built-in", Hint: hint, Text: s.body})
		}
	case strings.Contains(inner, "Attribution for git commits"):
		a.add(Chunk{ID: "session/attribution", Category: CatSession, Name: "commit attribution", Source: "built-in", Hint: "settings.json attribution / includeCoAuthoredBy", Text: inner})
	default:
		a.add(Chunk{Category: CatSession, Name: "reminder: " + firstLine(inner, 50), Source: "built-in", Text: inner})
	}
}

// markers split the large injected message into top-level sections.
type marker struct {
	kind string
	re   *regexp.Regexp
}

var markers = []marker{
	{"hook", regexp.MustCompile(`(?m)^\S+ hook additional context:`)},
	{"env", regexp.MustCompile(`(?m)^# Environment\s*$`)},
	{"deferred", regexp.MustCompile(`(?m)^The following deferred tools are now available`)},
	{"newtools", regexp.MustCompile(`(?m)^The (following )?tools (below )?(just became|are now) available`)},
	{"agents", regexp.MustCompile(`(?m)^Available agent types for the Agent tool:`)},
	{"skills", regexp.MustCompile(`(?m)^The following skills are available`)},
	{"mcp", regexp.MustCompile(`(?m)^# MCP Server Instructions`)},
}

type section struct {
	kind  string
	title string
	body  string
}

func (a *analyzer) injected(text string) {
	type hit struct {
		pos  int
		kind string
	}
	var hits []hit
	for _, m := range markers {
		for _, loc := range m.re.FindAllStringIndex(text, -1) {
			hits = append(hits, hit{loc[0], m.kind})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	if len(hits) == 0 || hits[0].pos > 0 {
		end := len(text)
		if len(hits) > 0 {
			end = hits[0].pos
		}
		a.generic(text[:end])
	}
	for i, h := range hits {
		end := len(text)
		if i+1 < len(hits) {
			end = hits[i+1].pos
		}
		body := text[h.pos:end]
		switch h.kind {
		case "hook":
			a.hook(body)
		case "env":
			a.add(Chunk{ID: "session/environment", Category: CatSession, Name: "Environment (message)", Source: "built-in",
				Hint: "built-in: working directory, platform and model facts", Text: body})
		case "deferred":
			a.deferred(body)
		case "newtools":
			a.newTools(body)
		case "agents":
			a.list(body, CatAgents)
		case "skills":
			a.list(body, CatSkills)
		case "mcp":
			a.mcpInstructions(body)
		}
	}
}

// generic splits unrecognised text on "# " headings.
func (a *analyzer) generic(text string) {
	for _, s := range splitAt(text, h1) {
		name := s.title
		if name == "" {
			name = firstLine(s.body, 60)
		}
		a.add(Chunk{Category: CatOther, Name: name, Source: "built-in", Text: s.body})
	}
}

var pluginRef = regexp.MustCompile(`\b([a-z][a-z0-9-]*):[a-z][a-z0-9-]*\b`)

func (a *analyzer) hook(body string) {
	event, _, _ := strings.Cut(body, " ")
	a.add(Chunk{ID: "hook/" + event, Category: CatHooks, Name: event + " hook", Source: "hook (source unknown)",
		Hint: "find it in settings.json hooks or in an enabled plugin's hooks/hooks.json", Text: body})
}

// attributeHooks runs after the skill catalogue is parsed. The candidates are the configured
// hooks for the chunk's event (plugin hooks.json files, settings files). One candidate settles
// it; with several, a plugin's hook usually mentions the plugin's own skills as
// "<plugin>:<skill>", so the most-mentioned known plugin wins.
func (a *analyzer) attributeHooks() {
	for i, c := range a.chunks {
		if c.Category != CatHooks {
			continue
		}
		event, _, _ := strings.Cut(c.Name, " ")
		cands := a.res.HookSources(event)
		counts := map[string]int{}
		for _, m := range pluginRef.FindAllStringSubmatch(c.Text, -1) {
			if a.plugins[m[1]] {
				counts[m[1]]++
			}
		}
		best, n := "", 0
		for p, k := range counts {
			if k > n || (k == n && p < best) {
				best, n = p, k
			}
		}
		switch {
		case best != "":
			src := "plugin " + best
			a.chunks[i].Source = src
			a.chunks[i].Hint = fmt.Sprintf("attributed by its mentions of %s: skills; disable the plugin (/plugin, or enabledPlugins \"%s@…\": false)", best, best)
			if others := without(cands, src); len(others) > 0 {
				a.chunks[i].Note = fmt.Sprintf("%s hooks are also configured in %s; their output, if any, is part of this chunk", event, strings.Join(others, ", "))
			}
		case len(cands) == 1:
			a.chunks[i].Source = cands[0]
			a.chunks[i].Hint = fmt.Sprintf("the only %s hook configured; remove it there (a plugin's hooks go with the plugin)", event)
		case len(cands) > 1:
			a.chunks[i].Source = "hook: " + strings.Join(cands, " or ")
			a.chunks[i].Hint = fmt.Sprintf("several %s hooks are configured; disable them one at a time and diff to see which wrote this", event)
		}
	}
}

func without(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

var nameLine = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func (a *analyzer) deferred(body string) {
	a.nameList(body, "deferred", CatDeferred, "deferred tools")
}

// nameList handles a header line followed by one tool name per line, grouping the names by
// source (built-in or MCP server).
func (a *analyzer) nameList(body, idPrefix, cat, label string) {
	header, rest, _ := strings.Cut(body, "\n")
	a.add(Chunk{ID: idPrefix + "/header", Category: cat, Name: label + " preamble", Source: "built-in", Text: header + "\n"})
	groups := map[string][]string{}
	var tail strings.Builder
	for _, l := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(l)
		if nameLine.MatchString(t) {
			src, _ := toolSource(t)
			groups[src] = append(groups[src], t)
			continue
		}
		tail.WriteString(l)
		tail.WriteString("\n")
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		names := groups[k]
		hint := "names only; full schemas load on demand via ToolSearch"
		if server, ok := strings.CutPrefix(k, "MCP "); ok {
			hint = mcpHint(server)
		}
		a.add(Chunk{ID: idPrefix + "/" + k, Category: cat, Name: fmt.Sprintf("%s (%d names)", k, len(names)), Source: k, Hint: hint, Text: strings.Join(names, "\n") + "\n"})
	}
	if strings.TrimSpace(tail.String()) != "" {
		a.generic(tail.String())
	}
}

var functionTag = regexp.MustCompile(`(?s)<function>(\{.*?\})</function>`)

// newTools handles tool schemas announced inside a message (MCP servers that connected after
// startup), one chunk per schema.
func (a *analyzer) newTools(body string) {
	locs := functionTag.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		// Names only; the schemas follow as tool_addition blocks.
		a.nameList(body, "newtools", CatTools, "tools announced in messages")
		return
	}
	a.add(Chunk{ID: "newtools/preamble", Category: CatTools, Name: "tools announced in messages: preamble", Source: "built-in", Text: body[:locs[0][0]]})
	for i, loc := range locs {
		var t struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(body[loc[2]:loc[3]]), &t)
		end := loc[1]
		if i+1 == len(locs) {
			end = len(body)
		}
		src, hint := toolSource(t.Name)
		a.add(Chunk{ID: "tool/" + t.Name, Category: CatTools, Name: t.Name, Source: src, Hint: hint, Text: body[loc[0]:end]})
	}
}

// listItem matches "- name: …" and "- name (alias): …" entries.
var listItem = regexp.MustCompile(`^- ([A-Za-z0-9_][A-Za-z0-9_:.-]*)(?: \([^)]*\))?: `)
var agentEnd = regexp.MustCompile(`\(Tools: [^)]*\)\s*$`)

// list splits the skill or agent catalogue into one chunk per entry. Agent descriptions can
// contain their own "- user: …" example lines, so an agent entry only ends after its
// "(Tools: …)" trailer.
func (a *analyzer) list(body string, cat string) {
	lines := strings.SplitAfter(body, "\n")
	var cur strings.Builder
	curName := ""
	seen := false
	flush := func() {
		if curName == "" {
			part := "preamble"
			if seen {
				part = "trailer"
			}
			a.add(Chunk{ID: strings.ToLower(cat) + "/" + part, Category: cat, Name: "catalogue " + part, Source: "built-in", Text: cur.String()})
		} else {
			a.listEntry(cat, curName, cur.String())
			seen = true
		}
		cur.Reset()
	}
	for _, l := range lines {
		m := listItem.FindStringSubmatch(l)
		closed := curName == "" || cat != CatAgents || agentEnd.MatchString(strings.TrimRight(cur.String(), "\n"))
		if m != nil && closed {
			flush()
			curName = m[1]
		} else if curName != "" && cat == CatAgents && agentEnd.MatchString(strings.TrimRight(cur.String(), "\n")) && strings.TrimSpace(l) != "" {
			// trailing prose after the last agent
			flush()
			curName = ""
		}
		cur.WriteString(l)
	}
	flush()
}

func (a *analyzer) listEntry(cat, name, text string) {
	if cat == CatSkills {
		if p, _, ok := strings.Cut(name, ":"); ok {
			if a.plugins == nil {
				a.plugins = map[string]bool{}
			}
			a.plugins[p] = true
		}
		src, hint := a.res.SkillSource(name)
		a.add(Chunk{ID: "skill/" + name, Category: CatSkills, Name: name, Source: src, Hint: hint, Text: text})
		return
	}
	src, hint := a.res.AgentSource(name)
	a.add(Chunk{ID: "agent/" + name, Category: CatAgents, Name: name, Source: src, Hint: hint, Text: text})
}

var h2 = regexp.MustCompile(`(?m)^## \S.*$`)

func (a *analyzer) mcpInstructions(body string) {
	for _, s := range splitAt(body, h2) {
		if s.title == "" {
			a.add(Chunk{ID: "mcp/preamble", Category: CatMCP, Name: "preamble", Source: "built-in", Text: s.body})
			continue
		}
		a.add(Chunk{ID: "mcp/" + s.title, Category: CatMCP, Name: s.title, Source: "MCP " + s.title, Hint: "server instructions ship with the server: disable it in /mcp or at claude.ai → Connectors", Text: s.body})
	}
}

// markDuplicates notes skills that share a base name, e.g. "tdd" and "mattpocock-skills:tdd".
func (a *analyzer) markDuplicates() {
	byBase := map[string][]int{}
	for i, c := range a.chunks {
		if c.Category != CatSkills || !strings.HasPrefix(c.ID, "skill/") {
			continue
		}
		base := c.Name
		if i := strings.LastIndex(base, ":"); i >= 0 {
			base = base[i+1:]
		}
		byBase[base] = append(byBase[base], i)
	}
	for _, idx := range byBase {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			var others []string
			for _, j := range idx {
				if j != i {
					others = append(others, a.chunks[j].Name)
				}
			}
			a.chunks[i].Note = "duplicate of " + strings.Join(others, ", ")
		}
	}
}

// --- helpers ---------------------------------------------------------------------------------

// splitAt cuts text at each match of re; the matched line becomes the section title (without
// leading #'s). Text before the first match becomes an untitled section.
func splitAt(text string, re *regexp.Regexp) []section {
	return splitAtFunc(text, re, func(m []string) string { return strings.TrimSpace(strings.TrimLeft(m[0], "#")) })
}

func splitAtFunc(text string, re *regexp.Regexp, title func([]string) string) []section {
	locs := re.FindAllStringSubmatchIndex(text, -1)
	var out []section
	if len(locs) == 0 || locs[0][0] > 0 {
		end := len(text)
		if len(locs) > 0 {
			end = locs[0][0]
		}
		if strings.TrimSpace(text[:end]) != "" {
			out = append(out, section{body: text[:end]})
		}
	}
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		m := make([]string, len(loc)/2)
		for k := range m {
			if loc[2*k] >= 0 {
				m[k] = text[loc[2*k]:loc[2*k+1]]
			}
		}
		out = append(out, section{title: title(m), body: text[loc[0]:end]})
	}
	return out
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	l, _, _ := strings.Cut(s, "\n")
	if r := []rune(l); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return l
}
