package foundation

import "github.com/dr-dobermann/gobpm/pkg/helpers"

type ID struct {
	id string
}

func newID(id string) *ID {
	return &ID{
		id: id,
	}
}

func NewID() *ID {
	return newID(GenerateId())
}

func NewIdentifyer(id string) *ID {
	id = helpers.Strim(id)
	if id == "" {
		return newID(GenerateId())
	}

	return newID(id)
}

// -------------- Identifyer interface -----------------------------------------
func (id *ID) Id() string {
	return id.id
}

//------------------------------------------------------------------------------
