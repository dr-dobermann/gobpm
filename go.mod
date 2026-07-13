module github.com/dr-dobermann/gobpm

go 1.25

toolchain go1.25.12

require (
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// The v0.2.0-prerelease … v0.6.x tags are the pre-2023 GoBPM codebase,
// fully replaced by the current ground-up rewrite. They no longer reflect
// this module's architecture or API and must not be selected.
retract [v0.2.0-prerelease, v0.6.4-prerelease]
