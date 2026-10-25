#!/usr/bin/env bash
# check-tool-pins.sh (FIX-025) — detect newer releases of the pinned dev
# tools and, if any, bump their `*_VERSION` pin in the Makefile (Dependabot
# can't see version strings embedded in build tooling). The Makefile is the
# only place a pin is written: the workflows install through `make`, and this
# script must never edit a workflow, since the GITHUB_TOKEN that pushes its
# bump may not (FIX-042). Writes
# `changed=true|false` to $GITHUB_OUTPUT and, when it bumped anything, the PR
# body to /tmp/pin-notes.md. A no-op run exits 0. Run from the repo root.
#
# Go tools resolve through `proxy.golang.org/<module>/@latest` (the highest
# release, pre-releases excluded) — no GitHub API, no rate limits. The docs
# toolchain (SRD-080) resolves through the equivalent PyPI JSON endpoint,
# marked by a `pypi:` source prefix.
set -euo pipefail

makefile="Makefile"
notes="/tmp/pin-notes.md"

# name | Makefile *_VERSION var | source (go module or pypi:<pkg>)
tools=(
	"mockery|MOCKERY_VERSION|github.com/vektra/mockery/v3"
	"golangci-lint|GOLANGCI_VERSION|github.com/golangci/golangci-lint/v2"
	"govulncheck|GOVULNCHECK_VERSION|golang.org/x/vuln"
	"covercheck|COVERCHECK_VERSION|github.com/dr-dobermann/covercheck"
	"linkcheck|LINKCHECK_VERSION|github.com/dr-dobermann/linkcheck"
	"mkdocs-material|MKDOCS_MATERIAL_VERSION|pypi:mkdocs-material"
	"mkdocs-awesome-nav|MKDOCS_AWESOME_NAV_VERSION|pypi:mkdocs-awesome-nav"
)

latest_version() { # go module
	curl -fsSL "https://proxy.golang.org/${1}/@latest" |
		grep -oE '"Version": *"[^"]*"' | head -1 | cut -d'"' -f4
}

latest_pypi() { # pypi package ("info" and its "version" precede "releases")
	curl -fsSL "https://pypi.org/pypi/${1}/json" |
		grep -oE '"version": *"[^"]*"' | head -1 | cut -d'"' -f4
}

current_pin() { # *_VERSION var
	grep -oE "^${1}[[:space:]]*:=[[:space:]]*\S+" "$makefile" | awk '{print $NF}' ||
		{
			echo "ERROR: $makefile has no '$1 := <version>' line;" \
				"fix it before the bump can run" >&2
			exit 1
		}
}

: >"$notes"
changed=0

for entry in "${tools[@]}"; do
	IFS='|' read -r name var mod <<<"$entry"
	cur="$(current_pin "$var")"
	case "$mod" in
	pypi:*) new="$(latest_pypi "${mod#pypi:}" || true)" ;;
	*) new="$(latest_version "$mod" || true)" ;;
	esac

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
	esc="$(printf '%s' "$cur" | sed 's/[.]/\\./g')"
	# Touch only the pin line itself — prose comments may cite a version
	# string that collides with another tool's pin (the covercheck
	# "-exclude-lines, v0.1.2+" note vs linkcheck's pin).
	sed -i "s/^\(${var}[[:space:]]*:=[[:space:]]*\)${esc}\$/\1${new}/" "$makefile"
	# sed exits 0 when it substitutes nothing, which would record a bump that
	# never happened; lintcfg's TestPinBotCoversEveryPin keeps every pin in
	# the one form the expression above rewrites.
	pinned="^${var}[[:space:]]*:=[[:space:]]*${new//./\\.}\$"
	if ! grep -qE "$pinned" "$makefile"; then
		echo "ERROR: $makefile has no '$var := $cur' line to rewrite;" \
			"$name was not bumped to $new" >&2
		exit 1
	fi
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
