package set

type OrderedSet[T comparable] struct {
	seen  *Set[T]
	order []T
}

func NewOrdered[T comparable]() *OrderedSet[T] {
	return &OrderedSet[T]{
		seen:  New[T](),
		order: make([]T, 0),
	}
}

func OrderedFromSlice[T comparable](items []T) *OrderedSet[T] {
	s := NewOrdered[T]()
	for _, item := range items {
		s.Add(item)
	}
	return s
}

func (s *OrderedSet[T]) Add(item T) bool {
	if s.seen.Contains(item) {
		return false
	}
	s.seen.Add(item)
	s.order = append(s.order, item)
	return true
}

func (s *OrderedSet[T]) Contains(item T) bool {
	return s.seen.Contains(item)
}

func (s *OrderedSet[T]) Size() int {
	return len(s.order)
}

func (s *OrderedSet[T]) ToSlice() []T {
	result := make([]T, len(s.order))
	copy(result, s.order)
	return result
}
