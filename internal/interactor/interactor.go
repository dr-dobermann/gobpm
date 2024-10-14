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

	// Registrator is an interface implemented by objects
	// which could register interaction nodes on runtime.
	Registrator interface {
		foundation.Identifyer

		Register(iror Interactor) (chan data.Data, error)
	}

	// RenderProvider is an interface of objects which could control
	// user interaction with Renderer objects and returns the results
	// of the interaction.
	RenderController interface {
		Registrator

		// AttachUser gets single user focus, checks its roles against
		// registered renderers and returns list of renderers which are
		// allowed to call by the user.
		AttachUser(userRoles []string) ([]hi.Renderer, error)

		Interact(
			iror Interactor,
			render hi.Renderer,
			performer hi.HumanPerformer) error
	}
)
