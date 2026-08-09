#!/usr/bin/env bash
# ci-run.sh — run a gate phase as numbered, announced, timed steps, and record
# the phase's own verdict (FIX-039).
#
# The gate used to report pass/fail through an exit code and progress through
# unstructured log text, which made every downstream observer responsible for
# information only the gate holds. That produced a masked exit code, a lost
# verdict when a wrapper was killed, a fabricated pass from a truncating filter,
# a liveness check that matched its own command line, and a step classifier that
# ran backwards — five failures, none of them in the gate. So the gate says what
# it is doing and records how it ended, and nobody downstream re-derives either.
#
# Usage: ci-run.sh <phase-label> <step>...
# Env:   MAKE (defaults to make), CI_DIR (defaults to .ci), CI_HEARTBEAT (30)

# The whole body is wrapped in a braced group that ends with `exit`, so bash
# parses it in ONE pass. Without that, bash reads a script incrementally as it
# runs, and editing this file mid-run shifts the byte offsets under the running
# interpreter — which happened on 2026-08-08: a 14/14 green run died at the last
# line with `syntax error near unexpected token done`, and its verdict was lost.
# A run's integrity must not depend on nobody touching the file while it runs.
{
set -uo pipefail

phase="${1:?phase label required}"
shift
steps=("$@")
[ "${#steps[@]}" -gt 0 ] || { echo "ci-run: no steps given" >&2; exit 64; }

MAKE="${MAKE:-make}"
CI_DIR="${CI_DIR:-.ci}"
CI_HEARTBEAT="${CI_HEARTBEAT:-30}"
status="$CI_DIR/last-run.json"
timings="$CI_DIR/timings.tsv"

# Refuse to record anything during a DRY RUN. GNU make executes a recipe line
# containing $(MAKE) even under -n — so `make -n ci` reached this script — and
# passes the dry-run flag down in MAKEFLAGS, so every step returned instantly
# and successfully. The result was a verdict reading "PASS, 14 steps, 1s" for a
# run that executed nothing: a forged pass produced by the machinery built to
# make passes unforgeable. The Makefile deliberately KEEPS $(MAKE) in these
# recipes — hiding it stops -n from recursing but also closes make's jobserver,
# so `make -j4 ci` warns "jobserver unavailable" on every step (measured) — and
# this guard is what makes the dry run harmless instead.
# ${MAKEFLAGS:-} because `set -u` makes a bare expansion fatal when the script
# is run directly rather than through make — which its own usage line invites,
# and which an independent review caught crashing on line one.
#
# Only the FIRST word carries short flags; long options are separate words
# (verified: `s --no-print-directory`), so matching the first word alone cannot
# be fooled by a long option that happens to contain an n.
case "${MAKEFLAGS:-}" in
	n*|*[[:space:]]n*)
		echo "ci-run: dry run (-n) — executing nothing and recording no verdict"
		exit 0
		;;
esac

mkdir -p "$CI_DIR"

# A stale verdict must never be mistaken for this run's, so it goes before the
# first step rather than being overwritten at the end.
rm -f "$status"

total="${#steps[@]}"
started_at="$(date -Is)"
run_start="$(date +%s)"
head_sha="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
durations=""

# human renders seconds as 5s / 1m38s / 1h02m — a bare "98" is a number the
# reader has to convert, and the point of this file is not making people work.
human() {
	local s="$1"
	if   [ "$s" -lt 60 ];   then printf '%ds' "$s"
	elif [ "$s" -lt 3600 ]; then printf '%dm%02ds' "$((s / 60))" "$((s % 60))"
	else printf '%dh%02dm' "$((s / 3600))" "$(((s % 3600) / 60))"
	fi
}

# typical_for reports the median of a step's recent runs, or nothing at all when
# there is no history. Nothing is deliberate: a borrowed or invented estimate is
# worse than none, because the reader stops trusting the number instead of the
# clock.
typical_for() {
	local step="$1" n
	[ -f "$timings" ] || return 0
	local vals
	vals="$(awk -F'\t' -v s="$step" '$1 == s { print $2 }' "$timings" | tail -5 | sort -n)"
	[ -n "$vals" ] || return 0
	n="$(printf '%s\n' "$vals" | wc -l)"
	printf '%s\n' "$vals" | sed -n "$(((n + 1) / 2))p"
}

# write_status records the verdict. It is written by the gate, not by the
# caller, so killing the caller cannot lose it; and it carries the HEAD sha and
# start time so an older file cannot be read as this run's.
write_status() {
	# Disarm first: a signal arriving mid-write would otherwise re-enter this
	# function through the trap and interleave two writers on one file.
	trap '' INT TERM

	local code="$1" failed="$2" note="$3"
	local elapsed=$(($(date +%s) - run_start))
	{
		printf '{\n'
		printf '  "phase": "%s",\n' "$phase"
		printf '  "exit": %s,\n' "$code"
		printf '  "verdict": "%s",\n' "$([ "$code" -eq 0 ] && echo PASS || echo FAIL)"
		printf '  "failed_step": "%s",\n' "$failed"
		printf '  "note": "%s",\n' "$note"
		printf '  "head": "%s",\n' "$head_sha"
		printf '  "started": "%s",\n' "$started_at"
		printf '  "finished": "%s",\n' "$(date -Is)"
		printf '  "seconds": %s,\n' "$elapsed"
		printf '  "steps": {%s}\n' "$durations"
		printf '}\n'
	} > "$status.tmp"

	# Atomic: a reader polling this file must see either the previous verdict or
	# the complete new one, never a half-written JSON document. rename(2) is the
	# only way to promise that.
	mv -f "$status.tmp" "$status"
}

# A signal that can be caught is recorded AND taken down with its children.
# Without the group kill the driver dies while `go test -race` runs on — an
# orphan holding the CPU and the coverage file, invisible to whoever thinks
# they stopped the gate (observed while testing this script).
#
# SIGKILL cannot be caught: the status file then stays absent, which is the
# correct reading of a run that did not finish, but the children DO survive. A
# caller that must be certain should signal the process group
# (`kill -TERM -<pgid>`), which this handler makes sufficient.
on_signal() {
	trap '' INT TERM                 # do not re-enter on our own group signal

	# The heartbeat lives in its OWN process group (setsid, so that killing it
	# takes its sleep along), which means the group kill below does NOT reach
	# it. Without this line an interrupted run leaves a heartbeat printing into
	# a log nobody is producing any more — measured, after the two fixes were
	# applied separately and their interaction was not.
	[ -n "${beat:-}" ] && kill -TERM -"$beat" 2>/dev/null

	kill -TERM 0 2>/dev/null         # make, its shell, go test — the whole group
	write_status 130 "$current_step" "interrupted by a signal"
	echo "[$phase] INTERRUPTED during $current_step — verdict recorded as FAIL"
	exit 130
}

current_step="(none)"
trap on_signal INT TERM

i=0
for step in "${steps[@]}"; do
	i=$((i + 1))
	current_step="$step"

	typical="$(typical_for "$step")"
	if [ -n "$typical" ]; then
		hint=" (typically $(human "$typical"))"
	else
		hint=" (no local baseline yet)"
	fi

	printf '[%2d/%d] %-22s started %s%s\n' \
		"$i" "$total" "$step" "$(date +%H:%M:%S)" "$hint"

	step_start="$(date +%s)"

	# The heartbeat is what makes a silence readable. test-core emits one
	# ::group:: line and then nothing until a whole race suite finishes, so a
	# working step, a deadlocked step and a dead step look identical.
	# setsid puts the heartbeat in its own process group, so killing it below
	# takes its `sleep` with it. Killing the subshell alone leaves the sleep
	# orphaned until its timer expires — up to one stray process per step.
	setsid bash -c '
		while sleep "$1"; do
			e=$(( $(date +%s) - $5 ))
			if [ "$e" -lt 60 ]; then el="${e}s"; else
				el="$((e / 60))m$(printf %02d $((e % 60)))s"; fi
			printf "[%2d/%d] %-22s … %s elapsed\n" "$2" "$3" "$4" "$el"
		done
	' _ "$CI_HEARTBEAT" "$i" "$total" "$step" "$step_start" &
	beat=$!

	# The step runs in the BACKGROUND and is waited on, because bash defers a
	# trap until the current FOREGROUND command finishes. A SIGTERM arriving
	# during a multi-minute `make test-core` sat pending for the rest of that
	# step, so the handler ran long after whoever sent the signal had concluded
	# nothing happened — measured 2026-08-08, and the reason T-7 exists. `wait`
	# is interruptible, so the trap fires at once.
	"$MAKE" "$step" &
	step_pid=$!
	wait "$step_pid"
	code=$?

	kill -TERM -"$beat" 2>/dev/null || kill "$beat" 2>/dev/null
	wait "$beat" 2>/dev/null

	elapsed=$(($(date +%s) - step_start))
	durations="$durations$([ -n "$durations" ] && echo ,) \"$step\": $elapsed"

	if [ "$code" -ne 0 ]; then
		printf '[%2d/%d] %-22s FAILED after %s (exit %d)\n' \
			"$i" "$total" "$step" "$(human "$elapsed")" "$code"
		write_status "$code" "$step" "step failed"
		echo "[$phase] verdict: FAIL at $step — see $status"
		exit "$code"
	fi

	printf '[%2d/%d] %-22s ok %s\n' "$i" "$total" "$step" "$(human "$elapsed")"

	# Only a PASSING step's duration becomes a baseline: a step that died after
	# two seconds is not evidence that it takes two seconds.
	printf '%s\t%s\n' "$step" "$elapsed" >> "$timings"
done

write_status 0 "" ""
printf '[%s] verdict: PASS — %d/%d steps in %s (see %s)\n' \
	"$phase" "$total" "$total" "$(human "$(($(date +%s) - run_start))")" "$status"

exit 0
}
