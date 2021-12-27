// GoBPM is BPMN v.2 compliant business process engine
//
// (c) 2021, Ruslan Gabitov a.k.a. dr-dobermann.
// Use of this source is governed by LGPL license that
// can be found in the LICENSE file.

/*
Package model as а part of the gobpm package allows to load,
create and save buisiness process models.
*/
package model

import (
	"fmt"
)

type ModelState uint8

const (
	MSCreated ModelState = iota
	MSStarted
	MSFinished
)

type ModelError struct {
	msg string
	Err error
}

func (me ModelError) Error() string {
	return fmt.Sprintf("ME: %s : %s",
		me.msg, me.Err.Error())
}

func NewModelError(err error, format string, params ...interface{}) error {
	return ModelError{fmt.Sprintf(format, params...), err}
}
