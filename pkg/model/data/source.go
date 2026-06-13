package data

// SourceProvider is a read-only named data source registered on the data
// plane (ADR-010 v.2 §2.7). A path-qualified read "SOURCE/addr" selects the
// provider by its segment and dispatches addr — the provider's own address
// space, passed verbatim — to Get. It is public so an embedding application
// can expose its own data (business objects, a JSON document, ...) as a named
// source.
type SourceProvider interface {
	// Get resolves addr within the provider's address space. addr is opaque
	// to the data plane: a plain name, a dotted path, or a JSONPath
	// expression, as the provider defines.
	Get(addr string) (Data, error)

	// Names lists the addresses the provider currently serves (best-effort
	// for an open address space).
	Names() []string
}
