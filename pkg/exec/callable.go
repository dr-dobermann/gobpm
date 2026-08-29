package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

const errorClass = "EXEC_ERRORS"

// CallableRef is what a Call Activity names: a key, optionally qualified by
// the namespace of the definitions document that declared the callable.
//
// BPMN types `calledElement` as a plain String and fixes no naming convention
// for it, and a reference into another document is meaningful only through
// that document's <import>. So the engine owns no convention either: an
// UNQUALIFIED reference (Namespace == "") names a registry key directly, and a
// qualified one names a callable the host must map, because only the host
// knows what it registered the other document's definitions under.
type CallableRef struct {
	// Namespace is the target document's namespace URI, empty for a
	// reference inside the calling document.
	Namespace string

	// Key is the callable's local name — the process key for an unqualified
	// reference.
	Key string
}

// CallableResolver turns a callable reference into the key the engine's
// process registry serves.
//
// It is a HOST contract, supplied once per engine, and it is consulted at
// call time for every call — including unqualified ones, so a host that maps
// keys (a tenant prefix, a naming convention) sees them all and a resolver can
// be reasoned about from its own code. The engine calls it outside every
// engine lock, because it is host code and may do anything, including calling
// back into the engine.
type CallableResolver interface {
	// ResolveCallable answers the registry key ref maps onto. Returning an
	// error fails the CALL — the caller's activity faults and the error chain
	// catches it at the Call Activity node — never the engine.
	ResolveCallable(ctx context.Context, ref CallableRef) (string, error)
}

// CallableResolverFunc adapts a plain function to CallableResolver, so a host
// with a one-line mapping writes no type (the http.HandlerFunc idiom).
type CallableResolverFunc func(context.Context, CallableRef) (string, error)

// ResolveCallable calls f, refusing a nil function rather than panicking on
// it. CallableResolverFunc(nil) is a NON-nil CallableResolver — the interface
// check just below this file relies on exactly that — so it passes
// thresher.WithCallableResolver's nil guard and would otherwise surface as a
// panic inside the engine at the first call, arbitrarily far from the mistake.
func (f CallableResolverFunc) ResolveCallable(
	ctx context.Context, ref CallableRef,
) (string, error) {
	if f == nil {
		return "", errs.New(
			errs.M("CallableResolverFunc: the function is nil and cannot "+
				"resolve callable %q — pass a function, or omit "+
				"thresher.WithCallableResolver to keep the default resolver",
				ref.Key),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return f(ctx, ref)
}

// DefaultCallableResolver is the resolver the engine uses when the host
// supplies none: the unqualified case is exact — the reference IS the key —
// and a qualified one is refused by name.
//
// Refusing is the only honest answer available to it. Taking the local part
// would silently call whatever the host happens to have registered under a
// name that merely coincides, and inventing a composite key would be a naming
// convention the standard declines to give. A host that never references
// another document therefore configures nothing, and one that does is told
// exactly which namespace it must teach the engine about.
type DefaultCallableResolver struct{}

// ResolveCallable answers ref.Key for an unqualified reference and refuses a
// qualified one, naming the namespace and the key.
func (DefaultCallableResolver) ResolveCallable(
	_ context.Context, ref CallableRef,
) (string, error) {
	// An unqualified reference IS the key it answers, so an empty one would
	// answer an empty key: the registry lookup would then fail naming nothing,
	// and the caller would have to guess which end was wrong.
	if ref.Key == "" {
		return "", errs.New(
			errs.M("DefaultCallableResolver: ref.Key is empty and names no "+
				"callable"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if ref.Namespace == "" {
		return ref.Key, nil
	}

	return "", errs.New(
		errs.M("no CallableResolver is configured for namespace %q, which "+
			"callable %q is declared in — supply one with "+
			"thresher.WithCallableResolver to map it onto a registered "+
			"process key", ref.Namespace, ref.Key),
		errs.C(errorClass, errs.ObjectNotFound))
}

// interface checks
var (
	_ CallableResolver = CallableResolverFunc(nil)
	_ CallableResolver = DefaultCallableResolver{}
)
