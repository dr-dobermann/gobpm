package model

type Documentation struct {
	Text   string
	Format string
}

type FlowDirection uint8

const (
	None FlowDirection = iota
	Begin
	End
	Both
)
