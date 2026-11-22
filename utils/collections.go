package utils

import (
	"oneTrick/set"
	"slices"
)

func UniqueNonZero[T comparable](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	var zero T
	unique := set.NewOrdered[T]()
	for _, item := range items {
		if item == zero {
			continue
		}
		unique.Add(item)
	}
	if unique.Size() == 0 {
		return nil
	}
	return unique.ToSlice()
}

func Chunk[T any](items []T, size int) [][]T {
	if len(items) == 0 || size <= 0 {
		return nil
	}
	chunks := make([][]T, 0, (len(items)+size-1)/size)
	for i := 0; i < len(items); i += size {
		end := min(i+size, len(items))
		chunks = append(chunks, items[i:end])
	}
	return chunks
}

func UnionNonZero[T comparable](base, additions []T) []T {
	var zero T
	out := slices.Clone(base)
	seen := set.FromSlice(out)
	for _, add := range additions {
		if add == zero || seen.Contains(add) {
			continue
		}
		seen.Add(add)
		out = append(out, add)
	}
	return out
}
