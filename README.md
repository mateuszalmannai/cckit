# cckit

Small tools for people who run a heavily customised Claude Code on macOS: see what your setup costs in context, keep memory organised, and keep secrets out of dotfiles.

| Tool | What it does |
|---|---|
| `ctxscope` | Captures what Claude Code sends at startup, attributes every token to the file, plugin, MCP server or setting that put it there, and diffs two captures |
| `memory` | Keeps Claude Code's memory folder as a tree of one-fact files, rebuilds and checks the `MEMORY.md` index, moves memories without breaking `[[links]]` |
| `secret` | Stores secrets in the macOS login Keychain instead of shell dotfiles, and moves existing `export` lines into it |

Work in progress: the tools are being moved into this repository. Install steps follow with `install.sh`.

## License

MIT, see `LICENSE`.
