package utils

import (
	"oneTrick/set"
	"slices"
	"strings"
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

func UniqueTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
