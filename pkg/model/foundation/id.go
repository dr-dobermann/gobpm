package foundation

import "strings"

// ID represents a BPMN element identifier.
type ID struct {
	id string
}

func newID(id string) *ID {
	return &ID{
		id: id,
	}
}

// NewID creates a new ID with a generated identifier.
func NewID() *ID {
	return newID(GenerateID())
}

// NewIdentifyer creates a new ID with the specified identifier or generates one if empty.
func NewIdentifyer(id string) *ID {
	id = strings.TrimSpace(id)
	if id == "" {
		return newID(GenerateID())
	}

	return newID(id)
}

// ID returns the identifier string.
func (id *ID) ID() string {
	return id.id
}

//------------------------------------------------------------------------------
