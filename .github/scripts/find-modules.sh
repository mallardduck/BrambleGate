#!/usr/bin/env bash
# find-modules.sh: Discover Go modules in the repository with consistent filtering.
#
# Usage:
#   find-modules.sh              # output go.mod file paths (one per line)
#   find-modules.sh --dirs       # output module directories (one per line)
#   find-modules.sh --json       # output directories as JSON array (for GitHub Actions matrix)
#
# Excluded paths (dependencies, frontend, tooling):
#   - ./web/*          frontend (no Go modules)
#   - ./gotools/*      build tooling (auto-excluded from discovery)

set -euo pipefail

output_format="${1:-paths}"

case "$output_format" in
  paths)
    # Output go.mod file paths (one per line)
    find . -name go.mod \
      -not -path './web/*' \
      -not -path './gotools/*' \
      | sort
    ;;
  --dirs)
    # Output module directories (one per line)
    find . -name go.mod \
      -not -path './web/*' \
      -not -path './gotools/*' \
      -printf '%h\n' | sort
    ;;
  --json)
    # Output directories as JSON array for GitHub Actions matrix
    find . -name go.mod \
      -not -path './web/*' \
      -not -path './gotools/*' \
      -printf '%h\n' | sort | jq -Rsc 'split("\n") | map(select(length > 0))'
    ;;
  *)
    echo "Unknown format: $output_format" >&2
    echo "Valid formats: paths (default), --dirs, --json" >&2
    exit 1
    ;;
esac
