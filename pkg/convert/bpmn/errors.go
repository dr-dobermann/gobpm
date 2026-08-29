package bpmn

import (
	"encoding/xml"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// ownError reports whether err is already a converter-classified failure
// that must not be re-wrapped. Re-wrapping would change or bury the error
// class (e.g. ObjectNotFound for an unknown operationRef becoming
// BuildingFailed) — the opposite of k8s/docker-style handling where the
// root cause stays inspectable via errors.As / errs.HasClass.
//
// Matches:
//   - *convert.UnsupportedElementError (SRD-051 §FR-3)
//   - *errs.ApplicationError carrying this package's errorClass
func ownError(err error) bool {
	if err == nil {
		return false
	}

	var uee *convert.UnsupportedElementError
	if errors.As(err, &uee) {
		return true
	}

	var ae *errs.ApplicationError
	if errors.As(err, &ae) && ae.HasClass(errorClass) {
		return true
	}

	return false
}

// wrapErr returns err unchanged when it is already ours; otherwise wraps
// it with message/class so foreign (gobpm constructor, stdlib) failures
// gain a self-identifying outer context without losing the cause (errs.E
// → Unwrap).
func wrapErr(message, class string, err error) error {
	if err == nil {
		return nil
	}

	if ownError(err) {
		return err
	}

	return errs.New(
		errs.M("%s", message),
		errs.C(errorClass, class),
		errs.E(err))
}

// notExpressibleHere refuses an element whose XML form and model form do
// not correspond, naming what to do instead.
//
// This is the rarest refusal and the one most likely to be mistaken for a
// gap: the engine EXECUTES the element, and it is still unreachable from
// a document. Saying only "unsupported" would invite someone to add it.
func notExpressibleHere(se xml.StartElement) error {
	why, ok := refusalReasons[se.Name.Local]
	if !ok {
		why = "its document form and this engine's model form do not correspond"
	}

	return errs.New(
		errs.M("bpmn: <%s> cannot be imported: %s", se.Name.Local, why),
		errs.C(errorClass, errs.InvalidParameter))
}
