module decision-table

go 1.25

toolchain go1.25.13

replace github.com/dr-dobermann/gobpm => ../..

replace github.com/dr-dobermann/gobpm/adapters/dtable => ../../adapters/dtable

require (
	github.com/dr-dobermann/gobpm v0.9.0
	github.com/dr-dobermann/gobpm/adapters/dtable v0.0.0-00010101000000-000000000000
)
