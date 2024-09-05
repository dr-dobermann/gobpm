/*
consinp is a package which implement Rendered interface for
user input from console.
*/

package consinp

import "github.com/dr-dobermann/gobpm/pkg/model/data"

type (
	Input struct {
		prompt        string
		name          string
		emptyDisabled bool
		value         data.Value
	}

	CRenderer struct {
		lanes []*Input
	}
)
