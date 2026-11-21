#!/usr/bin/env bash
# Install cckit: build ctxscope, link memory and secret, link the memory-tree skill.
#
#   ./install.sh [--force] [--no-skill]
#
#   PREFIX      where the tools go             (default ~/.local/bin)
#   SKILLS_DIR  where Claude Code finds skills (default ~/.claude/skills)
#
# Safe to run again after `git pull`: it rebuilds ctxscope and leaves correct links alone. A file or
# link that is already at a target and is not cckit's is skipped, unless --force, which moves it to
# <target>.bak-<timestamp> first. settings.json is never edited; the hook snippet is printed instead.
set -euo pipefail

repo="$(cd "$(dirname "$0")" && pwd)"
prefix="${PREFIX:-$HOME/.local/bin}"
skills="${SKILLS_DIR:-$HOME/.claude/skills}"
force=0 skill=1 skipped=0

for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
    --no-skill) skill=0 ;;
    -h|--help) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option $arg" >&2; exit 2 ;;
  esac
done

link() {  # link <source> <target>
  local from="$1" to="$2"
  if [[ -L "$to" && "$(readlink "$to")" == "$from" ]]; then
    echo "ok       $to"
    return
  fi
  if [[ -e "$to" || -L "$to" ]]; then
    if [[ $force -eq 0 ]]; then
      echo "skipped  $to exists and is not cckit's (--force replaces it, keeping a .bak copy)"
      skipped=1
      return
    fi
    local bak; bak="$to.bak-$(date +%Y%m%d-%H%M%S)"
    mv "$to" "$bak"
    echo "backup   $to -> $bak"
  fi
  mkdir -p "$(dirname "$to")"
  ln -s "$from" "$to"
  echo "linked   $to -> $from"
}

[[ "$(uname)" == Darwin ]] || echo "note     secret needs macOS; memory and ctxscope work elsewhere too"
mkdir -p "$prefix"

if command -v go >/dev/null; then
  target="$prefix/ctxscope"
  # a binary built from this module is ours to rebuild; anything else needs --force
  if [[ -e "$target" ]] \
    && ! go version -m "$target" 2>/dev/null | grep -q 'path[[:space:]]*github.com/mateuszalmannai/cckit/ctxscope$'; then
    if [[ $force -eq 0 ]]; then
      echo "skipped  $target exists and was not built by cckit (--force replaces it, keeping a .bak copy)"
      skipped=1
    else
      bak="$target.bak-$(date +%Y%m%d-%H%M%S)"; mv "$target" "$bak"; echo "backup   $target -> $bak"
    fi
  fi
  if [[ ! -e "$target" ]] || go version -m "$target" 2>/dev/null | grep -q 'path[[:space:]]*github.com/mateuszalmannai/cckit/ctxscope$'; then
    (cd "$repo/ctxscope" && go build -o "$target" .)
    echo "built    $target"
  fi
else
  echo "skipped  ctxscope: Go is not installed (brew install go)"
  skipped=1
fi

link "$repo/bin/memory" "$prefix/memory"
link "$repo/bin/secret" "$prefix/secret"
[[ $skill -eq 0 ]] || link "$repo/skills/memory-tree" "$skills/memory-tree"

case ":$PATH:" in
  *":$prefix:"*) ;;
  *) echo "note     $prefix is not on PATH; add it in your shell profile" ;;
esac

cat <<EOF

To keep MEMORY.md current without relying on the model, add these hooks to ~/.claude/settings.json
(see the memory section of README.md):

  "hooks": {
    "PostToolUse": [
      { "matcher": "Write|Edit", "hooks": [{ "type": "command", "command": "\"$prefix/memory\" hook post-tool-use", "timeout": 10 }] }
    ],
    "SessionStart": [
      { "hooks": [{ "type": "command", "command": "\"$prefix/memory\" hook session-start", "timeout": 10 }] }
    ]
  }
EOF
exit "$skipped"
