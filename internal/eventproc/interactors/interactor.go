package interactors

import (
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

type (
	RenderProcessor interface {
		foundation.Identifyer

		RegisterInteractor(iror Interactor) error
	}

	// Interactor is an interface which implemented by Nodes, which has ability
	// to interact with RenderProviders -- WEB, console or other services which
	// could call Render method of the Renderer.
	Interactor interface {
		Renderers() []hi.Renderer
	}
)
