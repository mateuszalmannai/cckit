# ctxscope

Shows what Claude Code puts into the context window before you type anything, attributes every token to the file, plugin, MCP server or setting that put it there, and diffs two captures so you can see what a config change saved. `/context` gives totals per category; ctxscope names the individual tool, skill, agent, hook or memory file and tells you how to remove it.

Install with `go install github.com/mateuszalmannai/cckit/ctxscope@latest`, or run `go build .` here. Tested with Claude Code 2.1.330: ctxscope reads Claude Code's request format, which can change between versions, so attribution may need an update after a Claude Code release.

## How it works

1. **Capture:** a local reverse proxy sits in front of `api.anthropic.com`, and Claude Code is pointed at it with `ANTHROPIC_BASE_URL`. The proxy records the `/v1/messages` request body and the `usage` block of the response; headers (and with them your credentials) are forwarded but never written to disk.
2. **Attribute:** the request is split into chunks, one per system-prompt section, tool schema, skill entry, agent entry, memory file (`Contents of …`), hook injection, MCP server instruction block, group of deferred tool names, and session context item. Each chunk gets a source and, where one exists, a hint on how to remove it:
    - skills and agents are looked up in `.claude/skills|agents` of the project (cwd up to `$HOME`) and in `~/.claude`, and catalogue entries that are commands in `.claude/commands/<name>.md` of either; a `plugin:name` entry belongs to that plugin, except `anthropic-skills:name` found in `~/.claude/skills/synced`, which is a claude.ai skill; an unprefixed entry found in an enabled plugin's `skills/` or `commands/` belongs to that plugin; everything else is built-in;
    - enabled plugins are read from `~/.claude/plugins/installed_plugins.json` and the `enabledPlugins` of the user and project settings files;
    - a hook chunk is matched to the configured hooks for its event (an enabled plugin's `hooks/hooks.json`, or the `hooks` of a settings file): one candidate settles it; with several, the plugin whose `plugin:skill` names the text mentions most wins and the chunk notes the others, and without such mentions the source lists all candidates;
    - untitled system-prompt text is labelled `identity` (a block that is only the "You are …" line) or `intro` (text before a block's first heading), so headless and interactive captures pair up in a diff;
    - `mcp__server__tool` names map to their MCP server, and `claude_ai_*` servers are flagged as claude.ai connectors.
3. **Count:** each chunk's tokens are estimated from its text (about 3.8 ASCII characters per token, one per CJK character), then all estimates are scaled so they sum to the exact input-token total the API reported. Per-chunk numbers are therefore approximate, the total is exact.
4. **Diff:** chunks are matched by a stable ID. A chunk whose text did not change counts as unchanged even when calibration re-weighted it, and the remainder is reported as calibration residual. A pair whose source changed (a local skill replaced by the built-in of the same name) counts as one chunk removed and one added, so each source's total moves by what it really lost or gained.

Attribution reads the filesystem at analysis time. `reanalyze` therefore attributes an old capture against today's config: a skill deleted since then becomes built-in. Re-analyse a copy (`CTXSCOPE_HOME=<dir>` with a copy of the snapshots) when old snapshots must keep their original sources.

## Commands

```bash
ctxscope capture [-label L] [-dir D] [-prompt P] [-- claude args...]   # headless: claude -p through the proxy
ctxscope proxy [-label L]                    # interactive: prints the ANTHROPIC_BASE_URL=… claude command
ctxscope import [-label L] [-total N] request.json
ctxscope list
ctxscope report [-by category|source|chunk] [-n N] [-hints] [snapshot]
ctxscope diff [-min N] [-n N] [A] [B]        # default: the two newest
ctxscope show [snapshot] <chunk-id-or-name>
ctxscope reanalyze [snapshot|all]            # re-run attribution after updating ctxscope
ctxscope tui [snapshot]
```

Snapshot references: `latest` (default), `latest~1` or `~1`, the index from `list`, or a unique part of a name.

## Trying a change without editing your config

Everything after `--` goes to `claude`, so a variant can be measured for one run without touching `~/.claude/settings.json`:

```bash
ctxscope capture -label baseline
ctxscope capture -label no-superpowers -- --settings '{"enabledPlugins":{"superpowers@claude-plugins-official":false}}'
ctxscope capture -label no-workflow -- --disallowedTools Workflow
ctxscope diff
```

## Headless vs interactive

`capture` runs `claude -p`, which loads a smaller context than an interactive session: claude.ai connectors and some tools (Artifact, AskUserQuestion, SendFeedback) are left out. In one measurement (2026-11-20, a personal notes repository), headless was 36.2k tokens and interactive 56.2k, with the Artifact tool alone at 13.4k. Use `proxy` to measure what an interactive session really carries:

```bash
ctxscope proxy -label interactive
# in another terminal, in the project to measure:
ANTHROPIC_BASE_URL=http://127.0.0.1:<port> claude
# send "hi"; the first full request is saved; quit claude, then Ctrl-C the proxy
```

## TUI

`ctxscope tui` opens a snapshot list, then the chunks of the chosen snapshot. The chunk screen starts with a strip coloured by category and a legend, and `t` opens a treemap: one rectangle per chunk, source or category, sized by tokens and coloured by category. Rectangles too small to label are folded, first into one per category (`+72 Skills`), then into a grey `+N more`.

| Key | Where | Action |
| --- | --- | --- |
| `/` | any list | fuzzy filter (esc clears) |
| `enter` | snapshots | open the snapshot's chunks |
| `space` | snapshots | mark up to two snapshots |
| `d` | snapshots | diff the two marked snapshots, or the selected one against the one before it |
| `t` | snapshots, chunks | treemap of the selected snapshot, or of the current chunk list and its drill-down |
| arrows / `hjkl`, `tab` | treemap | move to the neighbouring rectangle, or the next one by size |
| `g` | treemap | group by chunk → source → category |
| `enter` | treemap | open a chunk, or the chunks behind a source, category or fold |
| `g` | chunks | group by chunk → source → category |
| `enter` | chunks | open a chunk (full text, source, removal hint), or drill into a group |
| `esc` / `backspace` | any | back |
| `q` | any | quit |

## Where data lives

Snapshots are written to `$CTXSCOPE_HOME/snapshots` (default `~/.local/share/ctxscope/snapshots`) with mode 0600. They contain your full starting context (CLAUDE.md files, memory index, git status, email), so keep them out of any repository you share or sync.

## Caveats

- **Format drift:** the splitter keys on section markers Claude Code writes today (`Contents of …`, `The following skills are available…`, `# MCP Server Instructions` and so on). Unrecognised text is never dropped; it lands in an `Other` chunk, which is the signal to update `internal/analyze`.
- **Cost:** each capture is one real API request (a short "hi"), billed like any other.
- **Hints are leads:** a hint names the setting most likely to remove a chunk; confirm with a capture and `diff`.
