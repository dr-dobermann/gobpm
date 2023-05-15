package artifact

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type TextAnnotation struct {
	foundation.BaseElement

	text       string
	textFormat string
}
