#!/usr/bin/env bash
# Shared Go formatter for Make and PR lint wrappers.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

cd "$REPO_ROOT"

FORMAT_FLAG=-l
if [[ $# -eq 1 && "$1" == --write ]]; then
    FORMAT_FLAG=-w
elif [[ $# -ne 0 ]]; then
    printf 'usage: %s [--write]\n' "$0" >&2
    exit 2
fi

format_source() {
    git ls-files -z --cached --others --exclude-standard -- '*.go' | {
        files=()
        while IFS= read -r -d '' path; do
            # Match gofmt's directory walk: skip deleted files and symlinks.
            if [[ -f "$path" && ! -L "$path" ]]; then
                files+=("./$path")
            fi
            # Bound argument size for native Windows gofmt as well as Unix.
            if [[ ${#files[@]} -eq 32 ]]; then
                gofmt "$FORMAT_FLAG" "${files[@]}" || exit "$?"
                files=()
            fi
        done
        if [[ ${#files[@]} -gt 0 ]]; then
            gofmt "$FORMAT_FLAG" "${files[@]}" || exit "$?"
        fi
    }
}

if [[ "$FORMAT_FLAG" == -w ]]; then
    printf 'Formatting Go files...\n'
else
    printf 'Checking Go formatting...\n'
fi
if UNFORMATTED="$(format_source)"; then
    :
else
    status=$?
    if [[ "$FORMAT_FLAG" == -w ]]; then
        printf 'gofmt failed while formatting\n' >&2
    else
        printf 'gofmt failed while checking formatting\n' >&2
    fi
    exit "$status"
fi

if [[ "$FORMAT_FLAG" == -w ]]; then
    printf 'Done\n'
    exit 0
fi

if [[ -n "$UNFORMATTED" ]]; then
    printf 'The following files are not properly formatted:\n'
    printf '%s\n' "$UNFORMATTED"
    printf '\n'
    printf "Run 'make fmt' to fix formatting\n"
    exit 1
fi

printf 'All Go files are properly formatted\n'
