#!/usr/bin/env zsh
# HAL 9000 launcher
# Works with: zsh, bash, sh, fish (via env)
set -e
cd "${0:A:h}" 2>/dev/null || cd "$(cd "$(dirname "$0")" && pwd)"

# Auto-build if binary missing or sources changed
if [[ ! -f ./hal9000 ]] || [[ -n $(find . -maxdepth 1 -name '*.go' -newer ./hal9000 2>/dev/null) ]]; then
    echo "\033[38;2;204;51;34mBuilding HAL 9000...\033[0m"
    go build -o hal9000 .
    echo "\033[38;2;80;220;80mReady.\033[0m"
fi

exec ./hal9000 "$@"
