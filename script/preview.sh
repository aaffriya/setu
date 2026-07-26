#!/bin/sh
# Run the built binary as a throwaway installation for the in-app preview:
# its own port and its own state file under ./tmp, so previewing can never
# disturb a real setup. Every setting is an environment variable now, which is
# why this wrapper exists at all — override any of them when calling it.
set -e

export SETU_PORT="${SETU_PORT:-8091}"
export SETU_TOKEN="${SETU_TOKEN:-CHANGE_ME}"
export SETU_POLL_INTERVAL="${SETU_POLL_INTERVAL:-5s}"
export SETU_STATE_DIR="${SETU_STATE_DIR:-$PWD/tmp/preview-state}"

mkdir -p "$SETU_STATE_DIR"
exec ./bin/setu "$@"
