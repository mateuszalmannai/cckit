# cckit

Small tools for people who run a heavily customised Claude Code on macOS: see what your setup costs in context, keep memory organised, and keep secrets out of dotfiles.

| Tool | What it does |
|---|---|
| `ctxscope` | Captures what Claude Code sends at startup, attributes every token to the file, plugin, MCP server or setting that put it there, and diffs two captures |
| `memory` | Keeps Claude Code's memory folder as a tree of one-fact files, rebuilds and checks the `MEMORY.md` index, moves memories without breaking `[[links]]` |
| `secret` | Stores secrets in the macOS login Keychain instead of shell dotfiles, and moves existing `export` lines into it |

Work in progress: the tools are being moved into this repository. Install steps follow with `install.sh`.

## memory

`bin/memory` is a single Python 3 script with no dependencies. It works on Claude Code's built-in flat memory folder and on a tree of type folders (`user/`, `feedback/`, `project/`, `reference/`, each with topic subfolders), or a mix of both. `memory -h` lists every command.

- **For you:** `memory ls` (an fzf picker), `show`, `edit`, `new`, `rm`, and `mv`, which moves or renames a memory and rewrites every `[[link]]` to it.
- **For the index:** `memory index` rebuilds `MEMORY.md` from the files' frontmatter and `memory check` reports broken frontmatter, duplicate names and an index that is stale or too long to load.
- **For migrating:** `memory migrate --plan > plan.tsv` turns a flat folder into a plan you edit, and `memory migrate --apply plan.tsv` checks the whole plan before it moves anything.

Index titles come from an optional `metadata.title` in each memory, else from the memory's current `MEMORY.md` line, else from its name. The tree is `$MEMORY_ROOT`, else `~/.claude/memory`; to have Claude Code use that one folder in every project, set `"autoMemoryDirectory": "~/.claude/memory"` in `~/.claude/settings.json`.

The `memory-tree` skill in `skills/memory-tree` tells Claude to write memories in this layout and to run `memory index` and `memory check` afterwards. Two hooks in `~/.claude/settings.json` do the same without relying on the model: one rebuilds the index after every write to the memory folder, the other reports problems at session start. Hooks may not see your shell `PATH`, so use the full path:

```json
"hooks": {
  "PostToolUse": [
    { "matcher": "Write|Edit", "hooks": [{ "type": "command", "command": "\"$HOME/.local/bin/memory\" hook post-tool-use", "timeout": 10 }] }
  ],
  "SessionStart": [
    { "hooks": [{ "type": "command", "command": "\"$HOME/.local/bin/memory\" hook session-start", "timeout": 10 }] }
  ]
}
```

Both hooks always exit 0, so a memory problem never blocks a session. `memory status` prints one short line for a status line, such as `🧠 31 · 50/200 ✓`.

## License

MIT, see `LICENSE`.
