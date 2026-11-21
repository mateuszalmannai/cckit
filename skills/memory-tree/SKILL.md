---
name: memory-tree
description: Keep persistent memories as a local tree of markdown files instead of one flat folder. Use whenever writing, updating, recalling, or reorganizing long-term memory — saving a fact the user asked to remember, capturing feedback/preferences/project state, or looking one up. One fact per file, foldered by type, with an auto-generated index.
---

# Memory Tree

Store long-term memory as a **tree of markdown files** under the memory root:

```
<memory-root>/
  MEMORY.md          # generated index — one line per memory, loaded each session
  user/              # who the user is: role, expertise, preferences
  feedback/          # guidance on how to work — corrections + confirmed approaches
  project/           # ongoing work, goals, constraints not derivable from code/git
  reference/         # pointers to external resources (URLs, dashboards, tickets)
```

The memory root is the directory the harness names in the system prompt — use that one. To share one tree across every project, set `"autoMemoryDirectory": "~/.claude/memory"` in `~/.claude/settings.json`. It already
exists — write into it directly, no `mkdir` checks. Create the type subfolders
lazily, only when a memory of that type is first saved.

## One fact per file

**Write in Standard Technical English**: short sentences, active voice, present tense, one idea per sentence, no idioms. A memory is re-read months later by
someone who has lost the context it was written in.

Each memory is a single file, `<type>/<short-kebab-slug>.md`:

```markdown
---
name: <short-kebab-case-slug>
description: <one-line summary — used to judge relevance during recall>
metadata:
  type: user | feedback | project | reference
  title: <short title shown in the index>
  hook: <user/feedback only: the few words after the title in the index; defaults to the description>
---

<the fact. For feedback/project, follow with **Why:** and **How to apply:** lines.>
```

Put `title` and `hook` under `metadata`: the harness moves unknown top-level keys there anyway. Without a `title` the index shows the name in sentence case.

- `name` must be unique across the whole tree (it's the link target).
- Link related memories with `[[other-name]]` in the body. Link liberally — a
  `[[name]]` with no file yet is a valid marker of something worth writing later.
- Convert relative dates ("yesterday", "next sprint") to absolute ones before saving.

## Nesting deeper

When one type folder grows a cluster around a topic, nest a subfolder for it
(e.g. `project/website-relaunch/…`). Keep the `name` slug globally unique regardless of
depth; the folder is for humans browsing, `[[links]]` and the index resolve by name.

Move files with `memory mv <name> <type>/<folder> [--name NEW]`, not with `mv`: it renames the frontmatter `name` with the file, rewrites every `[[link]]` to it and rebuilds the index. To convert Claude Code's flat layout (files directly in the root), run `memory migrate --plan > plan.tsv`, fill in each row's project folder and kebab-case name, then `migrate --apply plan.tsv --dry-run` and `--apply plan.tsv`. The tool checks the whole plan before it moves anything.

## The index (MEMORY.md)

`MEMORY.md` at the root is the only file loaded into context each session, so it has a
context budget. Past roughly 100 memories one line per file no longer fits and the tail is
silently truncated — the memories at the end become invisible. The index therefore has two
levels:

**`user/` and `feedback/` keep one line per memory.** They change how the assistant works
without being asked, so they must load every session.

```markdown
## feedback
### Working agreements — `feedback/working-agreements/`
- [Title](feedback/working-agreements/slug.md) — hook
```

**`project/` and `reference/` keep one line per leaf folder**, listing the titles it holds
and how many:

```markdown
## project
### Website relaunch — `project/website-relaunch/` (4)
Launch date · Content freeze · Who signs off on copy · Staging URL
```

Recall from a leaf: read the titles, then `grep -h "^description:" <leaf>/*.md` for the
one-line summaries, and open only the file you need. Do **not** add an index file per leaf —
the descriptions already live in each file's frontmatter, and a second copy drifts.

Never put memory content in `MEMORY.md` — only pointers.

**Generate the index, never edit it by hand.** In the same turn you add, move, or delete a memory file, run the `memory` tool, then check:

```bash
memory index   # rebuild MEMORY.md from the frontmatter; keeps the text above the first `## `
memory check   # exit 1 on broken frontmatter, duplicate names, a stale or oversized index
```

`memory` comes from cckit (`bin/memory`, installed on `PATH` by `install.sh`). For people it also has `memory ls` (fzf picker), `show`, `edit`, `new <type>/<leaf>/<slug>`, `rm`. Use `--root DIR` (or `MEMORY_ROOT`) when the tree is not `~/.claude/memory`. `edit` and `new` open an editor, so as an agent write the file directly and run `index`.

## Before saving

1. **Search first.** Grep the tree for an existing file on the same fact; update it
   in place rather than creating a near-duplicate.
2. **Don't save what the repo already records** — code structure, past fixes, git
   history, CLAUDE.md. If asked to remember one of those, ask what was non-obvious and
   save that instead.
3. **Delete when wrong.** Remove files that turn out to be false, then run `index`.

## Recalling

Read `MEMORY.md`, pick the relevant lines by their hooks/descriptions, then open only
those files. Treat recalled facts as background context reflecting what was true when
written — if a memory names a file, function, or flag, verify it still exists before
acting on it.
