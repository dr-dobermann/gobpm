module github.com/dr-dobermann/gobpm

go 1.25

toolchain go1.25.13

require (
	github.com/google/uuid v1.6.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/stretchr/objx v0.5.3 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

// The v0.2.0-prerelease … v0.6.x tags are the pre-2023 GoBPM codebase,
// fully replaced by the current ground-up rewrite. They no longer reflect
// this module's architecture or API and must not be selected.
retract [v0.2.0-prerelease, v0.6.4-prerelease]
