package foundation

import (
	rand "math/rand"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

var generator IDGenerator

// IDGenerator creates a new Id every time Generate called.
type IDGenerator interface {
	Generate() string
}

// GenFunc represents a function that generates IDs.
type GenFunc func() string

// Generate implements IDGenerator interface for GenFunc.
func (gf GenFunc) Generate() string {
	return gf()
}

// SetGenerator sets user-defined Id generator.
func SetGenerator(newGen IDGenerator) error {
	if newGen == nil {
		return errs.New(
			errs.M("generator couldn't be empty"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	generator = newGen

	return nil
}

// GenerateID returns a new ID from generator. If there is no generator, then
// default Generator will be used.
func GenerateID() string {
	if generator == nil {
		if err := SetGenerator(newDefaultGenerator()); err != nil {
			errs.Panic("default generator setup failed: " + err.Error())
		}
	}

	return generator.Generate()
}

// ------------------- Default Generator ---------------------------------------
// defaultIdGenerator is a default based on math/rand/v2 Id generator.
type defaultIDGenerator struct {
	r *rand.Rand
}

func newDefaultGenerator() *defaultIDGenerator {
	return &defaultIDGenerator{
		//nolint: gosec
		r: rand.New(rand.NewSource(time.Now().UnixMilli())),
	}
}

func (g *defaultIDGenerator) Generate() string {
	return strconv.Itoa(int(g.r.Int63()))
}
