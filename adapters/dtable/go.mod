module github.com/dr-dobermann/gobpm/adapters/dtable

go 1.25

toolchain go1.25.13

replace github.com/dr-dobermann/gobpm => ../..

require (
	github.com/dr-dobermann/gobpm v0.9.0
	github.com/stretchr/testify v1.12.1
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect
