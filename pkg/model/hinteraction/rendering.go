package human_interaction

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// BPMN User Tasks need to be rendered on user interfaces like forms clients,
// portlets, etc. The Rendering element provides an extensible mechanism for
// specifying UI renderings for User Tasks (Task UI). The element is optional.
// One or more rendering methods can be provided in a Task definition. A User
// Task can be deployed on any compliant implementation, irrespective of the
// fact whether the implementation supports specified rendering methods or not.
// The Rendering element is the extension point for renderings. Things like
// language considerations are opaque for the Rendering element because the
// rendering applications typically provide Multilanguage support. Where this
// is not the case, providers of certain rendering types can decide to extend
// the rendering type in order to provide language information for a given
// rendering. The content of the rendering element is not defined by this
// International Standard.
type Renderer interface {
	foundation.Identifyer
	foundation.Namer

	Render(data.Source) ([]data.Data, error)
}
