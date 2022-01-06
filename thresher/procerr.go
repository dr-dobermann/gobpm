package thresher

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/model"
)

type ProcessExecutingError struct {
	pID        model.Id
	instanceID model.Id
	trackID    model.Id
	Err        error
	msg        string
}

func (pee ProcessExecutingError) Error() string {
	return fmt.Sprintf("ERR INST[%v : %v / %v] %s : %v",
		pee.instanceID, pee.pID, pee.trackID, pee.msg, pee.Err)
}

func NewPEErr(trk *Track, err error, format string, params ...interface{}) ProcessExecutingError {
	pee := ProcessExecutingError{msg: fmt.Sprintf(format, params...), Err: err}

	if trk != nil {
		pee.trackID = trk.id
		pee.instanceID = trk.instance.id
		pee.pID = trk.instance.snapshot.ID()
	}

	return pee
}
