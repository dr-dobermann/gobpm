package instance

import "context"

// awaitReply waits for the loop's answer to a request it has already accepted.
// It returns the answer, or stopped=true when the loop ended without one, or
// the context's error.
//
// The loop sends its answer before it closes loopDone, so when the answer
// finishes the instance both channels can be ready at once, and a plain select
// would pick at random and report a stop for an answer that did arrive. On
// loopDone the answer is therefore looked for once more; since the loop's last
// send happens before the close, this check is exact.
func awaitReply[T any](
	ctx context.Context,
	reply <-chan T,
	loopDone <-chan struct{},
) (r T, stopped bool, err error) {
	select {
	case r = <-reply:
		return r, false, nil

	case <-loopDone:
		select {
		case r = <-reply:
			return r, false, nil
		default:
			return r, true, nil
		}

	case <-ctx.Done():
		return r, false, ctx.Err()
	}
}
