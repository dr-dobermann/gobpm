package exec_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/exec"
	"github.com/stretchr/testify/require"
)

func TestDataPath(t *testing.T) {
	t.Run(
		"invalid datapath",
		func(t *testing.T) {
			for _, inv := range []string{
				"",
				"   ",
				"root",
				"root/subpath",
				"/root//subpath",
				"/root/   /subpath",
			} {
				t.Log("[", inv, "]")
				_, err := exec.NewDataPath(inv)
				require.Error(t, err, inv)
			}
		})

	t.Run(
		"drop tail",
		func(t *testing.T) {
			for dp, dt := range map[string]string{
				"/root/sub/subsub": "/root/sub",
				"/root/sub":        "/root",
				"/root":            "/",
			} {
				t.Log(dp, ":", dt)

				d, err := exec.NewDataPath(dp)
				require.NoError(t, err)

				d, err = d.DropTail()
				require.NoError(t, err)
				require.Equal(t, d.String(), dt)
			}

			_, err := exec.DataPath("").DropTail()
			require.Error(t, err)
		})

	t.Run(
		"appand",
		func(t *testing.T) {
			tests := []struct {
				dataPath, appendPath string
				shouldFailed         bool
			}{
				{
					dataPath:   "/",
					appendPath: "root",
				},
				{
					dataPath:   "/root",
					appendPath: "sub",
				},
				{
					dataPath:   "/root",
					appendPath: "sub/subsub",
				},
				{
					dataPath:     "/",
					appendPath:   "/root",
					shouldFailed: true,
				},
				{
					dataPath:     "/",
					appendPath:   "",
					shouldFailed: true,
				},
				{
					dataPath:     "/root",
					appendPath:   "/sub/subsub",
					shouldFailed: true,
				},
			}

			for _, tst := range tests {
				dp, err := exec.NewDataPath(tst.dataPath)
				require.NoError(t, err)

				dp, err = dp.Append(tst.appendPath)
				if tst.shouldFailed {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}

			_, err := exec.DataPath("").Append("sub")
			require.Error(t, err)
		})
}
