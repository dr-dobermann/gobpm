package infrastructure

import (
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

type Import struct {
	Type      string
	Location  string
	Namespace string
	Source    dataprovider.DataProvider
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
