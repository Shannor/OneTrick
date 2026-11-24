package ptr

func NonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func Deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

func DerefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
