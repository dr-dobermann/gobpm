package foundation_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/stretchr/testify/require"
)

func TestBaseElement(t *testing.T) {
	t.Run("no options",
		func(t *testing.T) {
			be, err := foundation.NewBaseElement()

			require.NoError(t, err)
			require.NotEmpty(t, be.ID())
			require.Empty(t, be.Docs())
		})

	t.Run("with_id",
		func(t *testing.T) {
			be := foundation.MustBaseElement(foundation.WithID("test_id"))

			require.Equal(t, "test_id", be.ID())
		})

	t.Run("with_docs",
		func(t *testing.T) {
			be := foundation.MustBaseElement(
				foundation.WithDoc("test_doc1", ""),
				foundation.WithDoc("test_doc2", "text/rtf"))

			require.NotEmpty(t, be.ID())

			docs := be.Docs()
			require.Equal(t, 2, len(be.Docs()))
			require.Equal(t, "test_doc1", docs[0].Text())
			require.Equal(t, "test_doc2", docs[1].Text())
			require.Equal(t, "text/plain", docs[0].Format())
			require.Equal(t, "text/rtf", docs[1].Format())
		})
}

// foreignConfig is an options.Configurator that is not *baseConfig, used to
// drive BaseOption.Apply down its type-casting error branch.
type foreignConfig struct{}

func (foreignConfig) Validate() error { return nil }

func TestMustBaseElementPanics(t *testing.T) {
	// a failing option (blank explicit id) makes the Must form panic.
	require.Panics(t, func() {
		_ = foundation.MustBaseElement(foundation.WithID("  "))
	})
}

func TestBaseOptionApplyForeignConfig(t *testing.T) {
	bo, ok := foundation.WithID("x").(foundation.BaseOption)
	require.True(t, ok)
	require.Error(t, bo.Apply(foreignConfig{}))
}
