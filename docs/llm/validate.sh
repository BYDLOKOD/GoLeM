#!/usr/bin/env bash
# Validate the llm-docs graph: every relative markdown link must resolve.
# Exit 0 on clean, 1 on any broken link.

set -euo pipefail

cd "$(dirname "$0")"

status=0
checked=0
broken=0

while IFS= read -r -d '' doc; do
    doc_dir="$(dirname "$doc")"

    # Extract each [label](target) - isolate the target column.
    # Skip external links (http/https), mailto, and bare anchors.
    while IFS= read -r target; do
        case "$target" in
            http://*|https://*|mailto:*|'#'*) continue ;;
            '') continue ;;
        esac

        # Strip fragment identifier.
        path="${target%%#*}"
        [ -z "$path" ] && continue

        # Resolve relative to the containing doc.
        resolved="$doc_dir/$path"

        checked=$((checked + 1))
        if [ ! -e "$resolved" ]; then
            echo "BROKEN: $doc -> $target (resolved: $resolved)"
            broken=$((broken + 1))
            status=1
        fi
    done < <(grep -oE '\]\([^)]+\)' "$doc" | sed -E 's/^\]\(([^)]+)\)$/\1/')
done < <(find . -type f -name '*.md' -print0)

if [ "$status" -eq 0 ]; then
    echo "OK: $checked links valid across $(find . -type f -name '*.md' | wc -l) docs"
else
    echo "FAIL: $broken broken links out of $checked checked"
fi

exit $status
