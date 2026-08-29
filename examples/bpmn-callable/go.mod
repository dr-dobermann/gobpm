module bpmn-callable

go 1.25

toolchain go1.25.13

replace github.com/dr-dobermann/gobpm => ../..

replace github.com/dr-dobermann/gobpm/adapters/lua => ../../adapters/lua

require github.com/dr-dobermann/gobpm v0.9.0
