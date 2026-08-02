package lanes

import (
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// LaneSet is one partitioning of a container's flow nodes. A Process or
// Sub-Process may declare several, and a Lane may nest one (BPMN 2.0.2 §10.8).
type LaneSet struct {
	lanes []*Lane

	name string

	foundation.BaseElement
}

// NewLaneSet creates a LaneSet over lanes, in declaration order.
//
// Order is kept rather than name-keyed, unlike every other container collection
// in the model: a lane's name is optional and carries no uniqueness rule, so a
// map would be unbuildable for the unnamed case and lossy for duplicates — and
// lane order is visible in every diagram, so reordering changes the model a
// human sees.
//
// An empty name is accepted (cardinality 0..1). A nil lane is refused.
//
// It enforces one normative constraint here rather than at registration, because
// it is visible in the lane set itself: "All Lanes in a single LaneSet MUST
// define partition element of the same type, e.g., all Lanes in a LaneSet
// reference a Resource as the partition element, but each Lane references a
// different Resource instance" (Table 10.135). Lanes that define no partition
// element at all are exempt — the attribute is optional, so declaring none is
// not declaring a conflicting type.
func NewLaneSet(
	name string,
	lanes []*Lane,
	baseOpts ...options.Option,
) (*LaneSet, error) {
	for i, l := range lanes {
		if l == nil {
			return nil,
				errs.New(
					errs.M("LaneSet %q: a nil Lane isn't allowed", name),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("lane_index", strconv.Itoa(i)))
		}
	}

	if err := checkPartitionTypes(name, lanes); err != nil {
		return nil, err
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("LaneSet %q creation failed", name),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &LaneSet{
			BaseElement: *be,
			name:        strings.TrimSpace(name),
			lanes:       slices.Clone(lanes),
		},
		nil
}

// MustLaneSet creates a LaneSet or panics — for tests and examples.
func MustLaneSet(
	name string,
	lanes []*Lane,
	baseOpts ...options.Option,
) *LaneSet {
	ls, err := NewLaneSet(name, lanes, baseOpts...)
	if err != nil {
		errs.Panic(err)
	}

	return ls
}

// checkPartitionTypes enforces Table 10.135's same-type rule over the lanes that
// actually declare a partition element.
//
// The comparison is by dynamic type, which is the only reading available: the
// standard's own example is "all Lanes in a LaneSet reference a Resource … but
// each Lane references a different Resource instance", so the constraint is on
// the type, explicitly not on the value.
func checkPartitionTypes(setName string, lanes []*Lane) error {
	var (
		want  reflect.Type
		first string
	)

	for _, l := range lanes {
		pe := l.PartitionElement()
		if pe == nil {
			continue
		}

		got := reflect.TypeOf(pe)
		if want == nil {
			want, first = got, l.Name()

			continue
		}

		if got != want {
			return errs.New(
				errs.M("LaneSet %q mixes partition element types: lane %q "+
					"declares %s, lane %q declares %s — all lanes in one set "+
					"must partition by the same type",
					setName, first, want, l.Name(), got),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("lane_set", setName))
		}
	}

	return nil
}

// Name returns the lane set's name, which may be empty.
func (ls *LaneSet) Name() string {
	return ls.name
}

// Lanes returns a copy of the set's lanes, in declaration order.
func (ls *LaneSet) Lanes() []*Lane {
	return slices.Clone(ls.lanes)
}
