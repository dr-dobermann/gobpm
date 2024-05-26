package scope

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/helpers"
)

// DataPath is path to data in the scope.
// root path '/' holds process'es Properties and DataObjects.
// executing subprocess and tasks add to the path as next layer on
// their start and removes them on their finish.
// full data path could be as '/subprocess_name/task_name'
type DataPath string

const (
	errorClass = "SCOPE_ERRORS"

	EmptyDataPath DataPath = ""
	RootDataPath  DataPath = "/"

	PathSeparator string = "/"
)

// New creates a new DataPath from correctly formed string.
// If there is errors in newPath, EmptyDataPath and error returned.
func NewDataPath(newPath string) (DataPath, error) {
	d := DataPath(newPath)
	if err := d.Validate(); err != nil {
		return EmptyDataPath, err
	}

	return d, nil
}

// Validate checks the DataPath and if it has any error, it return error.
func (p DataPath) Validate() error {
	invPath := errs.New(
		errs.M("invalid data path (should start from /): %q", p),
		errs.C(errorClass, errs.InvalidParameter))

	s := helpers.Strim(string(p))
	if s == "" {
		return invPath
	}

	fields := strings.Split(s, "/")
	// first element is empty if path starts from '/'
	if fields[0] != "" {
		return invPath
	}

	if len(fields) == 2 && fields[1] == "" {
		return nil
	}

	// fields doesn't have empty or untrimmed values
	for i := 1; i < len(fields); i++ {
		if helpers.Strim(fields[i]) == "" {
			return invPath
		}
	}

	return nil
}

func (p DataPath) String() string {
	return string(p)
}

// DropTail drops last part of the path and returns DataPath consisted the
// rest of the p.
func (p DataPath) DropTail() (DataPath, error) {
	pRef := p.String()
	pp := strings.Split(pRef, PathSeparator)
	if len(pp) == 1 {
		return EmptyDataPath, errs.New(
			errs.M("invalid path to drop its tail: " + pRef))
	}

	if len(pp) == 2 && pp[0] == "" {
		return NewDataPath("/")
	}

	return NewDataPath(strings.Join(pp[:len(pp)-1], PathSeparator))
}

// Append adds non-empyt tail to the DataPath p and retruns new DataPath on
// success or error on failure
func (p DataPath) Append(tail string) (DataPath, error) {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return EmptyDataPath,
			errs.New(
				errs.M("couldn't add empty string to tail"))
	}

	if err := p.Validate(); err != nil {
		return EmptyDataPath, err
	}

	if p == RootDataPath {
		return NewDataPath(p.String() + tail)
	}

	return NewDataPath(string(p) + PathSeparator + tail)
}
