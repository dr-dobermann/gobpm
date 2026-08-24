module github.com/dr-dobermann/gobpm/adapters/lua

go 1.25

toolchain go1.25.13

replace github.com/dr-dobermann/gobpm => ../..

require (
	github.com/dr-dobermann/gobpm v0.9.0
	github.com/stretchr/testify v1.12.1
	github.com/yuin/gopher-lua v1.1.2
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect
