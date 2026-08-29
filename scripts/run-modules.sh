#!/usr/bin/env bash
# run-modules.sh — run one command in every module, in parallel, and report
# each as its own folded group in the order the modules were given.
#
# The per-module sweeps used to be serial `for dir in $(MODULES)` loops. That
# is invisible for the six core modules and expensive for the fifty-two
# examples: examples-lint alone measured 209s of a 506s gate, four times what
# every other example step costs together, because fifty-two golangci-lint
# runs waited on each other for no reason. The modules are independent, so
# they run N at a time here.
#
# The output discipline is run-examples.sh's, deliberately — one idiom, not
# two. Each module's combined output goes to its own file and the groups print
# only when everything has finished, in the given order, so a failure is never
# interleaved with a neighbour's log and a `::group::` fold is never torn open
# by another module's lines. A failed module prints its log inside its group; a
# passing one prints nothing but the markers, exactly as the serial loop did.
#
# NOT used for the race tests (test-all). Those are already CPU-saturating and
# TEST_CPUS pins their budget to what the CI runner has; running several at
# once would oversubscribe the box and make the timing-sensitive suites — the
# ones that flake for their own reasons — flakier still.
#
# Usage: run-modules.sh <label> <command> <module-dir>...
#          label   names the step in the group markers ("lint", "build", …)
#          command is run with `bash -c` inside each module directory
# Env:   MODULE_JOBS   how many at once (CPU count, capped at 8)
#        MODULE_ORDER  unset = one flat batch; "deps" = dependency waves, for
#                      a command that WRITES what a sibling module reads (see
#                      the wave scheduler below)
#
# Written for bash 3.2, which is what macOS ships — no associative arrays.

# One-pass parse, the ci-run.sh precedent: editing this file mid-run must not
# shift the running interpreter's byte offsets.
{
set -uo pipefail

# An empty module list is a FAILURE, not a silent pass. A sweep that reports
# success over nothing is exactly the local no-op the parity rules exist to
# prevent — the caller derives $(MODULES) from a find, and a find that stopped
# matching should go red where someone sees it.
[ "$#" -ge 3 ] || {
	echo "run-modules: usage: run-modules.sh <label> <command> <dir>..." >&2
	echo "run-modules: (a run over zero modules is a failure, not a pass)" >&2
	exit 64
}

label="$1"
cmd="$2"
shift 2

if [ -z "${MODULE_JOBS:-}" ]; then
	cpus="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"
	MODULE_JOBS="$cpus"
	[ "$MODULE_JOBS" -gt 8 ] && MODULE_JOBS=8
fi

# Without set -e (the summary must run even when a job failed) the one command
# whose failure would misdirect every worker's output needs its own check: an
# empty $work would send the logs to /<slug>.log.
work="$(mktemp -d)" || exit 1
if [ -z "$work" ] || [ ! -d "$work" ]; then
	echo "run-modules: mktemp -d failed" >&2
	exit 1
fi
trap 'rm -rf "$work"' EXIT

# A module dir maps to one log and one status file; the slug keeps them flat.
slug() { printf '%s' "$1" | tr '/.' '__'; }

# run_one is what xargs fans out: the module's combined output to its log, its
# exit status to its status file. Never exits non-zero itself, so xargs keeps
# the rest of the batch running and the summary below decides.
run_one() {
	dir="$1"
	s="$(slug "$dir")"
	(cd "$dir" && bash -c "$cmd" </dev/null) >"$work/$s.log" 2>&1
	echo "$?" >"$work/$s.status"
}
export -f run_one slug
export work cmd

# run_batch runs one set of modules concurrently; non-zero if any failed.
run_batch() {
	printf '%s\n' "$@" |
		xargs -P "$MODULE_JOBS" -I{} bash -c 'run_one "$1"' _ {}
	for d in "$@"; do
		[ "$(cat "$work/$(slug "$d").status" 2>/dev/null || echo 1)" = "0" ] ||
			return 1
	done
	return 0
}

# --- the wave scheduler (MODULE_ORDER=deps) --------------------------------
#
# `go mod tidy` WRITES go.mod/go.sum, and modules in the same sweep can be
# linked by a `replace` — the four adapters replace the root. A flat batch
# would have the root rewriting the files the adapters read: cmd/go locks
# those files, so the exposure is not a corrupt write but a drift check
# resolving against whichever snapshot won the race, and a check that can
# answer differently on two runs of the same tree is worth nothing.
#
# So a writing sweep runs in dependency waves: everything whose in-sweep
# `replace` targets are already done goes in the next wave, together. The root
# and runtime go first, the four adapters follow as one wave. It is a
# topological sort by level, derived from the go.mod files themselves rather
# than hardcoded, so a replace edge added tomorrow is ordered correctly
# without anyone remembering this file exists.
#
# Only modules in THIS sweep constrain the order: an example replacing
# ../../adapters/lua does not wait on it during examples-tidy, because that
# sweep never writes it. That is why all 52 examples still go at once.

# build_dep_graph — resolve every in-sweep replace edge in ONE awk pass, into
# $work/deps as "<dir><TAB> <dep> <dep> ". Doing it with shell subshells
# instead — a `cd && pwd` per module per candidate — cost seven seconds on
# fifty-two modules and made the tidy sweep SLOWER than the serial loop it
# replaced: the scheduling was correct and the accounting still lost. Paths are
# normalised textually rather than by resolving them on disk, which is what
# lets the whole graph come out of a single process.
build_dep_graph() {
	_files=""
	for _m in $ALL_MODULES; do
		[ -f "$_m/go.mod" ] && _files="$_files $_m/go.mod"
	done
	# shellcheck disable=SC2086
	awk '
	function norm(path,   segs, i, n, out, k, up, s) {
		n = split(path, segs, "/")
		k = 0; up = 0
		for (i = 1; i <= n; i++) {
			if (segs[i] == "." || segs[i] == "") continue
			if (segs[i] == "..") { if (k > 0) k--; else up++; continue }
			out[++k] = segs[i]
		}
		# An edge climbing above the tree keeps its ".." rather than
		# clamping to "/", where it would collide with the repo root.
		s = ""
		for (i = 1; i <= up; i++) s = s "/.."
		for (i = 1; i <= k; i++) s = s "/" out[i]
		return (s == "" ? "/" : s)
	}
	FNR == 1 {
		dir = FILENAME; sub(/\/go\.mod$/, "", dir)
		mods[norm(dir)] = dir; order[++nm] = dir
	}
	/=>/ {
		n = split($0, parts, "=>"); if (n < 2) next
		rhs = parts[2]; sub(/^[ \t]+/, "", rhs); split(rhs, a, " ")
		# A version on the right-hand side is a registry replace; only a
		# local path can name a module this sweep is also writing.
		if (a[1] !~ /^[.\/]/) next
		edges[dir] = edges[dir] " " norm(dir "/" a[1])
	}
	END {
		for (i = 1; i <= nm; i++) {
			d = order[i]; out = ""
			n = split(edges[d], e, " ")
			for (j = 1; j <= n; j++)
				if (e[j] in mods && mods[e[j]] != d)
					out = out " " mods[e[j]]
			printf "%s\t%s \n", d, out
		}
	}' $_files >"$work/deps"

	# Read it back into parallel arrays — indexed, because bash 3.2 (what
	# macOS ships) has no associative ones — so the wave loop below spawns
	# nothing at all.
	MODS=(); DEPS=()
	while IFS="$(printf '\t')" read -r _d _dl; do
		MODS[${#MODS[@]}]="$_d"
		DEPS[${#DEPS[@]}]="$_dl"
	done <"$work/deps"
}

# deps_of <dir> — the sweep's own modules that <dir> replaces.
deps_of() {
	_i=0
	while [ "$_i" -lt "${#MODS[@]}" ]; do
		if [ "${MODS[$_i]}" = "$1" ]; then printf '%s' "${DEPS[$_i]}"; return; fi
		_i=$((_i + 1))
	done
}

ALL_MODULES="$*"
failed_early=0

if [ "${MODULE_ORDER:-}" = "deps" ]; then
	build_dep_graph
	remaining="$ALL_MODULES"
	done_mods=" "
	while [ -n "$(printf '%s' "$remaining" | tr -d ' ')" ]; do
		wave=""
		rest=""
		for d in $remaining; do
			ready=1
			for dep in $(deps_of "$d"); do
				case "$done_mods" in
				*" $dep "*) ;;
				*) ready=0 ;;
				esac
			done
			if [ "$ready" = "1" ]; then wave="$wave $d"; else rest="$rest $d"; fi
		done
		# No module became ready: a replace cycle. Nothing sane to schedule,
		# and silently dropping the rest would be the false pass this script
		# exists to refuse — run them together and let the command speak.
		if [ -z "$(printf '%s' "$wave" | tr -d ' ')" ]; then
			echo "run-modules: $label — replace cycle among:$rest" >&2
			echo "run-modules: scheduling them together; fix the cycle" >&2
			wave="$rest"
			rest=""
		fi
		# shellcheck disable=SC2086
		if ! run_batch $wave; then
			# A dependent would run against a broken provider and report
			# noise. Leave the rest unrun, and say so rather than imply they
			# passed.
			for d in $rest; do echo "skip" >"$work/$(slug "$d").status"; done
			failed_early=1
			break
		fi
		done_mods="$done_mods$(printf '%s ' $wave)"
		remaining="$rest"
	done
else
	run_batch "$@"
fi

ok=0
failed=0
skipped=0
for dir in "$@"; do
	s="$(slug "$dir")"
	status="$(cat "$work/$s.status" 2>/dev/null || echo "?")"
	echo "::group::$label $dir"
	case "$status" in
	0)
		ok=$((ok + 1))
		;;
	skip)
		skipped=$((skipped + 1))
		# stdout, not stderr: the two streams are separately buffered in a CI
		# log, and this line has to land INSIDE the fold it explains.
		echo "run-modules: $label $dir not run — a module it depends on failed"
		;;
	*)
		failed=$((failed + 1))
		cat "$work/$s.log" 2>/dev/null
		echo "run-modules: $label $dir exited $status"
		;;
	esac
	echo "::endgroup::"
done

# The summary is printed for a failing run only. A passing sweep already says
# what it did through its group markers, and the serial loop it replaces said
# nothing extra — a per-step line here would show up in every gate log for
# five steps and tell a reader nothing they did not have.
if [ "$failed" -ne 0 ] || [ "$failed_early" -ne 0 ]; then
	echo "run-modules: $label — $ok ok, $failed failed, $skipped not run" \
		"(jobs=$MODULE_JOBS)" >&2
fi
[ "$failed" -eq 0 ] && [ "$failed_early" -eq 0 ]
exit
}
