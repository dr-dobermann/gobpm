module script-task

go 1.25

toolchain go1.25.12

replace github.com/dr-dobermann/gobpm => ../..

replace github.com/dr-dobermann/gobpm/adapters/lua => ../../adapters/lua

require (
	github.com/dr-dobermann/gobpm v0.9.0
	github.com/dr-dobermann/gobpm/adapters/lua v0.0.0-00010101000000-000000000000
)

require github.com/yuin/gopher-lua v1.1.2 // indirect
