package utils

import "fmt"

func ToMap[T any, K comparable](items []T, keySelector func(T) K) (map[K]T, error) {
	if items == nil {
		return nil, fmt.Errorf("items slice cannot be nil")
	}

	result := make(map[K]T, len(items))
	for _, item := range items {
		key := keySelector(item)
		result[key] = item
	}
	return result, nil
}
