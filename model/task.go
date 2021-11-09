package model

import (
	"context"

	"github.com/dr-dobermann/gobpm/thresher"
)

func (st StoreTask) Exec(_ context.Context, tr *track) (state thresher.TrackState, ff []SequenceFlow, err error) {

	for _, v := range st.Vars {
		if _, cerr := tr.instance.vs.NewVar(v); err != nil {
			state = TsError
			err = NewProcExecError(tr, "couldn't add variable %s to instance", cerr)
			return
		}
	}

	state = TsEnded

	return
}
