package interactor

import (
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

type (
	// Interactor is an interface implemented by Nodes, which has ability
	// to interact with RenderProviders -- WEB, console or other services
	// calling Render method of the Renderer.
	Interactor interface {
		foundation.Identifyer

		Renderers() []hi.Renderer

		Roles() []*hi.ResourceRole

		Outputs() []*common.ResourceParameter
	}

	// RenderProvider is an interface of objectsh which could control
	// user interaction with Renderer objects and returs the results
	// of the interaction.
	RenderProvider interface {
		foundation.Identifyer

		RegisterInteractor(iror Interactor) (chan data.Data, error)
	}
)
