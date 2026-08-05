"""MkDocs build hook (SRD-080 FR-3).

Rewrites relative Markdown links whose target lies outside docs/ — or inside
a path excluded from the site (camunda7/) — to absolute GitHub URLs, so the
published site never carries a link the reader cannot follow. Links that stay
inside docs/ pass through untouched, and the Markdown sources are never
modified: in-repo browsing, Obsidian, and the linkcheck gate see the original
relative links.
"""

import posixpath
import re

REPO_URL = "https://github.com/dr-dobermann/gobpm/tree/master"

_NON_SLUG = re.compile(r"[^\w\- ]")


def github_slugify(value, separator):
    """Heading → anchor id, the way GitHub does it.

    Every in-repo anchor was authored against GitHub's slugs (lowercase;
    punctuation dropped without collapsing the spaces it leaves, so
    "Part 6 — Extending gobpm" → "part-6--extending-gobpm"). Python-Markdown's
    default slugify collapses those runs to a single separator, which would
    break each such anchor on the site. Wired into the toc extension in
    mkdocs.yml.
    """
    return _NON_SLUG.sub("", value.lower()).replace(" ", separator)

# Site-excluded paths (relative to docs/); keep in sync with exclude_docs
# in mkdocs.yml.
EXCLUDED = ("camunda7/",)

# [text](target) and ![alt](target); target captured up to ')' or whitespace
# (optional "title" tolerated). Fenced/inline code spans never form links in
# the rendered page, so a rare false hit inside a code block is harmless: the
# rewritten text is still displayed verbatim.
_LINK = re.compile(r"(!?\[[^\]]*\]\()([^)\s]+)((?:\s+\"[^\"]*\")?\))")


def _rewrite_target(target: str, page_dir: str) -> str:
    if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith(("#", "/")):
        return target  # absolute URL, mailto:, in-page anchor, or site-absolute

    path, _, fragment = target.partition("#")
    resolved = posixpath.normpath(posixpath.join(page_dir, path))

    outside = resolved.startswith("..")
    excluded = any(resolved.startswith(p) for p in EXCLUDED)
    if not (outside or excluded):
        return target

    # Repo-relative path: docs/<page_dir>/<path>, normalized. For a target
    # climbing to the repo root this reduces to "." → the repo URL itself.
    repo_path = posixpath.normpath(posixpath.join("docs", page_dir, path))
    if repo_path.startswith(".."):
        return target  # escapes the repository — leave as-is for --strict to flag
    url = REPO_URL if repo_path == "." else f"{REPO_URL}/{repo_path}"

    return f"{url}#{fragment}" if fragment else url


def on_page_markdown(markdown, page, config, files):
    page_dir = posixpath.dirname(page.file.src_uri)

    def repl(m):
        return f"{m.group(1)}{_rewrite_target(m.group(2), page_dir)}{m.group(3)}"

    return _LINK.sub(repl, markdown)
