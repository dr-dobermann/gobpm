# How to contribute

I'm really glad you're reading this, because we need volunteer developers and testers to help this project come to fruition.

Here are some important resources:

  * Mailing list: Join our [developer list](http://groups.google.com/group/gobpm/)
  * Bugs are tracking as [GitHub Issues](https://docs.github.com/en/issues/tracking-your-work-with-issues/about-issues). Before reporting an issue please check the [Issue list](https://github.com/dr-dobermann/gobpm/issues) if there is no one issue similar to yours.</br>Please give as much information about the issue as possible: 
  
    - Description
    - Step to reproduce
      - 1.
      - 2.
    - Expected behavior
    - Actual behavior

## Cross-module development

`gobpm` is a multi-module monorepo (per [ADR-003](docs/design/ADR-003-module-layout.md)):
the core library at the repo root, the `runtime/` submodule, each `adapters/*`
its own module, and each example its own module.

For editing across modules (e.g., core + `runtime/` + `adapters/sqlite/`), use Go
workspace mode:

    go work init . ./runtime ./adapters/sqlite ./examples/basic-process ./examples/simple-timer ./examples/timer-event

This creates a `go.work` file that lets Go resolve cross-module imports to local
working-tree copies. The file is gitignored — it is developer-machine state, not
committed.

Without workspace mode, cross-module edits require `replace` directives in `go.mod`
files, which are easy to forget to revert. Workspace mode is the recommended path.

## Local CI parity

Before pushing, run `make ci` locally. It runs the same checks GitHub Actions runs:

  * `make mock-check` — regenerates the mocks and fails if the result differs from
    what is committed; run `make gen_mock_files` after changing an interface
  * `make link-check` — fails on any relative link in the repository's Markdown
    that does not resolve, reporting `file:line`. The checker is the pinned
    external tool `dr-dobermann/linkcheck`; `make tools` installs it
  * `make tidy-check-all` — verifies every module's `go.mod` and `go.sum` are tidy
  * `make lint-all-modules` — runs `golangci-lint` (with the depguard
    import-direction rules from ADR-003 §4.4) and `gofmt` on every module; use
    `golangci-lint fmt` to fix formatting, not plain `gofmt`, since golangci
    simplifies by default and the two otherwise disagree
  * `make build-all` — builds every module
  * `make consumer-smoke` — builds a throwaway module against the library, so a
    break in the public API is caught even when the repo's own packages compile
  * `make test-all` — runs `go test -race` on every module; core also generates
    `coverage.txt` for Codecov, excluding generated code and `examples/`
  * `make cover-check` — the blocking diff-coverage gate (SRD-002): the lines your
    change adds or modifies must be covered at or above `COVER_MIN`. It judges only
    changed lines, so the untouched-code backlog never blocks you — but note it
    diffs the **committed** branch against `origin/master`, so run it after
    committing; on a dirty tree it measures only what is already in
  * `make vuln` — runs `govulncheck` against all modules

The CI workflow (`.github/workflows/check.yml`) calls these same Makefile targets
so there is no drift between local and CI behavior.

## Submitting changes

Please send a [GitHub Pull Request to gobpm](https://github.com/dr-dobermann/gobpm/compare) with a clear list of what you've done (read more about [pull requests](http://help.github.com/pull-requests/)). We can always use more test coverage. Please follow our coding conventions (below) and make sure all of your commits are atomic (one feature per commit).

Always write a clear log message for your commits. One-line messages are fine for small changes, but bigger changes should look like this:

    $ git commit -m "A brief summary of the commit
    > 
    > A paragraph describing what changed and its impact."

## Coding conventions

Since the gobpm native language is Go there is nothing better than using of coding convention described in [Effective Go](https://golang.org/doc/effective_go) article.

Thanks,
Ruslan Gabitov
