package identity

import (
	"testing"

	"github.com/matryer/is"
)

func TestId(t *testing.T) {
	is := is.New(t)

	id := NewID()

	s := id.String()

	is.True(s[len(s)-4:] == id.GetLast(4))
}
