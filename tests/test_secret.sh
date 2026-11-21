#!/usr/bin/env bash
# Tests for bin/secret against a throwaway keychain; the login Keychain is never touched.
# Run: tests/test_secret.sh   (macOS only)
set -uo pipefail

[[ "$(uname)" == Darwin ]] || { echo "skip: macOS only"; exit 0; }

SECRET="$(cd "$(dirname "$0")/.." && pwd)/bin/secret"
tmp=$(mktemp -d)
kc="$tmp/cckit-test.keychain-db"
pw="cckit-test"
searchlist_before=$(security list-keychains -d user)

cleanup() {
  security delete-keychain "$kc" 2>/dev/null
  rm -rf "$tmp"
  if [[ "$(security list-keychains -d user)" != "$searchlist_before" ]]; then
    echo "WARNING: the keychain search list changed during the tests:" >&2
    security list-keychains -d user >&2
  fi
}
trap cleanup EXIT

security create-keychain -p "$pw" "$kc" || { echo "cannot create test keychain"; exit 1; }
security unlock-keychain -p "$pw" "$kc"
security set-keychain-settings "$kc"   # no auto-lock during the run
export SECRET_KEYCHAIN="$kc" NO_COLOR=1

pass=0 fail=0
check() {  # check <description> <expected> <actual>
  if [[ "$2" == "$3" ]]; then pass=$((pass + 1)); else
    fail=$((fail + 1)); printf 'FAIL %s\n  expected: %q\n  actual:   %q\n' "$1" "$2" "$3"
  fi
}
rc() { "$@" >/dev/null 2>&1; echo $?; }

# set and get, including characters the `security -i` parser needs escaped
# shellcheck disable=SC2016  # the literal $x is part of the test value
tricky='a"b\c d$x '\''q'\'' ü'
printf '%s\n' "$tricky" | "$SECRET" set t-tricky 2>/dev/null
check "get returns the stored value" "$tricky" "$("$SECRET" get t-tricky)"

long=$(printf 'x%.0s' {1..300})
printf '%s\n' "$long" | "$SECRET" set t-long 2>/dev/null
check "values longer than 128 chars are kept whole" "$long" "$("$SECRET" get t-long)"

printf 'new\n' | "$SECRET" set t-long 2>/dev/null
check "set replaces an existing value" "new" "$("$SECRET" get t-long)"

check "empty value is refused" 1 "$(printf '\n' | rc "$SECRET" set t-empty)"
check "missing name is refused" 1 "$(rc "$SECRET" get)"
check "get of a missing item fails" 1 "$(rc "$SECRET" get t-missing)"

# check shows length and prefix, never the whole value
out=$("$SECRET" check t-long 2>&1)
check "check reports length and prefix" "✓ t-long (3 chars, starts new…)" "$out"
check "check fails when one item is missing" 1 "$(rc "$SECRET" check t-long t-missing)"

# list shows only items stored by secret
security add-generic-password -a "$USER" -s t-foreign -w foreign "$kc"
check "list shows managed items only" $'t-long\nt-tricky' "$("$SECRET" list)"

# import --rewrite moves picked export lines and keeps the file's mode
home="$tmp/home"; mkdir "$home"   # apart from the keychain's own files
dot="$home/dotfile.zsh"
cat > "$dot" <<'EOF'
export SOME_API_TOKEN='tok-123'
export PLAIN_SETTING=1
EOF
chmod 600 "$dot"
"$SECRET" import "$dot" --rewrite SOME_API_TOKEN >/dev/null 2>&1
check "import stores the value under the default name" "tok-123" "$("$SECRET" get some-api-token)"
check "import leaves other lines alone" "export PLAIN_SETTING=1" "$(sed -n 2p "$dot")"
check "import keeps the file mode" 600 "$(stat -f '%Lp' "$dot")"
check "rewritten line reads the value back" "tok-123" "$(zsh -fc 'source "$1"; printf %s "$SOME_API_TOKEN"' _ "$dot")"
check "rewrite leaves no temp file" "dotfile.zsh" "$(find "$home" -mindepth 1 -exec basename {} \;)"

"$SECRET" import "$dot" OTHER_VAR=custom >/dev/null 2>&1
check "import warns about a picked variable without an export line" 1 "$(rc "$SECRET" get custom)"

printf 'export ODD_TOKEN=%s\n' "'ü\\x'" > "$dot"
out=$("$SECRET" import "$dot" ODD_TOKEN 2>&1)
check "import stores a non-ASCII value" 'ü\x' "$("$SECRET" get odd-token)"
check "import warns that the generated line would return hex" 1 "$(grep -c 'returns such values as hex' <<<"$out")"

# rm asks first
printf 'n\n' | "$SECRET" rm t-tricky >/dev/null 2>&1
check "rm keeps the item on no" 0 "$(rc "$SECRET" get t-tricky)"
printf 'y\n' | "$SECRET" rm t-tricky >/dev/null 2>&1
check "rm deletes the item on yes" 1 "$(rc "$SECRET" get t-tricky)"

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
