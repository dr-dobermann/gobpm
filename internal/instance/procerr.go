package instance

import (
	"fmt"

	mid "github.com/dr-dobermann/gobpm/pkg/identity"
)

type ProcessExecutingError struct {
	pID        mid.Id
	instanceID mid.Id
	trackID    mid.Id
	Err        error
	msg        string
}

func (pee ProcessExecutingError) Error() string {
	return fmt.Sprintf("ERR INST[%v : %v / %v] %s : %v",
		pee.instanceID, pee.pID, pee.trackID, pee.msg, pee.Err)
}

func NewPEErr(trk *track, err error, format string, params ...interface{}) ProcessExecutingError {
	pee := ProcessExecutingError{msg: fmt.Sprintf(format, params...), Err: err}

	if trk != nil {
		pee.trackID = trk.id
		pee.instanceID = trk.instance.id
		pee.pID = trk.instance.snapshot.ID()
	}

	return pee
}
