package foundation_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func GenerateUUID() string {
	return uuid.New().String()
}

func TestGenerator(t *testing.T) {
	id1 := foundation.GenerateId()
	require.NotEmpty(t, id1)
	// t.Log(id1)

	id2 := foundation.GenerateId()
	require.NotEmpty(t, id2)
	// t.Log(id2)

	require.NotEqual(t, id1, id2)

	// nil generator
	require.Error(t, foundation.SetGenerator(nil))

	// normal generator
	foundation.SetGenerator(
		foundation.GenFunc(GenerateUUID))

	id1 = foundation.GenerateId()
	require.NotEmpty(t, id1)
	// t.Log(id1)

	id2 = foundation.GenerateId()
	require.NotEmpty(t, id2)
	// t.Log(id2)

	require.NotEqual(t, id1, id2)

}
