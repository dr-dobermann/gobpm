package foundation

import (
	rand "math/rand"
	"strconv"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

var (
	// genM guards generator: model elements are constructed from concurrent
	// goroutines (per-execution frame instantiation, concurrent instance
	// startup), so the generator swap and the fetch must not race.
	genM sync.RWMutex

	generator IDGenerator = newDefaultGenerator()
)

// IDGenerator provides unique identifiers for model elements.
type IDGenerator interface {
	Generate() string
}

// GenFunc adapts a plain function to the IDGenerator interface.
type GenFunc func() string

// Generate implements IDGenerator for GenFunc.
func (gf GenFunc) Generate() string {
	return gf()
}

// SetGenerator replaces the package id generator. It is safe to call
// concurrently with GenerateID; in-flight generations finish on the
// previous generator.
func SetGenerator(newGen IDGenerator) error {
	if newGen == nil {
		return errs.New(
			errs.M("generator couldn't be empty"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	genM.Lock()
	defer genM.Unlock()

	generator = newGen

	return nil
}

// GenerateID returns a new identifier from the configured generator.
func GenerateID() string {
	genM.RLock()
	g := generator
	genM.RUnlock()

	return g.Generate()
}

// defaultIDGenerator produces ids from the shared math/rand source — the
// top-level functions are goroutine-safe and auto-seeded, so the generator
// itself carries no state.
type defaultIDGenerator struct{}

func newDefaultGenerator() *defaultIDGenerator {
	return &defaultIDGenerator{}
}

// Generate implements IDGenerator.
func (g *defaultIDGenerator) Generate() string {
	// model-element ids are not security material; the shared math/rand
	// source is used for its goroutine safety, not unpredictability.
	return strconv.Itoa(int(rand.Int63())) //nolint:gosec
}
