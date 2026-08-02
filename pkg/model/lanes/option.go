package lanes

import (
	"errors"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// LaneSetAdder is a container configuration that can hold lane sets — a Process
// or a Sub-Process, the two FlowElementsContainers BPMN hangs `laneSets` off.
type LaneSetAdder interface {
	options.Configurator

	AddLaneSet(ls *LaneSet) error
}

// LaneSetOption configures a container with lane sets.
//
// It lives here rather than in either container package because both need it and
// neither can own it: `process` imports `activities`, so an option defined in
// one would be unreachable from the other.
type LaneSetOption func(cfg LaneSetAdder) error

// Option marks LaneSetOption as an options.Option; the dispatching constructor
// applies it by calling the func with a config that implements LaneSetAdder.
func (LaneSetOption) Option() {}

// WithLaneSets adds lane sets to a Process or Sub-Process, in the order given.
//
// A nil lane set is refused rather than skipped: unlike the variadic-tolerance
// of WithRoles or WithProperties, a nil here would silently drop a whole
// partitioning of the diagram, which is the loss this element exists to prevent.
func WithLaneSets(sets ...*LaneSet) LaneSetOption {
	f := func(cfg LaneSetAdder) error {
		ee := []error{}

		for i, ls := range sets {
			if ls == nil {
				ee = append(ee, errs.New(
					errs.M("WithLaneSets: a nil LaneSet isn't allowed"),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("lane_set_index", strconv.Itoa(i))))

				continue
			}

			if err := cfg.AddLaneSet(ls); err != nil {
				ee = append(ee, err)
			}
		}

		if len(ee) != 0 {
			return errors.Join(ee...)
		}

		return nil
	}

	return LaneSetOption(f)
}
