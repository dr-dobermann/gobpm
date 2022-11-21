package data

type Property struct {
	ItemAwareElement

	// There is no separate Name field.
	// Use ItemAwareElement.ItemSubjectRef.Name() instead.
}
