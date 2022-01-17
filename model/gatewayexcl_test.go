package model

import (
	"testing"

	"github.com/matryer/is"
)

func TestExclGateway(t *testing.T) {
	is := is.New(t)

	p := NewProcess(EmptyID(), "Test Exclusive Gateway", "")
	is.True(p != nil)

}
