package set

// Set provides standard set functionality for any comparable type.
type Set[T comparable] struct {
	elements []T
}

// NewSet returns a new Set from values.
func New[T comparable](values ...T) *Set[T] {
	s := Set[T]{
		elements: []T{},
	}

	for _, v := range values {
		if !s.Has(v) {
			s.elements = append(s.elements, v)
		}
	}

	return &s
}

// Count returns number of Set elements.
func (s *Set[T]) Count() int {

	return len(s.elements)
}

// Clear removes all elements from the Set.
func (s *Set[T]) Clear() {
	s.elements = []T{}
}

// Has checks if Set has the value.
func (s *Set[T]) Has(value T) bool {
	for _, e := range s.elements {
		if e == value {
			return true
		}
	}

	return false
}

// Add adds new values to the Set.
func (s *Set[T]) Add(values ...T) {
	for _, value := range values {
		if !s.Has(value) {
			s.elements = append(s.elements, value)
		}
	}
}

// Remove removes values from the Set
func (s *Set[T]) Remove(values ...T) {
	for _, value := range values {
		for i, e := range s.elements {
			if value == e {
				s.elements = append(s.elements[:i], s.elements[i+1:]...)

				break
			}
		}
	}
}

// All returns all elements of the Set.
func (s Set[T]) All() []T {

	return append([]T{}, s.elements...)
}
