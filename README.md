# cckit

Small tools for people who run a heavily customised Claude Code on macOS: see what your setup costs in context, keep memory organised, and keep secrets out of dotfiles.

| Tool | What it does |
|---|---|
| `ctxscope` | Captures what Claude Code sends at startup, attributes every token to the file, plugin, MCP server or setting that put it there, and diffs two captures |
| `memory` | Keeps Claude Code's memory folder as a tree of one-fact files, rebuilds and checks the `MEMORY.md` index, moves memories without breaking `[[links]]` |
| `secret` | Stores secrets in the macOS login Keychain instead of shell dotfiles, and moves existing `export` lines into it |

## Install

Requirements: macOS (for `secret`), Go 1.23 or newer (for `ctxscope`), Python 3.9 or newer (for `memory`), and optionally `fzf` for `memory ls`.

```sh
git clone https://github.com/mateuszalmannai/cckit.git ~/scripts/cckit
~/scripts/cckit/install.sh
```

`install.sh` builds `ctxscope` and links `memory` and `secret` into `~/.local/bin` (set `PREFIX` for another folder), and links the `memory-tree` skill into `~/.claude/skills` (`SKILLS_DIR`; `--no-skill` leaves it out). It never overwrites anything that is not its own unless you pass `--force`, which keeps a `.bak` copy, and it never edits `settings.json`: it prints the hook snippet for you to add. Run it again after `git pull`; the links pick up changes to `memory` and `secret` at once, and `ctxscope` is rebuilt.

To install only `ctxscope`: `go install github.com/mateuszalmannai/cckit/ctxscope@latest`.

## ctxscope

`ctxscope capture` runs Claude Code once through a local logging proxy and saves what it sent as a snapshot; `ctxscope proxy` does the same for an interactive session. `ctxscope report` lists every token by category, source or chunk, `ctxscope diff` compares two snapshots, so you can measure what a config change saved, and `ctxscope tui` browses a snapshot as a treemap. Snapshots hold your full starting context, so they live in `~/.local/share/ctxscope/snapshots`, outside any repository. Details and the attribution rules are in [`ctxscope/README.md`](ctxscope/README.md).

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

## secret

`bin/secret` is a bash script for macOS that keeps API tokens and passwords in the login Keychain instead of in `export` lines of your shell dotfiles, where every tool that reads your home folder, Claude Code included, can see them.

- **Store and read:** `secret set <name>` asks for the value with a hidden prompt (or reads it from a pipe), `secret get <name>` prints it, `secret copy <name>` puts it on the clipboard.
- **Look without revealing:** `secret check <name>...` shows whether an item exists, its length and its first five characters; `secret list` names the items `secret` has stored.
- **Move existing dotfile secrets:** `secret import ~/.secrets.zsh` offers each `export VAR=value` line, stores the values, and prints lookup lines to replace them with; `--rewrite` replaces them in place, keeping the file's permissions.

In a dotfile, a moved line reads the value when the shell starts:

```sh
export SOME_API_TOKEN="$(security find-generic-password -s some-api-token -w 2>/dev/null)"
```

`security -w` prints values that contain a backslash or a non-ASCII character as hex. `secret get` decodes them, and `secret import` warns when a value needs `$(secret get <name>)` instead of the generated line.

Values reach `security` on stdin, never on the command line, so they do not show up in `ps` or your shell history. Set `SECRET_KEYCHAIN` to a keychain file to use that instead of the login Keychain; `tests/test_secret.sh` does this with a throwaway keychain.

## Contributing

Run the checks that CI runs: `go vet ./...` and `go test ./...` in `ctxscope/`, `python3 -m unittest discover -s tests -p 'test_*.py'`, `tests/test_secret.sh` (it uses a throwaway keychain), and `shellcheck` on the shell scripts. The pre-commit hook in `.githooks` runs gitleaks and refuses commits whose author email is not a GitHub noreply address; enable it with `git config core.hooksPath .githooks`.

## License

MIT, see `LICENSE`.
