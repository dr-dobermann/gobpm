package identity

import "github.com/google/uuid"

type Id uuid.UUID

func NewID() Id {
	return Id(uuid.New())
}

func EmptyID() Id {
	return Id(uuid.Nil)
}

func (id Id) String() string {
	return uuid.UUID(id).String()
}

// GetLast returns n last symbols of the given id.
func (id Id) GetLast(n int) string {
	s := id.String()
	if n > len(s) {
		n = len(s)
	}

	return s[len(s)-n:]
}
