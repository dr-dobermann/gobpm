#!/usr/bin/env bash
# run-examples.sh — execute every example module end-to-end, in parallel, and
# report each one as its own folded group in the original order (FIX-029,
# SRD-094 M5).
#
# The sweep used to be a serial loop: 49 modules, each `go run .` under a
# timeout, one after another. The examples are independent processes, so they
# run N at a time here; what parallelism would otherwise cost — interleaved
# logs, a `::group::` fold torn open by another example's lines, a failure
# buried mid-stream — is avoided by buffering each example's output to its own
# file and printing the groups only when everything has finished, in the order
# the modules were given. A failed example prints its log inside its group;
# a passing one prints nothing but the group markers, as before.
#
# Usage: run-examples.sh <example-dir>...
# Env:   GO (go), EXAMPLE_TIMEOUT (timeout|gtimeout), EXAMPLE_RUN_TIMEOUT (90s),
#        EXAMPLE_JOBS (CPU count, capped at 8)

# One-pass parse, the ci-run.sh precedent: editing this file mid-run must not
# shift the running interpreter's byte offsets.
{
set -uo pipefail

[ "$#" -gt 0 ] || { echo "run-examples: no example dirs given" >&2; exit 64; }

GO="${GO:-go}"
EXAMPLE_TIMEOUT="${EXAMPLE_TIMEOUT:-timeout}"
EXAMPLE_RUN_TIMEOUT="${EXAMPLE_RUN_TIMEOUT:-90s}"

if [ -z "${EXAMPLE_JOBS:-}" ]; then
	cpus="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"
	EXAMPLE_JOBS="$cpus"
	[ "$EXAMPLE_JOBS" -gt 8 ] && EXAMPLE_JOBS=8
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# A module dir maps to one log and one status file; the slug keeps them flat.
slug() { printf '%s' "$1" | tr '/.' '__'; }

# run_one is what xargs fans out: the example's combined output to its log,
# its exit status to its status file. Never exits non-zero itself, so xargs
# keeps the rest of the batch running and the summary below decides.
run_one() {
	dir="$1"
	s="$(slug "$dir")"
	(cd "$dir" && "$EXAMPLE_TIMEOUT" "$EXAMPLE_RUN_TIMEOUT" "$GO" run . </dev/null) \
		>"$work/$s.log" 2>&1
	echo "$?" >"$work/$s.status"
}
export -f run_one slug
export work GO EXAMPLE_TIMEOUT EXAMPLE_RUN_TIMEOUT

printf '%s\n' "$@" | xargs -P "$EXAMPLE_JOBS" -I{} bash -c 'run_one "$1"' _ {}

ok=0
failed=0
for dir in "$@"; do
	s="$(slug "$dir")"
	status="$(cat "$work/$s.status" 2>/dev/null || echo "?")"
	echo "::group::run $dir"
	if [ "$status" = "0" ]; then
		ok=$((ok + 1))
	else
		failed=$((failed + 1))
		cat "$work/$s.log"
		if [ "$status" = "124" ]; then
			echo "run-examples: $dir timed out after $EXAMPLE_RUN_TIMEOUT" >&2
		else
			echo "run-examples: $dir exited $status" >&2
		fi
	fi
	echo "::endgroup::"
done

echo "run-examples: $ok ok, $failed failed (jobs=$EXAMPLE_JOBS)"
[ "$failed" -eq 0 ]
exit
}
