#!/usr/bin/env bash
# Restore data/app_security_info.json from git history so security info
# doesn't need to be re-collected from scratch. Use when the file was
# overwritten or lost but versions haven't changed (collectors would
# otherwise skip as "up to date").
#
# Usage:
#   ./scripts/restore-security-info.sh [COMMIT]
#
# If COMMIT is omitted, finds the most recent commit where the file
# contained at least one darwin (macOS) entry and restores from that.
# If COMMIT is set (e.g. abc123 or HEAD~5), restores from that commit.
#
# After restoring, commit and push the file. Regenerate index.html if
# needed (e.g. go run generate_html.go).

set -e
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
FILE="data/app_security_info.json"

has_darwin_entries() {
  local f="$1"
  if command -v jq &>/dev/null; then
    jq -e '[.apps[]? | select(.slug | endswith("/darwin"))] | length > 0' "$f" &>/dev/null
  else
    grep -q '"slug".*darwin' "$f"
  fi
}

if [ -n "$1" ]; then
  COMMIT="$1"
  echo "Restoring $FILE from commit: $COMMIT"
  git show "$COMMIT:$FILE" > "$FILE"
  echo "Restored. Review with: git diff $FILE"
  exit 0
fi

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

echo "Finding most recent commit where $FILE had darwin (macOS) entries..."
for commit in $(git log --format=%H -- "$FILE"); do
  git show "$commit:$FILE" > "$TMP" 2>/dev/null || continue
  if has_darwin_entries "$TMP"; then
    echo "Using commit: $commit"
    cp "$TMP" "$FILE"
    echo "Restored. Review with: git diff $FILE"
    exit 0
  fi
done

echo "No commit found where $FILE had darwin entries." >&2
echo "Specify a commit explicitly: $0 <commit>" >&2
exit 1
