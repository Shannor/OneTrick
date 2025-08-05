package set

// Set represents a collection of unique elements.
// It provides methods for adding, removing, and checking
// for the existence of elements.
type Set[T comparable] struct {
	items map[T]struct{}
}

// New creates and returns a new empty Set.
func New[T comparable]() *Set[T] {
	return &Set[T]{
		items: make(map[T]struct{}),
	}
}

// FromSlice creates a new Set from the provided slice of items.
// Any duplicate items in the slice will only be represented once in the Set.
func FromSlice[T comparable](items []T) *Set[T] {
	set := New[T]()
	for _, item := range items {
		set.Add(item)
	}
	return set
}

// Add adds an item to the Set.
// If the item already exists, the Set remains unchanged.
func (s *Set[T]) Add(item T) {
	s.items[item] = struct{}{}
}

// Remove removes an item from the Set.
// If the item doesn't exist, the Set remains unchanged.
func (s *Set[T]) Remove(item T) {
	delete(s.items, item)
}

// Contains checks if the item exists in the Set.
// Returns true if the item exists, false otherwise.
func (s *Set[T]) Contains(item T) bool {
	_, exists := s.items[item]
	return exists
}

// Size returns the number of items in the Set.
func (s *Set[T]) Size() int {
	return len(s.items)
}

// ToSlice returns all the items in the Set as a slice.
// The order of items in the returned slice is not guaranteed.
func (s *Set[T]) ToSlice() []T {
	result := make([]T, 0, len(s.items))
	for item := range s.items {
		result = append(result, item)
	}
	return result
}

// Union returns a new Set containing all elements from both Sets.
func (s *Set[T]) Union(other *Set[T]) *Set[T] {
	result := New[T]()

	for item := range s.items {
		result.Add(item)
	}

	for item := range other.items {
		result.Add(item)
	}

	return result
}

// Intersection returns a new Set containing only elements that exist in both Sets.
func (s *Set[T]) Intersection(other *Set[T]) *Set[T] {
	result := New[T]()

	for item := range s.items {
		if other.Contains(item) {
			result.Add(item)
		}
	}

	return result
}
