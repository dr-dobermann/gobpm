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

`gobpm` is a multi-module monorepo (per [ADR-003](doc/design/ADR-003-module-layout.md)):
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

  * `make tidy-check-all` — verifies every module's `go.mod` and `go.sum` are tidy
  * `make lint-all-modules` — runs `golangci-lint` (with the depguard
    import-direction rules from ADR-003 §4.4) on every module
  * `make build-all` — builds every module
  * `make test-all` — runs `go test -race` on every module; core also generates
    `coverage.txt` for Codecov
  * `make vuln` — runs `govulncheck` against the core module

The CI workflow (`.github/workflows/check.yml`) calls these same Makefile targets
so there is no drift between local and CI behavior.

## Submitting changes

Please send a [GitHub Pull Request to opengovernment](https://github.com/dr-dobermann/gobpm/compare) with a clear list of what you've done (read more about [pull requests](http://help.github.com/pull-requests/)). We can always use more test coverage. Please follow our coding conventions (below) and make sure all of your commits are atomic (one feature per commit).

Always write a clear log message for your commits. One-line messages are fine for small changes, but bigger changes should look like this:

    $ git commit -m "A brief summary of the commit
    > 
    > A paragraph describing what changed and its impact."

## Coding conventions

Since the gobpm native language is Go there is nothing better than using of coding convention described in [Effective Go](https://golang.org/doc/effective_go) article.

Thanks,
Ruslan Gabitov
