package artifact

import "github.com/dr-dobermann/gobpm/pkg/foundation"

type TextAnnotation struct {
	foundation.BaseElement

	text       string
	textFormat string
}
