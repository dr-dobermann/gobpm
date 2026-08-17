module github.com/dr-dobermann/gobpm/adapters/sqlite

go 1.25.0

toolchain go1.25.13

replace github.com/dr-dobermann/gobpm => ../..

require (
	github.com/dr-dobermann/gobpm v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.11.1
	modernc.org/sqlite v1.56.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
