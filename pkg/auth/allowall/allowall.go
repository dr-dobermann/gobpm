// Package allowall provides the engine's default AuthorizationProvider, which
// permits every request. The library delegates authorization to the host
// application by default (ADR-002 §4.2/§6); a closed system opts into a
// deny-by-default provider (a future sibling) or a real authorization adapter.
package allowall

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/auth"
)

// Provider authorizes every request.
type Provider struct{}

// New returns an allow-all AuthorizationProvider.
func New() auth.AuthorizationProvider { return Provider{} }

// Authorize always allows.
func (Provider) Authorize(context.Context, auth.Request) error { return nil }

var _ auth.AuthorizationProvider = Provider{}
