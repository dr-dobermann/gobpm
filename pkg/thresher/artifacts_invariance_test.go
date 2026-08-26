package thresher_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// artifactedProcess builds one and the same process twice: once bare, once
// with artifacts on BOTH containers — an annotation, a group, associations to
// a node and between artifacts on the process, and an annotation with its
// association inside the embedded sub-process. Same nodes, same flows — the
// ONLY difference is the artifacts.
func artifactedProcess(
	t *testing.T, id string, withArtifacts bool,
) *process.Process {
	t.Helper()

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	work, err := activities.NewManualTask("work")
	require.NoError(t, err)

	box, err := activities.NewSubProcess("box")
	require.NoError(t, err)

	innerStart, err := events.NewStartEvent("inner-start")
	require.NoError(t, err)

	innerWork, err := activities.NewManualTask("inner-work")
	require.NoError(t, err)

	innerEnd, err := events.NewEndEvent("inner-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{innerStart, innerWork, innerEnd} {
		require.NoError(t, box.Add(e))
	}

	link(t, innerStart, innerWork)
	link(t, innerWork, innerEnd)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	p, err := process.New(id)
	require.NoError(t, err)

	for _, e := range []flow.Element{start, work, box, end} {
		require.NoError(t, p.Add(e))
	}

	link(t, start, work)
	link(t, work, box)
	link(t, box, end)

	if withArtifacts {
		note := artifacts.MustTextAnnotation("Careful", "",
			foundation.WithID("note"))
		grp := artifacts.MustGroup("critical", foundation.WithID("grp"))

		require.NoError(t, p.AddArtifacts(note, grp,
			artifacts.MustAssociation(work, note, artifacts.None,
				foundation.WithID("a1")),
			artifacts.MustAssociation(note, grp, artifacts.Both,
				foundation.WithID("a2"))))

		boxNote := artifacts.MustTextAnnotation("inner note", "",
			foundation.WithID("box-note"))

		require.NoError(t, box.AddArtifacts(boxNote,
			artifacts.MustAssociation(innerWork, boxNote, artifacts.One,
				foundation.WithID("box-a"))))
	}

	return p
}

// TestArtifactsDoNotAffectExecution — SRD-092 T-13, the load-bearing test of
// this landing.
//
// "Model-only" is a claim about BEHAVIOR, and structure tests cannot prove
// it. The only honest proof is to run the same process twice — once bare,
// once with both containers fully artifacted — and require the executions to
// be indistinguishable.
func TestArtifactsDoNotAffectExecution(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	bare := runToCompletion(t, artifactedProcess(t, "artifacts-bare", false))
	full := runToCompletion(t, artifactedProcess(t, "artifacts-full", true))

	require.Equal(t, bare, full,
		"an artifacted process must complete exactly as the same process "+
			"without artifacts")
}

// TestArtifactsAreNotCloned — SRD-092 T-14: artifacts live on the
// DEFINITION. The per-instance node graph is a clone, and artifacts have no
// business in it; the definition is untouched by a run because an instance
// never received any artifact state.
func TestArtifactsAreNotCloned(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p := artifactedProcess(t, "artifacts-clone", true)
	require.Len(t, p.Artifacts(), 4, "the definition carries them")

	_ = runToCompletion(t, p)

	arts := p.Artifacts()
	require.Len(t, arts, 4)
	require.Equal(t, "note", arts[0].ID())
	require.Equal(t, "grp", arts[1].ID())
}
