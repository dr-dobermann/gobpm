#!/usr/bin/env bash
# check-tool-pins.sh (FIX-025) — detect newer releases of the pinned go-install
# dev tools and, if any, bump them in BOTH the Makefile and the CI workflow
# (Dependabot can't see version strings embedded in build tooling). Writes
# `changed=true|false` to $GITHUB_OUTPUT and, when it bumped anything, the PR
# body to /tmp/pin-notes.md. A no-op run exits 0. Run from the repo root.
#
# Every tool is a Go module, so `proxy.golang.org/<module>/@latest` (the highest
# release, pre-releases excluded) is the one datasource — no GitHub API, no rate
# limits.
set -euo pipefail

makefile="Makefile"
workflow=".github/workflows/check.yml"
notes="/tmp/pin-notes.md"

# name | Makefile *_VERSION var | go module
tools=(
	"mockery|MOCKERY_VERSION|github.com/vektra/mockery/v3"
	"golangci-lint|GOLANGCI_VERSION|github.com/golangci/golangci-lint/v2"
	"govulncheck|GOVULNCHECK_VERSION|golang.org/x/vuln"
	"covercheck|COVERCHECK_VERSION|github.com/dr-dobermann/covercheck"
)

latest_version() { # module
	curl -fsSL "https://proxy.golang.org/${1}/@latest" |
		grep -oE '"Version": *"[^"]*"' | head -1 | cut -d'"' -f4
}

current_pin() { # *_VERSION var
	grep -oE "^${1}[[:space:]]*:=[[:space:]]*\S+" "$makefile" | awk '{print $NF}'
}

: >"$notes"
changed=0

for entry in "${tools[@]}"; do
	IFS='|' read -r name var mod <<<"$entry"
	cur="$(current_pin "$var")"
	new="$(latest_version "$mod" || true)"

	if [ -z "$new" ]; then
		echo "WARN: could not resolve latest for $name ($mod)" >&2
		continue
	fi
	if [ "$cur" = "$new" ]; then
		echo "$name: $cur (current)"
		continue
	fi
	# only ever bump forward (a pin ahead of the resolved latest is left alone)
	if [ "$(printf '%s\n%s\n' "$cur" "$new" | sort -V | tail -1)" != "$new" ]; then
		echo "$name: pinned $cur is ahead of resolved $new — skipping"
		continue
	fi

	echo "$name: $cur -> $new (bumping)"
	esc="$(printf '%s' "$cur" | sed 's/[.]/\\./g')" # each pin's version is unique
	sed -i "s/${esc}/${new}/g" "$makefile" "$workflow"
	echo "- \`$name\`: \`$cur\` → \`$new\`" >>"$notes"
	changed=1
done

out="${GITHUB_OUTPUT:-/dev/stdout}"
if [ "$changed" -eq 1 ]; then
	echo "changed=true" >>"$out"
	echo "--- pin updates ---"
	cat "$notes"
else
	echo "changed=false" >>"$out"
	echo "all tool pins current"
fi
