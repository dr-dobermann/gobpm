package foundation

type ExtensionAttributeDefinition struct {
	name        string
	attrType    string
	isReference bool
}

type ExtensionDefinition struct {
	name     string
	attrDefs []ExtensionAttributeDefinition
}

type Extension struct {
	mustUnderstand bool
	extDef         ExtensionDefinition
}

type ExtensionAttributeValue struct {
	value      string
	valueRef   string
	attrDefRef *ExtensionAttributeDefinition
}
