# Changelog

All notable changes to the GoBPM project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Comprehensive test suite for core components
- Modular test structure organized by functionality
- Extensive documentation for event processing system
- README_INDEX.md for organized documentation navigation
- CHANGELOG.md for tracking project changes
- Professional main README with comprehensive project overview

### Changed
- Improved test coverage across multiple packages
- Enhanced error handling and validation
- Better mockery integration for testing

### Fixed
- **CRITICAL**: Fixed nil map assignment bug in EventHub event registration
- **CRITICAL**: Fixed missing map update in EventHub processor management
- Various test stability and reliability improvements

### Testing Improvements
- **pkg/model/artifacts**: 30% → 86.2% coverage (+56.2%)
- **internal/eventproc/eventhub**: 0% → 77.6% coverage (+77.6%)
- **pkg/thresher**: 0% → 68.8% coverage (+68.8%)

### Documentation
- Added comprehensive documentation for event processing interfaces
- Added detailed EventHub implementation documentation
- Created centralized documentation index
- Improved code examples and usage patterns
- Modernized main README with professional presentation
- Added feature highlights, use cases, and architecture overview
- Included quick start guide with practical examples
- Enhanced project status information and roadmap

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
