package service

import "github.com/dr-dobermann/gobpm/pkg/model/data"

// DataReader is the narrow, public, read-only data surface a Go operation
// receives (ADR-011 v.4 §2.6). It exposes the data plane's addressable reads
// (ADR-010 v.2 §2.7) and nothing else — no writes, no lifecycle, no events. A
// runtime variable is read by its explicit path "RUNTIME/<var>", a process
// property by its plain name.
type DataReader interface {
	// GetData resolves a datum by name (a plain name reads the default scope;
	// "SOURCE/addr" reads a named data source).
	GetData(name string) (data.Data, error)

	// GetDataByID resolves a datum by its ItemDefinition id.
	GetDataByID(id string) (data.Data, error)

	// GetSources lists the named data sources reachable through the reader.
	GetSources() []string

	// List enumerates variable names at the default scope (empty path) or a
	// named source.
	List(path string) ([]string, error)
}
