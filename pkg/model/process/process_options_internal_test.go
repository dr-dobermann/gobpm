package process

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

// TestProcessConfigAddNilRejected covers the nil guards of AddRole/AddProperty.
// The public WithRoles/WithProperties options filter nils before calling them,
// so the error branches are only reachable from inside the package.
func TestProcessConfigAddNilRejected(t *testing.T) {
	pc := &processConfig{
		roles: map[string]*hi.ResourceRole{},
		props: map[string]*data.Property{},
	}

	require.Error(t, pc.AddRole(nil))
	require.Error(t, pc.AddProperty(nil))
}
