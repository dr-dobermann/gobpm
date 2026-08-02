package hinteraction_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
	"github.com/stretchr/testify/require"
)

// TestRoleKindAuthorizes pins which levels of BPMN's Performer chain grant human
// authorization. Only the two human kinds do: a bare ResourceRole or a Performer
// may name a machine or an organization, which is why BPMN 2.0 introduced
// HumanPerformer beside the generic role (§10.3.4.1, ADR-020 v.3 §2.5.4).
func TestRoleKindAuthorizes(t *testing.T) {
	tests := []struct {
		kind hinteraction.RoleKind
		name string
		want bool
	}{
		{name: "bare resource role", kind: hinteraction.RoleResource, want: false},
		{name: "generic performer", kind: hinteraction.RolePerformer, want: false},
		{
			name: "human performer",
			kind: hinteraction.RoleHumanPerformer,
			want: true,
		},
		{
			name: "potential owner",
			kind: hinteraction.RolePotentialOwner,
			want: true,
		},
		{name: "unknown kind", kind: hinteraction.RoleKind("Nonesuch"), want: false},
		{name: "zero kind", kind: hinteraction.RoleKind(""), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.kind.Authorizes())
		})
	}
}
