# Changelog

All notable changes to the GoBPM project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.7.0] - 2026-06-28

**Version-line correction — no functional change from v0.1.1.**

The module's tag history carries an abandoned pre-2023 codebase (the
`v0.2.0-prerelease` … `v0.6.x` line, last published `v0.6.3` in 2022). Because
the module proxy serves the **highest** semver tag as "latest", that old code —
not the current ground-up rewrite — was what `pkg.go.dev` displayed, even after
`v0.1.1`. This release renumbers the current code **above** that line so the
proxy and `pkg.go.dev` reflect the actual module.

### Changed
- Version bumped `v0.1.1` → `v0.7.0` to supersede the abandoned `v0.6.x` line on
  the module proxy. The code is identical to `v0.1.1` (the complete 0.1.0 MVP
  element set — see below).

### Removed
- `retract` directive added for `[v0.2.0-prerelease, v0.6.4-prerelease]` — the
  pre-2023 codebase no longer reflects this module's API and should not be
  selected by `go get` or shown as current.

## [v0.1.1] - 2026-06-28

The 0.1.0 MVP element set is complete: the engine executes the high-frequency
BPMN core chosen by real-world usage frequency (SAD-001 §15.3).

### Added
- **Gateways**: Exclusive, Parallel, Inclusive (split + synchronizing OR-join),
  Complex (activation-threshold join), and Event-Based (mid-flow deferred choice
  and event-based instance start).
- **Events**: None start/end; Timer / Message / Signal intermediate catch and
  throw; signal-start instantiation; **Error End Event**; **Terminate End Event**
  (abnormal whole-instance termination).
- **Boundary events**: interrupting and non-interrupting, on Timer / Message /
  Signal / Error triggers, with per-track cancellation of the guarded activity.
- **Error handling**: `BpmnError` propagation and the Error Boundary Event.
- **Tasks**: Service, User, Send, Receive.
- **Messaging**: cross-instance message correlation by conversation keys, and
  event-triggered process instantiation.
- **Process data**: a container-scoped data plane with per-execution frames and
  addressable data sources (the `RUNTIME` provider, path-qualified reads).
- **Observability**: structured `slog` logging, visible by default with an
  explicit opt-out; a startup banner reporting the engine version and build
  revision.

### Changed
- Execution core reworked to a single-writer, per-instance event loop: one
  goroutine owns instance state and tracks communicate through it via events
  (ADR-001 / ADR-017).

### Fixed
- OR-join all-branches-arrive synchronization hang.
- Complex-join recheck race causing spurious gateway abort/hang.
- Non-message broadcast double-fire across concurrent instances sharing a catch.
- Runtime deadlocks in the bundled examples.

### Testing
- Diff-coverage CI gate (`covercheck`); every package now at or above 80%
  statement coverage.

## [v0.1.0] - Initial Development Phase

### Features Implemented
- BPMN v2 compatible BPM engine core
- Event-driven process execution
- Process instance management
- Timer event support
- Comprehensive BPMN model implementation
- Data handling and expression evaluation
- Error handling system
- Monitoring and observability

### Architecture
- Modular package structure
- Clean interfaces and abstractions
- Thread-safe concurrent processing
- Context-based cancellation support
- Extensible event system

### Components
- **Thresher**: Main BPM engine for process orchestration
- **EventHub**: Central event distribution system
- **Model Layer**: Complete BPMN element implementations
- **Instance Management**: Process execution and state management
- **Data Model**: Variable and expression handling
- **Error System**: Structured error handling

---

## Development Guidelines

### Versioning Strategy
- **Major** (X.0.0): Breaking API changes
- **Minor** (0.X.0): New features, backward compatible
- **Patch** (0.0.X): Bug fixes, backward compatible

### Changelog Categories
- **Added**: New features and capabilities
- **Changed**: Changes in existing functionality
- **Deprecated**: Soon-to-be removed features
- **Removed**: Features removed in this version
- **Fixed**: Bug fixes and error corrections
- **Security**: Security vulnerability fixes
- **Performance**: Performance improvements
- **Testing**: Test coverage and quality improvements
- **Documentation**: Documentation updates and additions

### Commit Message Format
Following [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` - New features
- `fix:` - Bug fixes
- `docs:` - Documentation changes
- `test:` - Test improvements
- `refactor:` - Code refactoring
- `perf:` - Performance improvements
- `chore:` - Maintenance tasks

### Breaking Changes
All breaking changes will be clearly documented with:
- **BREAKING CHANGE**: Clear indication in commit message
- Migration guide for updating existing code
- Deprecation warnings in prior minor version when possible
- Detailed explanation of the change and rationale

### Release Process
1. Update CHANGELOG.md with all changes
2. Update version numbers in relevant files
3. Create release tag following semver
4. Generate release notes from changelog
5. Update documentation if needed

### Contributing to Changelog
When contributing:
1. Add your changes to the "Unreleased" section
2. Use appropriate category (Added, Changed, Fixed, etc.)
3. Include issue/PR references where applicable
4. Describe user-facing impact, not internal details
5. Keep entries concise but informative

### Example Entry Format
```markdown
### Added
- Event-driven process execution with Timer support (#123)
- Comprehensive test suite achieving 75%+ coverage (#124)

### Fixed
- **CRITICAL**: Nil pointer dereference in EventHub registration (#125)
- Memory leak in process instance cleanup (#126)

### Changed
- **BREAKING**: EventProcessor interface now requires context parameter (#127)
- Improved error messages for better debugging experience (#128)
```

---

*This changelog is maintained manually alongside development. For detailed commit history, see the Git log.*
