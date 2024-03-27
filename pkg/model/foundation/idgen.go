package foundation

import (
	rand "math/rand"
	"strconv"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

var generator IdGenerator

// IdGenerator creates a new Id every time Generate called.
type IdGenerator interface {
	Generate() string
}

type GenFunc func() string

func (gf GenFunc) Generate() string {
	return gf()
}

// SetGenerator sets user-defined Id generator.
func SetGenerator(newGen IdGenerator) error {
	if newGen == nil {
		return errs.New(
			errs.M("generator couldn't be empty"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	generator = newGen

	return nil
}

// GenerateId returns a new Id from generator. If there is no generator, then
// default Genereator will be used.
func GenerateId() string {
	if generator == nil {
		if err := SetGenerator(newDefaultGenerator()); err != nil {
			errs.Panic("default generator setup failed: " + err.Error())
		}
	}

	return generator.Generate()
}

// ------------------- Default Generator ---------------------------------------
// defaultIdGenerator is a default based on math/rand/v2 Id generator.
type defaultIdGenerator struct {
	r *rand.Rand
}

func newDefaultGenerator() *defaultIdGenerator {
	return &defaultIdGenerator{
		//nolint: gosec
		r: rand.New(rand.NewSource(time.Now().UnixMilli())),
	}
}

func (g *defaultIdGenerator) Generate() string {
	return strconv.Itoa(int(g.r.Int63()))
}
