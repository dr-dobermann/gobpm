package infrastructure

import "github.com/dr-dobermann/gobpm/pkg/foundation"

type Import struct {
	Type      string
	location  string
	namespace string
}

type Definition struct {
	foundation.BaseElement

	name               string
	targetNamespace    string
	expressionLanguage string
	typeLanguage       string
	imports            []Import
	extensions         []foundation.Extension
	exporter           string
	exporterVersion    string
}
