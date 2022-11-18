package foundation

type RelationshipDirection byte

const (
	RdNone RelationshipDirection = iota
	RdForward
	RdBackward
	RdBoth
)

type Relationship struct {
	BaseElement
	relType   string
	direction RelationshipDirection
	sources   []string // element IDs
	targets   []string // element IDs
}
