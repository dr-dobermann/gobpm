package events_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/stretchr/testify/require"
)

func TestNewMessageEventDefintion(t *testing.T) {
	t.Run(
		"normal",
		func(t *testing.T) {
			med, err := events.NewMessageEventDefintion(
				common.MustMessage("test_message", nil), nil)
			require.NotNil(t, med, "message shouldn't be empty")
			require.NoError(t, err)
		})

	t.Run(
		"empty_msg",
		func(t *testing.T) {
			med, err := events.NewMessageEventDefintion(nil, nil)
			require.Nil(t, med, "message should be nil with empty message")
			require.Error(t, err)
		})
}
